package org

import (
	"context"
	"time"

	"erp/internal/platform/model"
	"erp/internal/platform/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Department 部门树节点。
type Department struct {
	model.Doc `bson:",inline"`
	Name      string        `bson:"name"`
	ParentID  bson.ObjectID `bson:"parent_id,omitempty"`
	Sort      int           `bson:"sort"`
}

type Service struct {
	repo *repo.TenantRepo[Department]
}

func NewService(db *mongo.Database) *Service {
	return &Service{repo: repo.NewTenantRepo[Department](db, "departments")}
}

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID) ([]Department, error) {
	return s.repo.FindMany(ctx, tenantID, bson.M{})
}

func (s *Service) Create(ctx context.Context, d *Department) error {
	d.CreatedAt = time.Now()
	_, err := s.repo.Insert(ctx, d.TenantID, d)
	return err
}

func (s *Service) Delete(ctx context.Context, tenantID, id bson.ObjectID) error {
	return s.repo.Delete(ctx, tenantID, id)
}
