package rbac

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Role 租户角色，持有权限码集合（以名称标识）。
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
		{Code: "patient.write", Desc: "患者建档/牙位"},
		{Code: "appt.read", Desc: "预约查看"},
		{Code: "appt.write", Desc: "预约操作/收费"},
		{Code: "catalog.read", Desc: "价目查看"},
		{Code: "catalog.write", Desc: "价目维护"},
		{Code: "staff.read", Desc: "员工查看"},
		{Code: "staff.write", Desc: "员工维护"},
		{Code: "billing.read", Desc: "财务查看"},
		{Code: "billing.write", Desc: "财务操作"},
		{Code: "admin.users", Desc: "用户管理"},
		{Code: "admin.roles", Desc: "角色管理"},
		{Code: "admin.settings", Desc: "门诊设置"},
		{Code: "admin.audit", Desc: "审计查看"},
	}
}

type Service struct {
	roles *mongo.Collection
}

func NewService(db *mongo.Database) *Service {
	return &Service{roles: db.Collection("roles")}
}

// PermSetOf 汇总用户全部角色的权限码；租户管理员拥有全部权限。
func (s *Service) PermSetOf(ctx context.Context, roleIDs []bson.ObjectID, isTenantAdmin bool, catalog []PermissionDef) map[string]bool {
	if isTenantAdmin {
		set := map[string]bool{"*": true}
		return set
	}
	set := map[string]bool{}
	if len(roleIDs) == 0 {
		return set
	}
	cur, err := s.roles.Find(ctx, bson.M{"_id": bson.M{"$in": roleIDs}})
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
	cur, err := s.roles.Find(ctx, bson.M{"tenant_id": tenantID})
	if err != nil {
		return nil, err
	}
	var out []Role
	return out, cur.All(ctx, &out)
}

func (s *Service) Create(ctx context.Context, r *Role) error {
	res, err := s.roles.InsertOne(ctx, r)
	if err == nil {
		r.ID = res.InsertedID.(bson.ObjectID)
	}
	return err
}

func (s *Service) Delete(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.roles.DeleteOne(ctx, bson.M{"_id": id, "tenant_id": tenantID})
	return err
}

func Has(set map[string]bool, code string) bool {
	if set["*"] {
		return true
	}
	return set[code]
}
