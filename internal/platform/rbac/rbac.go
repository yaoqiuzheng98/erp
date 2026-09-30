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
		{Code: "patient.write", Desc: "患者建档/牙位"},
		{Code: "appt.read", Desc: "预约查看"},
		{Code: "appt.write", Desc: "预约操作/收费"},
		{Code: "catalog.read", Desc: "价目查看"},
		{Code: "catalog.write", Desc: "价目维护"},
		{Code: "billing.read", Desc: "财务查看"},
		{Code: "billing.write", Desc: "财务操作"},
		{Code: "admin.users", Desc: "员工管理"},
		{Code: "admin.roles", Desc: "权限角色"},
		{Code: "admin.settings", Desc: "门诊设置"},
		{Code: "admin.audit", Desc: "审计查看"},
	}
}

// defaultRoles 新门诊默认角色（名称→权限码）。店长=全量权限。
func defaultRoles() []Role {
	return []Role{
		{Name: "医生", PermCodes: []string{
			"patient.read", "patient.write", "appt.read", "appt.write",
			"catalog.read", "billing.read",
		}},
		{Name: "护士", PermCodes: []string{
			"patient.read", "appt.read", "catalog.read",
		}},
		{Name: "前台", PermCodes: []string{
			"patient.read", "patient.write", "appt.read", "appt.write",
			"catalog.read", "billing.read", "billing.write",
		}},
		{Name: "店长", PermCodes: allCodes()},
	}
}

func allCodes() []string {
	defs := Catalog()
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Code)
	}
	return out
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
	if err != nil || n > 0 {
		return err
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
	r.ID = res.InsertedID.(bson.ObjectID)
	return nil
}

// Delete 删除角色。调用方应先确认无员工在用（auth.CountByRole）。
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
