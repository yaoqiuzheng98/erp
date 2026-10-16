package session

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func testManager(t *testing.T) (*Manager, context.Context) {
	t.Helper()
	uri := os.Getenv("MONGO_TEST_URL")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27018"
	}
	ctx := context.Background()
	cli, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Skipf("mongo unavailable: %v", err)
	}
	ping, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := cli.Ping(ping, nil); err != nil {
		t.Skipf("mongo ping failed: %v", err)
	}
	db := cli.Database("erp_unittest_session")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = cli.Disconnect(ctx)
	})
	return NewManager(db, 12), ctx
}

// TestPermanentSession 永久会话：有效期约 100 年；解绑清会话后旧 token 即刻失效。
func TestPermanentSession(t *testing.T) {
	m, ctx := testManager(t)
	tid := bson.NewObjectID()
	uid := bson.NewObjectID()
	s, err := m.CreatePermanent(ctx, KindPatient, uid, tid)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(s.ExpiresAt) < 90*365*24*time.Hour {
		t.Fatalf("expires too soon: %v", s.ExpiresAt)
	}
	if _, err := m.Get(ctx, s.ID, KindPatient); err != nil {
		t.Fatalf("permanent session should be retrievable: %v", err)
	}
	// 普通会话照旧有 12h 过期（对照组）。
	s2, err := m.Create(ctx, KindPatient, uid, tid)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(s2.ExpiresAt) > 13*time.Hour {
		t.Fatalf("normal session should expire in ~12h: %v", s2.ExpiresAt)
	}
	// 解绑清会话：两个都失效。
	if err := m.DeleteByUser(ctx, tid, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, s.ID, KindPatient); err == nil {
		t.Fatal("session should be gone after DeleteByUser")
	}
	if _, err := m.Get(ctx, s2.ID, KindPatient); err == nil {
		t.Fatal("normal session should be gone after DeleteByUser")
	}
}
