package auth

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), 12)
	return string(h), err
}

// CheckPassword 校验明文密码；空哈希（未设密码）一律不通过。
func CheckPassword(hash, plain string) bool {
	if hash == "" || plain == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

var phoneRe = regexp.MustCompile(`^1[3-9]\d{9}$`)

// ValidPhone 大陆手机号：11 位数字、1 开头。员工登录名与联系方式共用。
func ValidPhone(p string) bool { return phoneRe.MatchString(p) }

// MinPasswordLen 员工密码最小长度（创建/重置/自改一致）。
const MinPasswordLen = 6

var (
	ErrBadCredential = errors.New("门诊名、手机号或密码错误")
	ErrPhoneUsed     = errors.New("手机号已被其他员工使用")
	ErrPhoneInvalid  = errors.New("手机号格式不正确")
	ErrPwdTooShort   = errors.New("密码至少 6 位")
)

// User 租户用户，即员工：手机号+密码登录，角色定权限。
// Title/Years/Specialty 为患者端医生团队页展示的执业信息。
type User struct {
	ID           bson.ObjectID   `bson:"_id,omitempty"`
	TenantID     bson.ObjectID   `bson:"tenant_id"`
	Phone        string          `bson:"phone"`
	PasswordHash string          `bson:"password_hash"`
	Name         string          `bson:"name"`
	RoleIDs      []bson.ObjectID `bson:"role_ids"`
	Bio          string          `bson:"bio,omitempty"`        // 个人简介：患者端首页团队展示
	Title        string          `bson:"title,omitempty"`      // 职称：如 主任医师
	Years        int             `bson:"years,omitempty"`      // 从业年限
	Specialty    string          `bson:"specialty,omitempty"`  // 专科：如 口腔正畸专科
	HideHome     bool            `bson:"hide_home,omitempty"`  // 首页不展示该成员
	HomeOrder    int             `bson:"home_order,omitempty"` // 首页展示权重（大在前，0=最后）
	Status       string          `bson:"status"`               // active / disabled
	IsTenantAdm  bool            `bson:"is_tenant_admin"`
	Avatar       string          `bson:"avatar,omitempty"` // 大头照 GridFS 文件 ID hex
	LastLoginAt  time.Time       `bson:"last_login_at,omitempty"`
}

// SysAdmin 系统超管，无 tenant_id。
type SysAdmin struct {
	ID           bson.ObjectID `bson:"_id,omitempty"`
	Username     string        `bson:"username"`
	PasswordHash string        `bson:"password_hash"`
}

type Service struct {
	users     *mongo.Collection
	sysadmins *mongo.Collection
	tenants   *mongo.Collection
}

func NewService(db *mongo.Database) *Service {
	return &Service{
		users:     db.Collection("users"),
		sysadmins: db.Collection("sys_admins"),
		tenants:   db.Collection("tenants"),
	}
}

// ---------- 登录 ----------

// LoginTenant 租户登录：先按门诊名定位租户，再按手机号查员工。
func (s *Service) LoginTenant(ctx context.Context, tenantName, phone, password string) (*User, error) {
	if !ValidPhone(phone) {
		return nil, ErrBadCredential
	}
	var t struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := s.tenants.FindOne(ctx, bson.M{"name": tenantName, "status": "active"}).Decode(&t); err != nil {
		return nil, ErrBadCredential
	}
	var u User
	err := s.users.FindOne(ctx, bson.M{"tenant_id": t.ID, "phone": phone, "status": "active"}).Decode(&u)
	if err != nil || !CheckPassword(u.PasswordHash, password) {
		return nil, ErrBadCredential
	}
	_, _ = s.users.UpdateOne(ctx, bson.M{"_id": u.ID}, bson.M{"$set": bson.M{"last_login_at": time.Now()}})
	return &u, nil
}

func (s *Service) LoginSys(ctx context.Context, username, password string) (*SysAdmin, error) {
	var a SysAdmin
	err := s.sysadmins.FindOne(ctx, bson.M{"username": username}).Decode(&a)
	if err != nil || !CheckPassword(a.PasswordHash, password) {
		return nil, ErrBadCredential
	}
	return &a, nil
}

// ---------- 员工（租户用户） ----------

// UserByID 按租户+ID 取员工（强制租户隔离）。
func (s *Service) UserByID(ctx context.Context, tenantID, id bson.ObjectID) (*User, error) {
	var u User
	if err := s.users.FindOne(ctx, bson.M{"_id": id, "tenant_id": tenantID}).Decode(&u); err != nil {
		return nil, err
	}
	return &u, nil
}

// CountAdmins 门诊管理员人数（取消管理员身份前的护栏）。
func (s *Service) CountAdmins(ctx context.Context, tenantID bson.ObjectID) (int64, error) {
	return s.users.CountDocuments(ctx, bson.M{"tenant_id": tenantID, "is_tenant_admin": true})
}

// SortByWeight 首页权重排序（Strategy/Comparator：权重降序，同重按姓名升序）。
// portal 的 teamMember 与 admin 的用户列表复用同一比较器，改规则只改这里。
func SortByWeight[T any](list []T, weight func(T) int, name func(T) string) {
	sort.Slice(list, func(i, j int) bool {
		if weight(list[i]) != weight(list[j]) {
			return weight(list[i]) > weight(list[j])
		}
		return name(list[i]) < name(list[j])
	})
}

// SortUsersForHome 首页配置/患者端首页共用的员工排序。
func SortUsersForHome(users []User) {
	SortByWeight(users,
		func(u User) int { return u.HomeOrder },
		func(u User) string { return u.Name })
}

// List 员工名册（含离职），按入职顺序。
func (s *Service) List(ctx context.Context, tenantID bson.ObjectID) ([]User, error) {
	cur, err := s.users.Find(ctx, bson.M{"tenant_id": tenantID},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var out []User
	return out, cur.All(ctx, &out)
}

// ListActive 在职员工：挂号选医生用（全员可接诊）。
func (s *Service) ListActive(ctx context.Context, tenantID bson.ObjectID) ([]User, error) {
	cur, err := s.users.Find(ctx, bson.M{
		"tenant_id": tenantID, "status": "active",
	})
	if err != nil {
		return nil, err
	}
	var act []User
	return act, cur.All(ctx, &act)
}

// Create 新建员工：姓名/手机号/初始密码必填，手机号租户内唯一。
func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, u *User, plain string) error {
	u.Name = strings.TrimSpace(u.Name)
	if u.Name == "" {
		return errors.New("姓名必填")
	}
	if !ValidPhone(u.Phone) {
		return ErrPhoneInvalid
	}
	if len(plain) < MinPasswordLen {
		return ErrPwdTooShort
	}
	n, err := s.users.CountDocuments(ctx, bson.M{"tenant_id": tenantID, "phone": u.Phone})
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrPhoneUsed
	}
	hash, err := HashPassword(plain)
	if err != nil {
		return err
	}
	u.TenantID, u.PasswordHash = tenantID, hash
	if u.Status == "" {
		u.Status = "active"
	}
	res, err := s.users.InsertOne(ctx, u)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrPhoneUsed
		}
		return err
	}
	oid, ok := res.InsertedID.(bson.ObjectID)
	if !ok {
		return errors.New("创建失败: 非法 ID")
	}
	u.ID = oid
	return nil
}

// Update 更新员工资料（姓名/手机号/角色/状态），不动密码。
func (s *Service) Update(ctx context.Context, tenantID, id bson.ObjectID, set bson.M) error {
	if p, ok := set["phone"].(string); ok {
		if !ValidPhone(p) {
			return ErrPhoneInvalid
		}
		n, err := s.users.CountDocuments(ctx, bson.M{
			"tenant_id": tenantID, "phone": p, "_id": bson.M{"$ne": id},
		})
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrPhoneUsed
		}
	}
	if n, ok := set["name"].(string); ok && strings.TrimSpace(n) == "" {
		return errors.New("姓名必填")
	}
	res, err := s.users.UpdateOne(ctx, bson.M{"_id": id, "tenant_id": tenantID}, bson.M{"$set": set})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrPhoneUsed
		}
		return err
	}
	if res.MatchedCount == 0 {
		return errors.New("员工不存在")
	}
	return nil
}

// SetPassword 管理员重置员工密码。
func (s *Service) SetPassword(ctx context.Context, tenantID, id bson.ObjectID, plain string) error {
	if len(plain) < MinPasswordLen {
		return ErrPwdTooShort
	}
	hash, err := HashPassword(plain)
	if err != nil {
		return err
	}
	res, err := s.users.UpdateOne(ctx,
		bson.M{"_id": id, "tenant_id": tenantID},
		bson.M{"$set": bson.M{"password_hash": hash}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errors.New("员工不存在")
	}
	return nil
}

// ChangePassword 员工自助改密：校验原密码。
func (s *Service) ChangePassword(ctx context.Context, tenantID, id bson.ObjectID, oldPlain, newPlain string) error {
	u, err := s.UserByID(ctx, tenantID, id)
	if err != nil {
		return errors.New("员工不存在")
	}
	if !CheckPassword(u.PasswordHash, oldPlain) {
		return errors.New("原密码不正确")
	}
	return s.SetPassword(ctx, tenantID, id, newPlain)
}

func (s *Service) SysAdminByID(ctx context.Context, id bson.ObjectID) (*SysAdmin, error) {
	var a SysAdmin
	if err := s.sysadmins.FindOne(ctx, bson.M{"_id": id}).Decode(&a); err != nil {
		return nil, err
	}
	return &a, nil
}
