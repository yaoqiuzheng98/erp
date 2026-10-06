package billing

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"erp/internal/platform/seqno"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestDateFilter(t *testing.T) {
	if len(dateFilter("created_at", "", "")) != 0 {
		t.Fatal("empty range should give empty filter")
	}
	f := dateFilter("created_at", "2026-10-05", "2026-10-06")
	m, ok := f["created_at"].(bson.M)
	if !ok {
		t.Fatalf("filter = %v", f)
	}
	from := m["$gte"].(time.Time)
	to := m["$lt"].(time.Time)
	if to.Sub(from) != 48*time.Hour {
		t.Fatalf("range = %v, want 48h", to.Sub(from))
	}
	if _, offset := from.Zone(); offset != 8*3600 {
		t.Fatalf("filter zone offset = %d, want +8h", offset)
	}
	// 非法日期被忽略，不拼条件
	if len(dateFilter("created_at", "xxx", "")) != 0 {
		t.Fatal("invalid from should be ignored")
	}
}

// testSvc 连本地 mongo（MONGO_TEST_URL，默认 127.0.0.1:27018），连不上则跳过。
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
	db := cli.Database("erp_unittest_billing")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = cli.Disconnect(ctx)
	})
	if err := New(db, seqno.New(db)).EnsureIndexes(ctx); err != nil {
		t.Fatalf("ensure indexes: %v", err)
	}
	return New(db, seqno.New(db)), db, ctx
}

func mustOID(t *testing.T, hex string) bson.ObjectID {
	t.Helper()
	id, err := bson.ObjectIDFromHex(hex)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestPayConcurrent 并发全额核销：只能成功一次，收款单不多记。
func TestPayConcurrent(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid := mustOID(t, "6ac4bd23b33e9a18faace5ec")
	if err := svc.CreateAR(ctx, tid, AR{
		PatientID:   mustOID(t, "6ac4bd23b33e9a18faace5ed"),
		PatientName: "并发测试", DocNo: "CH-T-0001", Amount: 100,
	}); err != nil {
		t.Fatal(err)
	}
	b, err := svc.ByDocNo(ctx, tid, "CH-T-0001")
	if err != nil || b == nil {
		t.Fatalf("bill missing: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = svc.Pay(ctx, tid, b.ID, 100, "cash", "test")
		}(i)
	}
	wg.Wait()
	ok, fail := 0, 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if strings.Contains(err.Error(), "单据状态已变化") ||
			strings.Contains(err.Error(), "单据已结清或已作废") {
			// 输家看到哪种错误取决于交错时序：更新前读到 open 则报"状态已变化"，
			// 更新后读到 paid 则报"已结清"，两种都是正确的互斥结果
			fail++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || fail != 1 {
		t.Fatalf("ok=%d fail=%d, want 1/1", ok, fail)
	}
	n, err := db.Collection("payments").CountDocuments(ctx, bson.M{"tenant_id": tid})
	if err != nil || n != 1 {
		t.Fatalf("payments = %d, want 1", n)
	}
}

// TestVoidTwice 作废幂等语义：第二次明确报错，不静默。
func TestVoidTwice(t *testing.T) {
	svc, _, ctx := testSvc(t)
	tid := mustOID(t, "6ac4bd23b33e9a18faace5ec")
	if err := svc.CreateAR(ctx, tid, AR{
		PatientID:   mustOID(t, "6ac4bd23b33e9a18faace5ed"),
		PatientName: "作废测试", DocNo: "CH-T-0002", Amount: 50,
	}); err != nil {
		t.Fatal(err)
	}
	b, _ := svc.ByDocNo(ctx, tid, "CH-T-0002")
	if err := svc.VoidBill(ctx, tid, b.ID); err != nil {
		t.Fatalf("first void: %v", err)
	}
	if err := svc.VoidBill(ctx, tid, b.ID); err == nil {
		t.Fatal("second void should fail")
	}
}
