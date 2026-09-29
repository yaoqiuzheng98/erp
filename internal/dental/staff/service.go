package staff

import (
	"context"
	"errors"
	"sync"
	"time"

	"erp/internal/platform/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Service struct {
	staff  *repo.TenantRepo[Staff]
	roles  *repo.TenantRepo[RoleDef]
	seeded sync.Map // tenantID hex → true，本进程内各租户只种子一次
}

// ensure 懒种子：新门诊首次访问员工相关功能时自动建索引+默认角色+老码迁移。
func (s *Service) ensure(ctx context.Context, tenantID bson.ObjectID) {
	if _, ok := s.seeded.Load(tenantID.Hex()); ok {
		return
	}
	if err := s.EnsureSeed(ctx, tenantID); err == nil {
		s.seeded.Store(tenantID.Hex(), true)
	}
}

func New(db *mongo.Database) *Service {
	return &Service{
		staff: repo.NewTenantRepo[Staff](db, "staff"),
		roles: repo.NewTenantRepo[RoleDef](db, "staff_roles"),
	}
}

// EnsureSeed 唯一索引 + 默认角色（幂等）+ 存量英文码迁移。
func (s *Service) EnsureSeed(ctx context.Context, tenantID bson.ObjectID) error {
	if _, err := s.staff.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "name", Value: 1}},
	}); err != nil {
		return err
	}
	if _, err := s.roles.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "name", Value: 1}},
	}); err != nil {
		return err
	}
	// 存量英文码 → 中文名（硬编码时代的老数据）。
	for old, name := range map[string]string{
		"doctor": "医生", "nurse": "护士", "front": "前台", "assistant": "助理",
	} {
		_, _ = s.staff.Col.UpdateMany(ctx,
			bson.M{"tenant_id": tenantID, "role": old},
			bson.M{"$set": bson.M{"role": name}})
	}
	n, err := s.roles.Count(ctx, tenantID, bson.M{})
	if err != nil || n > 0 {
		return err
	}
	now := time.Now()
	docs := []any{}
	for _, r := range []RoleDef{
		{Name: "医生", CanPractice: true, Status: "active"},
		{Name: "护士", Status: "active"},
		{Name: "前台", Status: "active"},
		{Name: "助理", Status: "active"},
	} {
		r.TenantID, r.CreatedAt = tenantID, now
		docs = append(docs, r)
	}
	_, err = s.roles.Col.InsertMany(ctx, docs)
	if err != nil && mongo.IsDuplicateKeyError(err) {
		return nil // 并发首访重复种子，唯一索引保证下幂等
	}
	return err
}

// ---------- 角色 ----------

func (s *Service) ListRoles(ctx context.Context, tenantID bson.ObjectID) ([]RoleDef, error) {
	s.ensure(ctx, tenantID)
	return s.roles.FindMany(ctx, tenantID, bson.M{})
}

func (s *Service) ActiveRoles(ctx context.Context, tenantID bson.ObjectID) ([]RoleDef, error) {
	s.ensure(ctx, tenantID)
	return s.roles.FindMany(ctx, tenantID, bson.M{"status": "active"})
}

func (s *Service) CreateRole(ctx context.Context, tenantID bson.ObjectID, name string, canPractice bool) error {
	s.ensure(ctx, tenantID)
	if name == "" {
		return errors.New("角色名必填")
	}
	if n, _ := s.roles.Count(ctx, tenantID, bson.M{"name": name}); n > 0 {
		return errors.New("角色已存在")
	}
	r := &RoleDef{Name: name, CanPractice: canPractice}
	r.TenantID, r.CreatedAt = tenantID, time.Now()
	r.Status = "active"
	_, err := s.roles.Insert(ctx, tenantID, r)
	return err
}

func (s *Service) SetRole(ctx context.Context, tenantID, id bson.ObjectID, status string, canPractice bool) error {
	if status != "active" && status != "disabled" {
		return errors.New("状态无效")
	}
	return s.roles.Update(ctx, tenantID, id, bson.M{"status": status, "can_practice": canPractice})
}

func (s *Service) DeleteRole(ctx context.Context, tenantID, id bson.ObjectID) error {
	r, err := s.roles.FindByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if n, _ := s.staff.Count(ctx, tenantID, bson.M{"role": r.Name}); n > 0 {
		return errors.New("仍有员工使用该角色，先调整员工")
	}
	return s.roles.Delete(ctx, tenantID, id)
}

// practicableRoles 可接诊的在职角色名。
func (s *Service) practicableRoles(ctx context.Context, tenantID bson.ObjectID) []string {
	s.ensure(ctx, tenantID)
	list, err := s.roles.FindMany(ctx, tenantID, bson.M{"status": "active", "can_practice": true})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, r := range list {
		out = append(out, r.Name)
	}
	return out
}

// CanPractice 该角色名是否可接诊（角色不存在/停用/未勾选均 false）。
func (s *Service) CanPractice(ctx context.Context, tenantID bson.ObjectID, role string) bool {
	s.ensure(ctx, tenantID)
	for _, n := range s.practicableRoles(ctx, tenantID) {
		if n == role {
			return true
		}
	}
	return false
}

func (s *Service) checkRole(ctx context.Context, tenantID bson.ObjectID, role string) error {
	s.ensure(ctx, tenantID)
	r, err := s.roles.FindOne(ctx, tenantID, bson.M{"name": role})
	if err != nil || r.Status != "active" {
		return errors.New("角色不存在或已停用")
	}
	return nil
}

// ---------- 员工 ----------

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, activeOnly bool) ([]Staff, error) {
	s.ensure(ctx, tenantID)
	f := bson.M{}
	if activeOnly {
		f["status"] = "active"
	}
	return s.staff.FindMany(ctx, tenantID, f)
}

// ListDoctors 预约用：只列可接诊角色的在职员工。
func (s *Service) ListDoctors(ctx context.Context, tenantID bson.ObjectID) ([]Staff, error) {
	s.ensure(ctx, tenantID)
	names := s.practicableRoles(ctx, tenantID)
	if len(names) == 0 {
		return nil, nil
	}
	return s.staff.FindMany(ctx, tenantID, bson.M{"status": "active", "role": bson.M{"$in": names}})
}

func (s *Service) ByID(ctx context.Context, tenantID, id bson.ObjectID) (*Staff, error) {
	return s.staff.FindByID(ctx, tenantID, id)
}

func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, st *Staff) error {
	s.ensure(ctx, tenantID)
	if st.Name == "" {
		return errors.New("姓名必填")
	}
	if err := s.checkRole(ctx, tenantID, st.Role); err != nil {
		return err
	}
	if n, _ := s.staff.Count(ctx, tenantID, bson.M{"name": st.Name}); n > 0 {
		return errors.New("同名成员已存在")
	}
	st.TenantID, st.CreatedAt = tenantID, time.Now()
	if st.Status == "" {
		st.Status = "active"
	}
	_, err := s.staff.Insert(ctx, tenantID, st)
	return err
}

func (s *Service) Update(ctx context.Context, tenantID, id bson.ObjectID, set bson.M) error {
	if r, ok := set["role"].(string); ok && r != "" {
		if err := s.checkRole(ctx, tenantID, r); err != nil {
			return err
		}
	}
	return s.staff.Update(ctx, tenantID, id, set)
}

func (s *Service) Delete(ctx context.Context, tenantID, id bson.ObjectID) error {
	return s.staff.Delete(ctx, tenantID, id)
}
