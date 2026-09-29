package patient

import (
	"context"
	"errors"
	"time"

	"erp/internal/platform/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Service struct {
	pats *repo.TenantRepo[Patient]
}

func New(db *mongo.Database) *Service {
	return &Service{pats: repo.NewTenantRepo[Patient](db, "patients")}
}

// List 姓名/电话模糊搜。
func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, q string, skip, limit int64) ([]Patient, int64, error) {
	f := bson.M{}
	if q != "" {
		f["$or"] = []bson.M{
			{"name": bson.M{"$regex": q}},
			{"phone": bson.M{"$regex": q}},
		}
	}
	total, err := s.pats.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.pats.FindMany(ctx, tenantID, f)
	return list, total, err
}

func (s *Service) ByID(ctx context.Context, tenantID, id bson.ObjectID) (*Patient, error) {
	return s.pats.FindByID(ctx, tenantID, id)
}

// Create 建档：按电话自动认领已有患者（有则报错防重），认领不到才新建。
func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, p *Patient) error {
	if p.Name == "" {
		return errors.New("姓名必填")
	}
	if p.Phone != "" {
		if n, _ := s.pats.Count(ctx, tenantID, bson.M{"phone": p.Phone}); n > 0 {
			return errors.New("该电话已建过患者档案")
		}
	}
	p.TenantID, p.CreatedAt = tenantID, time.Now()
	p.ID, _ = s.pats.Insert(ctx, tenantID, p)
	_, err := s.pats.FindByID(ctx, tenantID, p.ID)
	return err
}

// SetTooth 设置牙位状态；空状态 = 恢复健康（删除键）。
func (s *Service) SetTooth(ctx context.Context, tenantID, patID bson.ObjectID, tooth, status string) error {
	if status == "" {
		_, err := s.pats.Col.UpdateOne(ctx,
			bson.M{"_id": patID, "tenant_id": tenantID},
			bson.M{"$unset": bson.M{"teeth." + tooth: ""}})
		return err
	}
	_, err := s.pats.Col.UpdateOne(ctx,
		bson.M{"_id": patID, "tenant_id": tenantID},
		bson.M{"$set": bson.M{"teeth." + tooth: status}})
	return err
}
