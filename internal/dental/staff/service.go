package staff

import (
	"context"
	"errors"
	"time"

	"erp/internal/platform/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Service struct {
	staff *repo.TenantRepo[Staff]
}

func New(db *mongo.Database) *Service {
	return &Service{staff: repo.NewTenantRepo[Staff](db, "staff")}
}

// EnsureIndexes 姓名租户内唯一。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	_, err := s.staff.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "name", Value: 1}},
	})
	return err
}

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, activeOnly bool) ([]Staff, error) {
	f := bson.M{}
	if activeOnly {
		f["status"] = "active"
	}
	return s.staff.FindMany(ctx, tenantID, f)
}

// ListDoctors 预约用：只列在职医生。
func (s *Service) ListDoctors(ctx context.Context, tenantID bson.ObjectID) ([]Staff, error) {
	return s.staff.FindMany(ctx, tenantID, bson.M{"status": "active", "role": Doctor})
}

func (s *Service) ByID(ctx context.Context, tenantID, id bson.ObjectID) (*Staff, error) {
	return s.staff.FindByID(ctx, tenantID, id)
}

func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, st *Staff) error {
	if st.Name == "" {
		return errors.New("姓名必填")
	}
	if _, ok := Roles[st.Role]; !ok {
		return errors.New("角色无效")
	}
	if n, _ := s.staff.Count(ctx, tenantID, bson.M{"name": st.Name}); n > 0 {
		return errors.New("同名员工已存在")
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
		if _, valid := Roles[r]; !valid {
			return errors.New("角色无效")
		}
	}
	return s.staff.Update(ctx, tenantID, id, set)
}

func (s *Service) Delete(ctx context.Context, tenantID, id bson.ObjectID) error {
	return s.staff.Delete(ctx, tenantID, id)
}
