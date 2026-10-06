package appointment

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"erp/internal/billing"
	"erp/internal/dental/catalog"
	"erp/internal/dental/patient"
	"erp/internal/platform/auth"
	"erp/internal/platform/seqno"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestSlotFormat(t *testing.T) {
	for _, ok := range []string{"09:00", "00:00", "23:59", "9:00"} {
		// 注意 "9:00" 非 HH:MM 零填充，应拒绝
		want := ok != "9:00"
		if slotRe.MatchString(ok) != want {
			t.Fatalf("slotRe(%q) = %v, want %v", ok, !want, want)
		}
	}
	for _, bad := range []string{"", "24:00", "09:60", "9:0", "0900", "09:00:00"} {
		if slotRe.MatchString(bad) {
			t.Fatalf("slotRe(%q) = true, want false", bad)
		}
	}
}

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
	db := cli.Database("erp_unittest_appt")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = cli.Disconnect(ctx)
	})
	seq := seqno.New(db)
	bsvc := billing.New(db, seq)
	if err := bsvc.EnsureIndexes(ctx); err != nil {
		t.Fatalf("ensure indexes: %v", err)
	}
	svc := New(db, seq, bsvc, patient.New(db), catalog.New(db), auth.NewService(db))
	if err := svc.EnsureIndexes(ctx); err != nil {
		t.Fatalf("ensure appt indexes: %v", err)
	}
	return svc, db, ctx
}

// TestCompleteConcurrent 并发开单：只能成功一次，不建重单。
func TestCompleteConcurrent(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	res, err := db.Collection("appointments").InsertOne(ctx, bson.M{
		"tenant_id": tid, "patient_id": pid, "patient_name": "并发测试",
		"date": "2026-10-06", "slot": "10:00", "item": "测试项目",
		"status": Serving, "created_at": time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	apptID := res.InsertedID.(bson.ObjectID)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.Complete(ctx, tid, apptID, nil, 500, false, "", "", "test")
		}(i)
	}
	wg.Wait()
	ok, fail := 0, 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if strings.Contains(err.Error(), ErrBadStatus.Error()) {
			fail++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || fail != 1 {
		t.Fatalf("ok=%d fail=%d, want 1/1", ok, fail)
	}
	n, err := db.Collection("bills").CountDocuments(ctx, bson.M{"tenant_id": tid, "ref_id": apptID.Hex()})
	if err != nil || n != 1 {
		t.Fatalf("bills for appt = %d, want 1", n)
	}
}
