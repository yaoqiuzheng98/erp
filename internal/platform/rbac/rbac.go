package rbac

import (
	"context"
	"errors"
	"sync"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Role 租户角色，持有权限码集合（以名称标识）。员工经角色获得权限。
type Role struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	TenantID  bson.ObjectID `bson:"tenant_id"`
	Name      string        `bson:"name"`
	PermCodes []string      `bson:"perm_codes"`
}

// PermissionDef 权限码定义。单垂直应用：全量固定目录，无插件贡献。
type PermissionDef struct {
	Code string // {domain}.{resource}.{action}
	Desc string
}

// Catalog 全量权限目录（管理后台角色勾选 + 中间件鉴权共用）。
func Catalog() []PermissionDef {
	return []PermissionDef{
		{Code: "patient.read", Desc: "患者查看"},
		{Code: "patient.write", Desc: "患者建档"},
		{Code: "appt.read", Desc: "预约查看"},
		{Code: "appt.write", Desc: "预约操作"},
		{Code: "admin.users", Desc: "员工管理"},
		{Code: "admin.roles", Desc: "权限角色"},
		{Code: "admin.settings", Desc: "门诊设置"},
		{Code: "admin.audit", Desc: "审计查看"},
	}
}

// defaultRoles 新门诊默认角色：只给门诊两个子菜单的查看权限；
// 患者建档/预约操作等写权限由管理员在「权限角色」按需追加。
func defaultRoles() []Role {
	readonly := []string{"patient.read", "appt.read"}
	return []Role{
		{Name: "医生", PermCodes: readonly},
		{Name: "护士", PermCodes: readonly},
		{Name: "前台", PermCodes: readonly},
	}
}

type Service struct {
	roles  *mongo.Collection
	seeded sync.Map // tenantID hex → true，本进程内各租户只种子一次
}

func NewService(db *mongo.Database) *Service {
	return &Service{roles: db.Collection("roles")}
}

// ensure 懒种子：新门诊首次触及角色时自动建索引+默认角色。
func (s *Service) ensure(ctx context.Context, tenantID bson.ObjectID) {
	if _, ok := s.seeded.Load(tenantID.Hex()); ok {
		return
	}
	if err := s.EnsureSeed(ctx, tenantID); err == nil {
		s.seeded.Store(tenantID.Hex(), true)
	}
}

// EnsureSeed 名称唯一索引 + 空表时种子默认角色（幂等）。
func (s *Service) EnsureSeed(ctx context.Context, tenantID bson.ObjectID) error {
	if _, err := s.roles.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenant_id", Value: 1}, {Key: "name", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return err
	}
	n, err := s.roles.CountDocuments(ctx, bson.M{"tenant_id": tenantID})
	if err != nil {
		return err
	}
	if n > 0 {
		// 已存在的租户：顺手清掉历史模块权限码（幂等）。
		return s.PruneUnknownPerms(ctx, tenantID)
	}
	// 逐角色 upsert：并发首访也不会种出双份（唯一索引兜底）。
	for _, r := range defaultRoles() {
		_, err := s.roles.UpdateOne(ctx,
			bson.M{"tenant_id": tenantID, "name": r.Name},
			bson.M{"$setOnInsert": bson.M{
				"tenant_id": tenantID, "name": r.Name, "perm_codes": r.PermCodes,
			}},
			options.UpdateOne().SetUpsert(true))
		if err != nil && !mongo.IsDuplicateKeyError(err) {
			return err
		}
	}
	return nil
}

// PermSetOf 汇总员工全部角色的权限码；租户管理员拥有全部权限。
func (s *Service) PermSetOf(ctx context.Context, tenantID bson.ObjectID, roleIDs []bson.ObjectID, isTenantAdmin bool) map[string]bool {
	if isTenantAdmin {
		return map[string]bool{"*": true}
	}
	set := map[string]bool{}
	if len(roleIDs) == 0 {
		return set
	}
	cur, err := s.roles.Find(ctx, bson.M{"_id": bson.M{"$in": roleIDs}, "tenant_id": tenantID})
	if err != nil {
		return set
	}
	var roles []Role
	if err := cur.All(ctx, &roles); err != nil {
		return set
	}
	for _, r := range roles {
		for _, p := range r.PermCodes {
			set[p] = true
		}
	}
	return set
}

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID) ([]Role, error) {
	s.ensure(ctx, tenantID)
	cur, err := s.roles.Find(ctx, bson.M{"tenant_id": tenantID},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})) // 按创建顺序
	if err != nil {
		return nil, err
	}
	var out []Role
	return out, cur.All(ctx, &out)
}

func (s *Service) Create(ctx context.Context, r *Role) error {
	s.ensure(ctx, r.TenantID)
	n, err := s.roles.CountDocuments(ctx, bson.M{"tenant_id": r.TenantID, "name": r.Name})
	if err != nil {
		return err
	}
	if n > 0 {
		return errors.New("角色已存在")
	}
	res, err := s.roles.InsertOne(ctx, r)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return errors.New("角色已存在")
		}
		return err
	}
	oid, ok := res.InsertedID.(bson.ObjectID)
	if !ok {
		return errors.New("创建失败: 非法 ID")
	}
	r.ID = oid
	return nil
}

// filterKnownPerms 只保留当前权限目录里的权限码（旧版 catalog/billing 等已删模块的码会被清掉）。
func filterKnownPerms(codes []string) []string {
	known := map[string]bool{}
	for _, p := range Catalog() {
		known[p.Code] = true
	}
	out := make([]string, 0, len(codes))
	seen := map[string]bool{}
	for _, c := range codes {
		if known[c] && !seen[c] {
			out = append(out, c)
			seen[c] = true
		}
	}
	return out
}

// PruneUnknownPerms 清洗租户全部角色的权限码：删除已不存在模块的旧码。
// 角色自定义仍保留，只动当前目录外的历史码。
func (s *Service) PruneUnknownPerms(ctx context.Context, tenantID bson.ObjectID) error {
	cur, err := s.roles.Find(ctx, bson.M{"tenant_id": tenantID})
	if err != nil {
		return err
	}
	var roles []Role
	if err := cur.All(ctx, &roles); err != nil {
		return err
	}
	for _, r := range roles {
		next := filterKnownPerms(r.PermCodes)
		if len(next) == len(r.PermCodes) {
			continue
		}
		if _, err := s.roles.UpdateOne(ctx,
			bson.M{"_id": r.ID, "tenant_id": tenantID},
			bson.M{"$set": bson.M{"perm_codes": next}}); err != nil {
			return err
		}
	}
	return nil
}

func Has(set map[string]bool, code string) bool {
	if set["*"] {
		return true
	}
	return set[code]
}
