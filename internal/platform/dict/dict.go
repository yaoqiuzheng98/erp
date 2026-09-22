package dict

import (
	"context"
	"time"

	"erp/internal/platform/model"
	"erp/internal/platform/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Entry 字典项：type+code 定位，如 unit/pcs。
type Entry struct {
	model.Doc `bson:",inline"`
	Type      string `bson:"type"`
	Code      string `bson:"code"`
	Label     string `bson:"label"`
	Sort      int    `bson:"sort"`
	Status    string `bson:"status"`
}

// Param 租户级参数键值。
type Param struct {
	model.Doc `bson:",inline"`
	Key       string `bson:"key"`
	Value     string `bson:"value"`
	Desc      string `bson:"desc"`
}

type Service struct {
	dicts  *repo.TenantRepo[Entry]
	params *repo.TenantRepo[Param]
}

func NewService(db *mongo.Database) *Service {
	return &Service{
		dicts:  repo.NewTenantRepo[Entry](db, "dicts"),
		params: repo.NewTenantRepo[Param](db, "params"),
	}
}

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, typ string) ([]Entry, error) {
	f := bson.M{}
	if typ != "" {
		f["type"] = typ
	}
	return s.dicts.FindMany(ctx, tenantID, f)
}

func (s *Service) Create(ctx context.Context, e *Entry) error {
	e.CreatedAt = time.Now()
	if e.Status == "" {
		e.Status = "active"
	}
	_, err := s.dicts.Insert(ctx, e.TenantID, e)
	return err
}

func (s *Service) Delete(ctx context.Context, tenantID, id bson.ObjectID) error {
	return s.dicts.Delete(ctx, tenantID, id)
}
