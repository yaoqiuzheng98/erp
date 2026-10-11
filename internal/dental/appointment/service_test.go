package appointment

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"erp/internal/dental/patient"
	"erp/internal/platform/model"
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
	svc := New(db, seq, patient.New(db))
	return svc, db, ctx
}

// TestCreateConcurrentDoubleBook 并发约同时段：只能成功一个，不重约。
func TestCreateConcurrentDoubleBook(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	if _, err := db.Collection("patients").InsertOne(ctx, bson.M{
		"_id": pid, "tenant_id": tid, "name": "重约测试", "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = svc.Create(ctx, tid, &Appointment{
				PatientID: pid,
				Date: "2026-10-06", Slot: "10:00",
			}, 0, 0, nil)
		}(i)
	}
	wg.Wait()
	ok, fail := 0, 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if strings.Contains(err.Error(), "已约满") {
			fail++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || fail != 1 {
		t.Fatalf("ok=%d fail=%d, want 1/1", ok, fail)
	}
}

// TestListRange 周视图范围查：只回区间内，按日期+时段排序，电话筛选生效。
func TestListRange(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ee")
	if _, err := db.Collection("patients").InsertOne(ctx, bson.M{
		"_id": pid, "tenant_id": tid, "name": "范围测试", "phone": "13900019999", "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	mk := func(date, slot string) {
		if _, err := db.Collection("appointments").InsertOne(ctx, bson.M{
			"tenant_id": tid, "patient_id": pid, "patient_name": "范围测试",
			"date": date, "slot": slot,
			"status": Booked, "created_at": time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("2026-10-05", "10:00") // 周一
	mk("2026-10-07", "09:00") // 周三早
	mk("2026-10-07", "14:00") // 周三午
	mk("2026-10-11", "09:00") // 周日
	mk("2026-10-12", "09:00") // 下周一，区间外
	mk("2026-10-04", "09:00") // 上周日，区间外

	got, err := svc.ListRange(ctx, tid, "2026-10-05", "2026-10-11", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	// 日期+时段升序
	for i := 1; i < len(got); i++ {
		a, b := got[i-1].Date+got[i-1].Slot, got[i].Date+got[i].Slot
		if a > b {
			t.Fatalf("not sorted: %s after %s", b, a)
		}
	}
	// 电话筛选命中 / 不命中
	if res, _ := svc.ListRange(ctx, tid, "2026-10-05", "2026-10-11", "19999", 0); len(res) != 4 {
		t.Fatalf("phone hit len = %d, want 4", len(res))
	}
	if res, _ := svc.ListRange(ctx, tid, "2026-10-05", "2026-10-11", "00000", 0); len(res) != 0 {
		t.Fatalf("phone miss len = %d, want 0", len(res))
	}
}

// TestSlotConfig 放号档位：对齐校验 + 每档人数 + 非法配置回落默认。
func TestSlotConfig(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ee")
	if _, err := db.Collection("patients").InsertOne(ctx, bson.M{
		"_id": pid, "tenant_id": tid, "name": "档位测试", "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	mk := func(date, slot string, minutes, capacity int) error {
		return svc.Create(ctx, tid, &Appointment{
			PatientID: pid,
			Date: date, Slot: slot,
		}, minutes, capacity, nil)
	}
	// 默认 30 分钟档：09:10 不对齐拒绝，09:30 通过
	if err := mk("2026-10-06", "09:10", 0, 0); err == nil {
		t.Fatal("unaligned slot should fail")
	}
	if err := mk("2026-10-06", "09:30", 0, 0); err != nil {
		t.Fatalf("aligned slot: %v", err)
	}
	// 默认每档 1 人：同档第二人约满
	if err := mk("2026-10-06", "09:30", 0, 0); err == nil ||
		(err != nil && !strings.Contains(err.Error(), "已约满")) {
		t.Fatalf("second booking should be full, got %v", err)
	}
	// 每档 2 人：同档可约两人，第三人约满
	if err := mk("2026-10-07", "10:00", 30, 2); err != nil {
		t.Fatalf("capacity first: %v", err)
	}
	if err := mk("2026-10-07", "10:00", 30, 2); err != nil {
		t.Fatalf("capacity second: %v", err)
	}
	if err := mk("2026-10-07", "10:00", 30, 2); err == nil {
		t.Fatal("capacity third should be full")
	}
	// 60 分钟档：整点通过，半点拒绝
	if err := mk("2026-10-08", "10:00", 60, 1); err != nil {
		t.Fatalf("hourly aligned: %v", err)
	}
	if err := mk("2026-10-08", "10:30", 60, 1); err == nil {
		t.Fatal("hourly misaligned should fail")
	}
}

// TestCreateBizHours 配了分段只收段内档（防绕过直调午休时间）。
func TestCreateBizHours(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	if _, err := db.Collection("patients").InsertOne(ctx, bson.M{
		"_id": pid, "tenant_id": tid, "name": "时段测试", "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	biz := []model.HourSeg{{Open: "09:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}}
	mk := func(slot string) error {
		return svc.Create(ctx, tid, &Appointment{
			PatientID: pid,
			Date: "2026-10-09", Slot: slot,
		}, 30, 1, biz)
	}
	for _, s := range []string{"09:00", "11:30", "14:00", "17:30"} {
		if err := mk(s); err != nil {
			t.Fatalf("in-biz %s: %v", s, err)
		}
	}
	for _, s := range []string{"08:30", "12:00", "12:30", "13:30", "18:00"} {
		if err := mk(s); err == nil ||
			(err != nil && !strings.Contains(err.Error(), "不在营业时间内")) {
			t.Fatalf("out-biz %s should fail, got %v", s, err)
		}
	}
}
