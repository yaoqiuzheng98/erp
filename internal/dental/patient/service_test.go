package patient

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func testSvc(t *testing.T) (*Service, *mongo.Database, context.Context) {
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
	db := cli.Database("erp_unittest_patient")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = cli.Disconnect(ctx)
	})
	svc := New(db)
	if err := svc.EnsureIndexes(ctx); err != nil {
		t.Fatalf("ensure indexes: %v", err)
	}
	return svc, db, ctx
}

// TestOpenIDUnique 一个微信只能绑一个档：撞唯一索引；解绑后可被别的档绑。
func TestOpenIDUnique(t *testing.T) {
	svc, _, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	a := &Patient{Name: "甲", Phone: "13900000011"}
	b := &Patient{Name: "乙", Phone: "13900000012"}
	if err := svc.Create(ctx, tid, a); err != nil {
		t.Fatal(err)
	}
	if err := svc.Create(ctx, tid, b); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindOpenID(ctx, tid, a.ID, "oid-x"); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	// 同微信再绑别人：必须失败（唯一索引兜底）
	if err := svc.BindOpenID(ctx, tid, b.ID, "oid-x"); err == nil {
		t.Fatal("second bind of same openid should fail")
	}
	// 定位还是一号
	if got, err := svc.ByOpenID(ctx, tid, "oid-x"); err != nil || got.ID != a.ID {
		t.Fatalf("by openid = %+v %v", got, err)
	}
	// 解绑后：老档查不到，新档可绑
	if err := svc.ClearOpenID(ctx, tid, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ByOpenID(ctx, tid, "oid-x"); err == nil {
		t.Fatal("after clear should miss")
	}
	if err := svc.BindOpenID(ctx, tid, b.ID, "oid-x"); err != nil {
		t.Fatalf("rebind after clear: %v", err)
	}
	if got, err := svc.ByOpenID(ctx, tid, "oid-x"); err != nil || got.ID != b.ID {
		t.Fatalf("rebound = %+v %v", got, err)
	}
}
