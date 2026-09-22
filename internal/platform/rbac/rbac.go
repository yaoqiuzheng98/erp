package rbac

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Role 租户角色，持有权限码集合。
type Role struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	TenantID  bson.ObjectID `bson:"tenant_id"`
	Code      string        `bson:"code"`
	Name      string        `bson:"name"`
	PermCodes []string      `bson:"perm_codes"`
	DataScope string        `bson:"data_scope"` // self / dept / all
}

// PermissionDef 插件声明的权限码。
type PermissionDef struct {
	Code string // {pluginID}.{resource}.{action}
	Desc string
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
