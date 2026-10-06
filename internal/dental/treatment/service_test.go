package treatment

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
	db := cli.Database("erp_unittest_treat")
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

// TestEnsureMigration 老数据拆诊疗单：跑两遍只有一张，费用单反链补上。
func TestEnsureMigration(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	aid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ef")
	if _, err := db.Collection("appointments").InsertOne(ctx, bson.M{
		"_id": aid, "tenant_id": tid, "patient_id": pid, "patient_name": "迁移测试",
		"date": "2026-10-01", "slot": "10:00", "item": "测试项目",
		"diagnosis": "测试诊断", "charge": 300, "charge_no": "CH-T-0099",
		"status": "done", "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("bills").InsertOne(ctx, bson.M{
		"tenant_id": tid, "patient_id": pid, "patient_name": "迁移测试",
		"doc_no": "CH-T-0099", "amount": 300, "paid_amount": 300,
		"status": "paid", "ref_id": aid.Hex(), "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := svc.EnsureMigration(ctx); err != nil {
			t.Fatal(err)
		}
	}
	n, _ := db.Collection("treatments").CountDocuments(ctx, bson.M{"tenant_id": tid})
	if n != 1 {
		t.Fatalf("treatments = %d, want 1", n)
	}
	var tr Treatment
	if err := db.Collection("treatments").FindOne(ctx, bson.M{"tenant_id": tid}).Decode(&tr); err != nil {
		t.Fatal(err)
	}
	if tr.Status != Paid || tr.Total != 300 || tr.Diagnosis != "测试诊断" || tr.BillNo != "CH-T-0099" {
		t.Fatalf("treatment = %+v", tr)
	}
	var b struct {
		TreatmentID bson.ObjectID `bson:"treatment_id"`
	}
	if err := db.Collection("bills").FindOne(ctx, bson.M{"doc_no": "CH-T-0099"}).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if b.TreatmentID != tr.ID {
		t.Fatalf("bill.treatment_id = %s, want %s", b.TreatmentID.Hex(), tr.ID.Hex())
	}
}

// TestInsertDuplicate 同预约建两张直接拒绝。
func TestInsertDuplicate(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	aid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ef")
	mk := func() error {
		return svc.Insert(ctx, tid, &Treatment{ApptID: aid, Total: 100, Status: Billed})
	}
	_ = db
	if err := mk(); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := mk(); err != ErrExists {
		t.Fatalf("second insert err = %v, want ErrExists", err)
	}
}
