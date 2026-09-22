package auth

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"golang.org/x/crypto/bcrypt"
)

func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), 12)
	return string(h), err
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// User 租户用户。
type User struct {
	ID           bson.ObjectID   `bson:"_id,omitempty"`
	TenantID     bson.ObjectID   `bson:"tenant_id"`
	Username     string          `bson:"username"`
	PasswordHash string          `bson:"password_hash"`
	Name         string          `bson:"name"`
	DeptID       bson.ObjectID   `bson:"dept_id,omitempty"`
	RoleIDs      []bson.ObjectID `bson:"role_ids"`
	Status       string          `bson:"status"` // active / disabled
	IsTenantAdm  bool            `bson:"is_tenant_admin"`
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

var ErrBadCredential = errors.New("企业名、用户名或密码错误")

// LoginTenant 租户登录：先按企业名定位租户，再在租户内查用户。
// 用户名按租户隔离，不同租户可有同名用户（唯一索引在 tenant_id+username 上）。
func (s *Service) LoginTenant(ctx context.Context, tenantName, username, password string) (*User, error) {
	var t struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := s.tenants.FindOne(ctx, bson.M{"name": tenantName, "status": "active"}).Decode(&t); err != nil {
		return nil, ErrBadCredential
	}
	var u User
	err := s.users.FindOne(ctx, bson.M{"tenant_id": t.ID, "username": username, "status": "active"}).Decode(&u)
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

func (s *Service) UserByID(ctx context.Context, id bson.ObjectID) (*User, error) {
	var u User
	if err := s.users.FindOne(ctx, bson.M{"_id": id}).Decode(&u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Service) SysAdminByID(ctx context.Context, id bson.ObjectID) (*SysAdmin, error) {
	var a SysAdmin
	if err := s.sysadmins.FindOne(ctx, bson.M{"_id": id}).Decode(&a); err != nil {
		return nil, err
	}
	return &a, nil
}
