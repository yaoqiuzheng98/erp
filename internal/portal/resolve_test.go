package portal

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"erp/internal/dental/patient"
	"erp/internal/platform/config"
	"erp/internal/platform/env"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TestResolveByCode 按请求 code 查档：已建档→患者；
// 未建档→unbound（供 4401）；换码失败→err（不断言未建档）。
func TestResolveByCode(t *testing.T) {
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
	db := cli.Database("erp_unittest_portal")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = cli.Disconnect(ctx)
	})
	pats := patient.New(db)
	tid := bson.NewObjectID()
	p := &patient.Patient{Name: "码测试", Phone: "13900000001"}
	if err := pats.Create(ctx, tid, p); err != nil {
		t.Fatal(err)
	}
	if err := pats.BindOpenID(ctx, tid, p.ID, "oid-bound"); err != nil {
		t.Fatal(err)
	}

	old := exchangeCode
	defer func() { exchangeCode = old }()
	exchangeCode = func(appid, secret, code string) (string, error) {
		switch code {
		case "code-bound":
			return "oid-bound", nil
		case "code-fresh":
			return "oid-fresh", nil
		default:
			return "", errors.New("微信登录失败")
		}
	}
	g := &Guard{e: &env.Env{Cfg: &config.Config{}}, pats: pats}

	got, unbound, err := g.resolveByCode(ctx, tid, "code-bound")
	if err != nil || unbound || got == nil || got.ID != p.ID {
		t.Fatalf("bound = %+v %v %v", got, unbound, err)
	}
	if _, unbound, err := g.resolveByCode(ctx, tid, "code-fresh"); err != nil || !unbound {
		t.Fatalf("fresh should be unbound, got %v %v", unbound, err)
	}
	if _, unbound, err := g.resolveByCode(ctx, tid, "code-bad"); err == nil || unbound {
		t.Fatalf("bad code should err without unbound, got %v %v", unbound, err)
	}
}
