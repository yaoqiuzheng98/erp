package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const (
	KindTenant  = "tenant"
	KindSys     = "sys"
	KindPatient = "patient"
)

// Session 会话文档，kind 区分租户会话与系统后台会话。
type Session struct {
	ID        string        `bson:"_id"`
	Kind      string        `bson:"kind"`
	UserID    bson.ObjectID `bson:"user_id"`
	TenantID  bson.ObjectID `bson:"tenant_id,omitempty"`
	CSRF      string        `bson:"csrf"`
	ExpiresAt time.Time     `bson:"expires_at"`
}

type Manager struct {
	col *mongo.Collection
	ttl time.Duration
}

func NewManager(db *mongo.Database, ttlHours int) *Manager {
	return &Manager{col: db.Collection("sessions"), ttl: time.Duration(ttlHours) * time.Hour}
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) Create(ctx context.Context, kind string, userID, tenantID bson.ObjectID) (*Session, error) {
	s := &Session{
		ID:        newToken(),
		Kind:      kind,
		UserID:    userID,
		TenantID:  tenantID,
		CSRF:      newToken()[:32],
		ExpiresAt: time.Now().Add(m.ttl),
	}
	_, err := m.col.InsertOne(ctx, s)
	return s, err
}

// CreatePermanent 创建不过期会话（患者端用：一次绑定，长期有效）。
// 用 100 年后到期而不是去掉字段，避免动 TTL 索引与 Get 查询语义。
func (m *Manager) CreatePermanent(ctx context.Context, kind string, userID, tenantID bson.ObjectID) (*Session, error) {
	s := &Session{
		ID:        newToken(),
		Kind:      kind,
		UserID:    userID,
		TenantID:  tenantID,
		CSRF:      newToken()[:32],
		ExpiresAt: time.Now().AddDate(100, 0, 0),
	}
	_, err := m.col.InsertOne(ctx, s)
	return s, err
}

// DeleteByUser 删除该用户全部会话（解绑微信时调用，旧 token 即刻失效）。
func (m *Manager) DeleteByUser(ctx context.Context, tenantID, userID bson.ObjectID) error {
	_, err := m.col.DeleteMany(ctx, bson.M{"tenant_id": tenantID, "user_id": userID})
	return err
}

var ErrNotFound = errors.New("session not found")

func (m *Manager) Get(ctx context.Context, token, kind string) (*Session, error) {
	var s Session
	err := m.col.FindOne(ctx, bson.M{
		"_id":        token,
		"kind":       kind,
		"expires_at": bson.M{"$gt": time.Now()},
	}).Decode(&s)
	if err != nil {
		return nil, ErrNotFound
	}
	return &s, nil
}

func (m *Manager) Destroy(ctx context.Context, token string) {
	_, _ = m.col.DeleteOne(ctx, bson.M{"_id": token})
}
