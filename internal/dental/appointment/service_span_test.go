package appointment

import (
	"context"
	"strings"
	"testing"
	"time"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// newSpanPatient 长单测试用患者（一人一单，每次换新患者）。
func newSpanPatient(t *testing.T, db *mongo.Database, ctx context.Context, tid bson.ObjectID, name string) bson.ObjectID {
	t.Helper()
	id := bson.NewObjectID()
	if _, err := db.Collection("patients").InsertOne(ctx, bson.M{
		"_id": id, "tenant_id": tid, "name": name, "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// createSpan 建一条预约；end 为空表示单档。
func createSpan(svc *Service, ctx context.Context, tid, pid, doc bson.ObjectID,
	date, slot, end string, allowSpan bool, biz []model.HourSeg) error {
	return svc.Create(ctx, tid, &Appointment{
		PatientID: pid, Date: date, Slot: slot, EndSlot: end, DoctorID: doc,
	}, BookOpts{AllowPast: true, Minutes: 30, AllowSpan: allowSpan, Biz: biz, Doctor: testDoctor})
}

// TestCreateSpanBlocksCoveredSlots 长单占满覆盖档，相邻档和其他医生不受影响。
func TestCreateSpanBlocksCoveredSlots(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	date := "2026-10-27"

	p1 := newSpanPatient(t, db, ctx, tid, "长单患者")
	if err := createSpan(svc, ctx, tid, p1, testDocID, date, "08:30", "10:00", true, nil); err != nil {
		t.Fatalf("create span: %v", err)
	}
	// 结束时间落库，方便周视图/余量直接按区间展示。
	list, err := svc.OfPatient(ctx, tid, p1)
	if err != nil || len(list) != 1 {
		t.Fatalf("span list = %v, %v", list, err)
	}
	if list[0].EndSlot != "10:00" {
		t.Fatalf("end_slot = %q, want 10:00", list[0].EndSlot)
	}

	// 覆盖档内同医生再约：拒。
	p2 := newSpanPatient(t, db, ctx, tid, "冲突患者")
	if err := createSpan(svc, ctx, tid, p2, testDocID, date, "09:00", "", false, nil); err == nil ||
		!strings.Contains(err.Error(), "已约满") {
		t.Fatalf("covered slot should be full, got %v", err)
	}
	// 相邻档（结束时刻）可约。
	p3 := newSpanPatient(t, db, ctx, tid, "相邻患者")
	if err := createSpan(svc, ctx, tid, p3, testDocID, date, "10:00", "", false, nil); err != nil {
		t.Fatalf("adjacent slot: %v", err)
	}
	// 长单之前的档也可约。
	p4 := newSpanPatient(t, db, ctx, tid, "前置患者")
	if err := createSpan(svc, ctx, tid, p4, testDocID, date, "08:00", "", false, nil); err != nil {
		t.Fatalf("slot before span: %v", err)
	}
	// 另一位医生同时段不受影响。
	p5 := newSpanPatient(t, db, ctx, tid, "别家患者")
	if err := createSpan(svc, ctx, tid, p5, testDoc2ID, date, "09:00", "", false, nil); err != nil {
		t.Fatalf("another doctor: %v", err)
	}
}

// TestCreateSpanValidation 结束时间/粒度/跨时段/入口限制。
func TestCreateSpanValidation(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	date := "2026-10-28"
	pid := newSpanPatient(t, db, ctx, tid, "校验患者")
	biz := []model.HourSeg{{Open: "09:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}}

	cases := []struct {
		name  string
		slot  string
		end   string
		allow bool
		biz   []model.HourSeg
		want  string
	}{
		{"结束不晚于开始", "09:00", "09:00", true, nil, "晚于开始时间"},
		{"非粒度整数倍", "09:00", "09:15", true, nil, "整数倍"},
		{"患者端入口不允许", "09:00", "10:00", false, nil, "不支持长时段"},
		{"跨午休", "11:30", "14:30", true, biz, "跨出当前放号时段"},
		{"起点在时段外", "08:30", "09:30", true, biz, "不在放号时间内"},
	}
	for _, tc := range cases {
		err := createSpan(svc, ctx, tid, pid, testDocID, date, tc.slot, tc.end, tc.allow, tc.biz)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want contains %q", tc.name, err, tc.want)
		}
	}
	// 合法整段：09:00-12:00（上午全部）。
	if err := createSpan(svc, ctx, tid, pid, testDocID, date, "09:00", "12:00", true, biz); err != nil {
		t.Fatalf("whole morning: %v", err)
	}
}

// TestBookedCountsExpandsSpan 余量接口把长单展开到每个覆盖档，小程序据此显示"已预约"。
func TestBookedCountsExpandsSpan(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	date := "2026-10-29"
	p1 := newSpanPatient(t, db, ctx, tid, "余量长单")
	if err := createSpan(svc, ctx, tid, p1, testDocID, date, "08:30", "10:00", true, nil); err != nil {
		t.Fatal(err)
	}
	// 老单没有 end_slot：按单档兜底，不能漏计。
	legacy := newSpanPatient(t, db, ctx, tid, "余量老单")
	if _, err := db.Collection("appointments").InsertOne(ctx, bson.M{
		"tenant_id": tid, "patient_id": legacy, "patient_name": "余量老单",
		"doctor_id": testDocID, "date": date, "slot": "11:00",
		"status": Booked, "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	slots := DaySlots(30)
	got, err := svc.BookedCounts(ctx, tid, date, date, testDocID, 30, slots)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"08:30", "09:00", "09:30", "11:00"} {
		if got[date][s] != 1 {
			t.Fatalf("count[%s] = %d, want 1", s, got[date][s])
		}
	}
	for _, s := range []string{"08:00", "10:00", "10:30"} {
		if got[date][s] != 0 {
			t.Fatalf("count[%s] = %d, want 0", s, got[date][s])
		}
	}
	// 不传档位表时退回精确匹配，老调用不炸。
	exact, err := svc.BookedCounts(ctx, tid, date, date, testDocID, 30, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exact[date]["08:30"] != 1 || exact[date]["09:00"] != 0 {
		t.Fatalf("exact fallback = %v", exact[date])
	}
}

// TestCancelSpanFreesSlots 取消长单后覆盖档全部释放（仍是一条单，一次取消）。
func TestCancelSpanFreesSlots(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	date := "2026-10-30"
	p1 := newSpanPatient(t, db, ctx, tid, "取消长单")
	if err := createSpan(svc, ctx, tid, p1, testDocID, date, "08:30", "10:00", true, nil); err != nil {
		t.Fatal(err)
	}
	list, err := svc.OfPatient(ctx, tid, p1)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if err := svc.Cancel(ctx, tid, list[0].ID); err != nil {
		t.Fatal(err)
	}
	p2 := newSpanPatient(t, db, ctx, tid, "释放后患者")
	if err := createSpan(svc, ctx, tid, p2, testDocID, date, "09:00", "", false, nil); err != nil {
		t.Fatalf("slot after cancel: %v", err)
	}
}

// TestSpanPatientStillOneActive 长单仍受一人一单约束，不能借长单绕过。
func TestSpanPatientStillOneActive(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	pid := newSpanPatient(t, db, ctx, tid, "一人一单长单")
	if err := createSpan(svc, ctx, tid, pid, testDocID, "2026-10-31", "08:30", "10:00", true, nil); err != nil {
		t.Fatal(err)
	}
	err := createSpan(svc, ctx, tid, pid, testDocID, "2026-11-01", "10:00", "", false, nil)
	if err == nil || !strings.Contains(err.Error(), "一笔已预约") {
		t.Fatalf("second active = %v, want one-active error", err)
	}
}

// TestCreateWritesEndSlot 单档新单也写 end_slot，后续逻辑统一按区间处理。
func TestCreateWritesEndSlot(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	pid := newSpanPatient(t, db, ctx, tid, "单档结束时间")
	if err := createSpan(svc, ctx, tid, pid, testDocID, "2026-11-02", "09:00", "", false, nil); err != nil {
		t.Fatal(err)
	}
	list, err := svc.OfPatient(ctx, tid, pid)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if list[0].EndSlot != "09:30" {
		t.Fatalf("end_slot = %q, want 09:30", list[0].EndSlot)
	}
	if list[0].IsSpan(30) {
		t.Fatal("single slot should not be span")
	}
}

// TestAppointmentEndFallback 老单无 end_slot 的区间兜底。
func TestAppointmentEndFallback(t *testing.T) {
	a := Appointment{Slot: "09:00"}
	if got := a.EndMinutes(30); got != 9*60+30 {
		t.Fatalf("legacy end = %d, want %d", got, 9*60+30)
	}
	b := Appointment{Slot: "09:00", EndSlot: "11:00"}
	if got := b.EndMinutes(30); got != 11*60 {
		t.Fatalf("explicit end = %d, want %d", got, 11*60)
	}
	if !b.IsSpan(30) || a.IsSpan(30) {
		t.Fatal("IsSpan mismatch")
	}
	// 老单/脏值补结束时间，周视图按单档展示。
	c := Appointment{Slot: "09:00", EndSlot: "08:00"}
	c.NormalizeEnd(30)
	if c.EndSlot != "09:30" {
		t.Fatalf("normalized end = %q, want 09:30", c.EndSlot)
	}
	if _, ok := slotAdd("23:30", 30); ok {
		t.Fatal("slotAdd should reject crossing midnight")
	}
}
