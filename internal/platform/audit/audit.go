package audit

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Entry 审计日志。
type Entry struct {
	ID       bson.ObjectID `bson:"_id,omitempty"`
	TenantID bson.ObjectID `bson:"tenant_id,omitempty"`
	UserID   bson.ObjectID `bson:"user_id,omitempty"`
	Username string        `bson:"username"`
	Action   string        `bson:"action"` // login / plugin.enable / user.create ...
	Target   string        `bson:"target"`
	Detail   string        `bson:"detail"`
	IP       string        `bson:"ip"`
	At       time.Time     `bson:"at"`
}

type Service struct {
	col *mongo.Collection
}

func New(db *mongo.Database) *Service {
	return &Service{col: db.Collection("audit_logs")}
}

func (s *Service) Log(ctx context.Context, e Entry) {
	e.At = time.Now()
	_, _ = s.col.InsertOne(ctx, e)
}

func (s *Service) List(ctx context.Context, filter bson.M, limit int64) ([]Entry, error) {
	opts := options.Find().SetSort(bson.D{{Key: "at", Value: -1}}).SetLimit(limit)
	cur, err := s.col.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var out []Entry
	return out, cur.All(ctx, &out)
}
