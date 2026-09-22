package tenant

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Tenant 企业租户。
type Tenant struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	Name      string        `bson:"name"`
	Code      string        `bson:"code"`
	Status    string        `bson:"status"` // active / suspended
	CreatedAt time.Time     `bson:"created_at"`
}

var ErrSuspended = errors.New("租户已停用")

type Service struct {
	col *mongo.Collection
}

func NewService(db *mongo.Database) *Service {
	return &Service{col: db.Collection("tenants")}
}

func (s *Service) ByID(ctx context.Context, id bson.ObjectID) (*Tenant, error) {
	var t Tenant
	if err := s.col.FindOne(ctx, bson.M{"_id": id}).Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Service) List(ctx context.Context) ([]Tenant, error) {
	cur, err := s.col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	var out []Tenant
	return out, cur.All(ctx, &out)
}

func (s *Service) Create(ctx context.Context, name, code string) (*Tenant, error) {
	t := &Tenant{Name: name, Code: code, Status: "active", CreatedAt: time.Now()}
	res, err := s.col.InsertOne(ctx, t)
	if err != nil {
		return nil, err
	}
	t.ID = res.InsertedID.(bson.ObjectID)
	return t, nil
}

func (s *Service) SetStatus(ctx context.Context, id bson.ObjectID, status string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"status": status}})
	return err
}
