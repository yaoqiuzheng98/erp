package tenant

import (
	"context"
	"os"
	"testing"
	"time"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TestSetBusinessHoursRoundTrip 时段落库往返：展示串同步派生，清空回落。
func TestSetBusinessHoursRoundTrip(t *testing.T) {
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
	db := cli.Database("erp_unittest_tenant")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = cli.Disconnect(ctx)
	})
	svc := NewService(db)
	id, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	if _, err := db.Collection("tenants").InsertOne(ctx, bson.M{
		"_id": id, "name": "时段门诊", "status": "active", "hours": "老文本",
	}); err != nil {
		t.Fatal(err)
	}
	segs := []model.HourSeg{{Open: "09:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}}
	if err := svc.SetBusinessHours(ctx, id, segs); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := svc.ByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.BusinessHours()) != 2 || got.HoursText() != "9:00-12:00 14:00-18:00" {
		t.Fatalf("roundtrip = %+v %q", got.BusinessHours(), got.HoursText())
	}
	if got.Hours != "9:00-12:00 14:00-18:00" {
		t.Fatalf("hours display not synced: %q", got.Hours)
	}
	// 非法不落库
	if err := svc.SetBusinessHours(ctx, id, []model.HourSeg{{Open: "12:00", Close: "09:00"}}); err == nil {
		t.Fatal("bad segs should fail")
	}
	// 留空不落库（早晚两段必填）
	if err := svc.SetBusinessHours(ctx, id, nil); err == nil {
		t.Fatal("empty segs should fail")
	}
	got, _ = svc.ByID(ctx, id)
	if len(got.BusinessHours()) != 2 {
		t.Fatalf("bad saves must not overwrite: %+v", got.BusinessHours())
	}
}

// TestCreateDefaultBiz 新建门诊默认营业时间（9:00-12:00 14:00-18:00）。
func TestCreateDefaultBiz(t *testing.T) {
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
	db := cli.Database("erp_unittest_tenant")
	svc := NewService(db)
	nt, err := svc.Create(ctx, "默认时段门诊")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = db.Collection("tenants").DeleteOne(ctx, bson.M{"_id": nt.ID})
		_ = cli.Disconnect(ctx)
	})
	if len(nt.BusinessHours()) != 2 || nt.HoursText() != "9:00-12:00 14:00-18:00" {
		t.Fatalf("default = %+v %q", nt.BusinessHours(), nt.HoursText())
	}
}

// TestSetSlotSegsRoundTrip 放号两段落库往返：写入新两段时顺手清掉老单段字段。
func TestSetSlotSegsRoundTrip(t *testing.T) {
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
	db := cli.Database("erp_unittest_tenant_slotsegs")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = cli.Disconnect(ctx)
	})
	svc := NewService(db)
	id, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	if _, err := db.Collection("tenants").InsertOne(ctx, bson.M{
		"_id": id, "name": "放号测试", "status": "active",
		"slot_from": "08:00", "slot_to": "20:00",
	}); err != nil {
		t.Fatal(err)
	}
	segs := []model.HourSeg{{Open: "09:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}}
	if err := svc.SetSlotSegs(ctx, id, segs); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := svc.ByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.SlotSegs) != 2 || got.SlotFrom != "" || got.SlotTo != "" {
		t.Fatalf("roundtrip = %+v %q %q", got.SlotSegs, got.SlotFrom, got.SlotTo)
	}
	if w := got.SlotWindow(); len(w) != 2 || w[0].Open != "09:00" {
		t.Fatalf("window = %+v", w)
	}
	// 非法两段不落库
	bad := []model.HourSeg{{Open: "09:00", Close: "12:00"}, {Open: "11:00", Close: "18:00"}}
	if err := svc.SetSlotSegs(ctx, id, bad); err == nil {
		t.Fatal("overlap should fail")
	}
	// 全空=清除，回落营业时段
	if err := svc.SetSlotSegs(ctx, id, []model.HourSeg{{}, {}}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = svc.ByID(ctx, id)
	if len(got.SlotSegs) != 0 {
		t.Fatalf("after clear = %+v", got.SlotSegs)
	}
	// 非法单段（只有起）不落库
	if err := svc.SetSlotSegs(ctx, id, []model.HourSeg{{Open: "09:00"}, {}}); err == nil {
		t.Fatal("half should fail")
	}
}
