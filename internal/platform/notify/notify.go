package notify

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Message 站内通知。
type Message struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	TenantID  bson.ObjectID `bson:"tenant_id"`
	UserID    bson.ObjectID `bson:"user_id"`
	Type      string        `bson:"type"` // todo / alert / info
	Title     string        `bson:"title"`
	Link      string        `bson:"link"`
	ReadAt    time.Time     `bson:"read_at,omitempty"`
	CreatedAt time.Time     `bson:"created_at"`
}

type Service struct {
	col *mongo.Collection
}

func New(db *mongo.Database) *Service {
	return &Service{col: db.Collection("notifications")}
}

func (s *Service) Send(ctx context.Context, m *Message) {
	m.CreatedAt = time.Now()
	_, _ = s.col.InsertOne(ctx, m)
}

func (s *Service) UnreadCount(ctx context.Context, tenantID, userID bson.ObjectID) int64 {
	n, _ := s.col.CountDocuments(ctx, bson.M{
		"tenant_id": tenantID, "user_id": userID,
		"read_at": bson.M{"$exists": false},
	})
	return n
}

func (s *Service) List(ctx context.Context, tenantID, userID bson.ObjectID, limit int64) ([]Message, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit)
	cur, err := s.col.Find(ctx, bson.M{"tenant_id": tenantID, "user_id": userID}, opts)
	if err != nil {
		return nil, err
	}
	var out []Message
	return out, cur.All(ctx, &out)
}

func (s *Service) MarkRead(ctx context.Context, tenantID, id bson.ObjectID) {
	_, _ = s.col.UpdateOne(ctx,
		bson.M{"_id": id, "tenant_id": tenantID},
		bson.M{"$set": bson.M{"read_at": time.Now()}})
}
