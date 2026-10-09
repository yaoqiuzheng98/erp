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
	"erp/internal/dental/treatment"
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
		t.Fatalf("ensure billing indexes: %v", err)
	}
	if err := bsvc.EnsureIndexes(ctx); err != nil {
		t.Fatalf("ensure indexes: %v", err)
	}
	svc := New(db, seq, bsvc, patient.New(db), catalog.New(db), auth.NewService(db), treatment.New(db))
	if err := svc.EnsureIndexes(ctx); err != nil {
		t.Fatalf("ensure appt indexes: %v", err)
	}
	return svc, db, ctx
}

// TestCreateConcurrentDoubleBook 并发约同时段：只能成功一个，不重约。
func TestCreateConcurrentDoubleBook(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	did, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ef")
	if _, err := db.Collection("patients").InsertOne(ctx, bson.M{
		"_id": pid, "tenant_id": tid, "name": "重约测试", "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("users").InsertOne(ctx, bson.M{
		"_id": did, "tenant_id": tid, "name": "重约医生",
		"status": "active", "can_practice": true, "created_at": time.Now(),
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
				PatientID: pid, DoctorID: did,
				Date: "2026-10-06", Slot: "10:00", Item: "测试",
			}, 0, 0)
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

// TestPayRegIdempotent 挂号费重复调：只建一张 RG 单，直接返回成功。
func TestPayRegIdempotent(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	res, err := db.Collection("appointments").InsertOne(ctx, bson.M{
		"tenant_id": tid, "patient_id": pid, "patient_name": "挂号测试",
		"date": "2026-10-06", "slot": "11:00", "item": "测试",
		"status": Booked, "reg_fee": 10, "created_at": time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	apptID := res.InsertedID.(bson.ObjectID)
	if err := svc.PayReg(ctx, tid, apptID, "cash", "test"); err != nil {
		t.Fatalf("first PayReg: %v", err)
	}
	if err := svc.PayReg(ctx, tid, apptID, "cash", "test"); err != nil {
		t.Fatalf("second PayReg should be no-op: %v", err)
	}
	n, err := db.Collection("bills").CountDocuments(ctx, bson.M{"tenant_id": tid, "ref_id": apptID.Hex()})
	if err != nil || n != 1 {
		t.Fatalf("RG bills = %d, want 1", n)
	}
}

// TestCancelUnpaid 取待缴费单：未收单作废 + 预约取消；已结清单不动。
func TestCancelUnpaid(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	mk := func(status string) bson.ObjectID {
		res, err := db.Collection("appointments").InsertOne(ctx, bson.M{
			"tenant_id": tid, "patient_id": pid, "patient_name": "取消测试",
			"date": "2026-10-06", "slot": "12:00", "item": "测试",
			"status": status, "created_at": time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return res.InsertedID.(bson.ObjectID)
	}
	mkBill := func(ref, docno, status string, paid float64) {
		if _, err := db.Collection("bills").InsertOne(ctx, bson.M{
			"tenant_id": tid, "patient_id": pid, "patient_name": "取消测试",
			"doc_no": docno, "amount": 100, "paid_amount": paid,
			"status": status, "ref_id": ref, "created_at": time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// unpaid + open → 取消 + 作废
	a1 := mk(Unpaid)
	mkBill(a1.Hex(), "CH-T-0010", "open", 0)
	voided, err := svc.CancelUnpaid(ctx, tid, a1)
	if err != nil {
		t.Fatalf("CancelUnpaid: %v", err)
	}
	if len(voided) != 1 || voided[0] != "CH-T-0010" {
		t.Fatalf("voided = %v", voided)
	}
	var got struct {
		Status string `bson:"status"`
	}
	if err := db.Collection("appointments").FindOne(ctx, bson.M{"_id": a1}).Decode(&got); err != nil || got.Status != Cancel {
		t.Fatalf("appt status = %+v, %v", got, err)
	}
	var b struct {
		Status string `bson:"status"`
	}
	if err := db.Collection("bills").FindOne(ctx, bson.M{"doc_no": "CH-T-0010"}).Decode(&b); err != nil || b.Status != "void" {
		t.Fatalf("bill status = %+v, %v", b, err)
	}

	// unpaid + paid → 拒绝（钱动了必须人工处理）
	a2 := mk(Unpaid)
	mkBill(a2.Hex(), "CH-T-0011", "paid", 100)
	if _, err := svc.CancelUnpaid(ctx, tid, a2); err == nil {
		t.Fatal("CancelUnpaid with paid bill should fail")
	}

	// done + open（新模型：开单即 done，钱还没收）→ 作废 + 取消
	a5 := mk(Done)
	mkBill(a5.Hex(), "CH-T-0012", "open", 0)
	if _, err := db.Collection("treatments").InsertOne(ctx, bson.M{
		"tenant_id": tid, "appt_id": a5, "total": 100,
		"status": "billed", "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	var trPick struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := db.Collection("treatments").FindOne(ctx, bson.M{"appt_id": a5}).Decode(&trPick); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("bills").UpdateOne(ctx, bson.M{"doc_no": "CH-T-0012"},
		bson.M{"$set": bson.M{"treatment_id": trPick.ID}}); err != nil {
		t.Fatal(err)
	}
	voided, err = svc.CancelUnpaid(ctx, tid, a5)
	if err != nil {
		t.Fatalf("CancelUnpaid done+open: %v", err)
	}
	if len(voided) != 1 {
		t.Fatalf("voided = %v", voided)
	}
	if err := db.Collection("appointments").FindOne(ctx, bson.M{"_id": a5}).Decode(&got); err != nil || got.Status != Cancel {
		t.Fatalf("appt status = %+v, %v", got, err)
	}
	var tr struct {
		Status string `bson:"status"`
	}
	if err := db.Collection("treatments").FindOne(ctx, bson.M{"appt_id": a5}).Decode(&tr); err != nil || tr.Status != "void" {
		t.Fatalf("treatment status = %+v, %v", tr, err)
	}

	// done + paid → 拒绝
	a6 := mk(Done)
	mkBill(a6.Hex(), "CH-T-0013", "paid", 100)
	if _, err := svc.CancelUnpaid(ctx, tid, a6); err == nil {
		t.Fatal("CancelUnpaid done+paid should fail")
	}

	// booked 走普通取消
	a3 := mk(Booked)
	if _, err := svc.CancelUnpaid(ctx, tid, a3); err != nil {
		t.Fatalf("CancelUnpaid booked: %v", err)
	}

	// serving 拒绝
	a4 := mk(Serving)
	if _, err := svc.CancelUnpaid(ctx, tid, a4); err == nil {
		t.Fatal("CancelUnpaid serving should fail")
	}
}

// TestListRange 周视图范围查：只回区间内，按日期+时段排序，医生/电话筛选生效。
func TestListRange(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ee")
	did, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ef")
	if _, err := db.Collection("patients").InsertOne(ctx, bson.M{
		"_id": pid, "tenant_id": tid, "name": "范围测试", "phone": "13900019999", "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("users").InsertOne(ctx, bson.M{
		"_id": did, "tenant_id": tid, "name": "范围医生",
		"status": "active", "can_practice": true, "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	mk := func(date, slot string) {
		if _, err := db.Collection("appointments").InsertOne(ctx, bson.M{
			"tenant_id": tid, "patient_id": pid, "patient_name": "范围测试",
			"doctor_id": did, "doctor": "范围医生",
			"date": date, "slot": slot, "item": "测试",
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

	got, err := svc.ListRange(ctx, tid, "2026-10-05", "2026-10-11", bson.NilObjectID, "", 0)
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
	if res, _ := svc.ListRange(ctx, tid, "2026-10-05", "2026-10-11", bson.NilObjectID, "19999", 0); len(res) != 4 {
		t.Fatalf("phone hit len = %d, want 4", len(res))
	}
	if res, _ := svc.ListRange(ctx, tid, "2026-10-05", "2026-10-11", bson.NilObjectID, "00000", 0); len(res) != 0 {
		t.Fatalf("phone miss len = %d, want 0", len(res))
	}
	// 医生筛选命中 / 不命中
	if res, _ := svc.ListRange(ctx, tid, "2026-10-05", "2026-10-11", did, "", 0); len(res) != 4 {
		t.Fatalf("doctor hit len = %d, want 4", len(res))
	}
	otherDoc, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5f0")
	if res, _ := svc.ListRange(ctx, tid, "2026-10-05", "2026-10-11", otherDoc, "", 0); len(res) != 0 {
		t.Fatalf("doctor miss len = %d, want 0", len(res))
	}
}

// TestSlotConfig 放号档位：对齐校验 + 每档人数 + 非法配置回落默认。
func TestSlotConfig(t *testing.T) {
	svc, db, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	pid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ee")
	did, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ef")
	if _, err := db.Collection("patients").InsertOne(ctx, bson.M{
		"_id": pid, "tenant_id": tid, "name": "档位测试", "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("users").InsertOne(ctx, bson.M{
		"_id": did, "tenant_id": tid, "name": "档位医生",
		"status": "active", "can_practice": true, "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	mk := func(date, slot string, minutes, capacity int) error {
		return svc.Create(ctx, tid, &Appointment{
			PatientID: pid, DoctorID: did,
			Date: date, Slot: slot, Item: "测试",
		}, minutes, capacity)
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

func TestCompleteConcurrent(t *testing.T) {
	// 并发开单幂等：两边都成功，但只建一张诊疗单/费用单。
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
	if ok != 2 || fail != 0 {
		t.Fatalf("ok=%d fail=%d, want 2/0", ok, fail)
	}
	n, err := db.Collection("bills").CountDocuments(ctx, bson.M{"tenant_id": tid, "ref_id": apptID.Hex()})
	if err != nil || n != 1 {
		t.Fatalf("bills for appt = %d, want 1", n)
	}
	tn, err := db.Collection("treatments").CountDocuments(ctx, bson.M{"tenant_id": tid, "appt_id": apptID})
	if err != nil || tn != 1 {
		t.Fatalf("treatments for appt = %d, want 1", tn)
	}
}
