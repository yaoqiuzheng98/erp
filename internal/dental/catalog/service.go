package catalog

import (
	"context"
	"errors"
	"time"

	"erp/internal/platform/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Service struct {
	db    *mongo.Database
	items *repo.TenantRepo[ServiceItem]
}

func New(db *mongo.Database) *Service {
	return &Service{db: db, items: repo.NewTenantRepo[ServiceItem](db, "service_items")}
}

// EnsureSeed 建名称唯一索引 + 空表时种子默认价目（幂等）。
func (s *Service) EnsureSeed(ctx context.Context, tenantID bson.ObjectID) error {
	if _, err := s.items.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "name", Value: 1}},
	}); err != nil {
		return err
	}
	n, err := s.items.Count(ctx, tenantID, bson.M{})
	if err != nil || n > 0 {
		return err
	}
	defaults := []ServiceItem{
		{Name: "初诊检查", Category: "检查", Price: 50, Unit: "次", Status: "active"},
		{Name: "口腔拍片", Category: "检查", Price: 100, Unit: "次", Status: "active"},
		{Name: "超声洁治", Category: "洁治", Price: 300, Unit: "次", Status: "active"},
		{Name: "树脂补牙", Category: "充填", Price: 300, Unit: "颗", Status: "active"},
		{Name: "根管治疗", Category: "根管", Price: 1200, Unit: "颗", Status: "active"},
		{Name: "简单拔牙", Category: "拔牙", Price: 300, Unit: "颗", Status: "active"},
		{Name: "阻生智齿拔除", Category: "拔牙", Price: 1200, Unit: "颗", Status: "active"},
		{Name: "正畸复诊", Category: "正畸", Price: 200, Unit: "次", Status: "active"},
		{Name: "烤瓷冠修复", Category: "修复", Price: 1500, Unit: "颗", Status: "active"},
		{Name: "种植牙", Category: "种植", Price: 8000, Unit: "颗", Status: "active"},
	}
	now := time.Now()
	docs := make([]any, 0, len(defaults))
	for _, d := range defaults {
		d.TenantID, d.CreatedAt = tenantID, now
		docs = append(docs, d)
	}
	_, err = s.db.Collection("service_items").InsertMany(ctx, docs)
	return err
}

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, activeOnly bool) ([]ServiceItem, error) {
	f := bson.M{}
	if activeOnly {
		f["status"] = "active"
	}
	return s.items.FindMany(ctx, tenantID, f)
}

func (s *Service) ByID(ctx context.Context, tenantID, id bson.ObjectID) (*ServiceItem, error) {
	return s.items.FindByID(ctx, tenantID, id)
}

func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, it *ServiceItem) error {
	if it.Name == "" {
		return errors.New("项目名称必填")
	}
	if n, _ := s.items.Count(ctx, tenantID, bson.M{"name": it.Name}); n > 0 {
		return errors.New("同名项目已存在")
	}
	if it.Price < 0 {
		return errors.New("单价不能为负")
	}
	if it.Unit == "" {
		it.Unit = "次"
	}
	it.TenantID, it.CreatedAt = tenantID, time.Now()
	if it.Status == "" {
		it.Status = "active"
	}
	_, err := s.items.Insert(ctx, tenantID, it)
	return err
}

func (s *Service) Update(ctx context.Context, tenantID, id bson.ObjectID, set bson.M) error {
	return s.items.Update(ctx, tenantID, id, set)
}

func (s *Service) Delete(ctx context.Context, tenantID, id bson.ObjectID) error {
	return s.items.Delete(ctx, tenantID, id)
}
