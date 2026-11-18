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

// TestCreateRequiresPhone 新建患者名称+电话必填；电话重复拒绝。
func TestCreateRequiresPhone(t *testing.T) {
	svc, _, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	if err := svc.Create(ctx, tid, &Patient{Name: "无电话"}); err == nil {
		t.Fatal("empty phone should fail")
	}
	if err := svc.Create(ctx, tid, &Patient{Phone: "13900000021"}); err == nil {
		t.Fatal("empty name should fail")
	}
	if err := svc.Create(ctx, tid, &Patient{Name: "格式错", Phone: "123"}); err == nil {
		t.Fatal("bad phone should fail")
	}
	first := &Patient{Name: "张三", Phone: "13900000021"}
	if err := svc.Create(ctx, tid, first); err != nil {
		t.Fatal(err)
	}
	if err := svc.Create(ctx, tid, &Patient{Name: "张三二", Phone: "13900000021"}); err == nil {
		t.Fatal("duplicate phone should fail")
	}
}

func TestParseAgeAndGender(t *testing.T) {
	if age, err := ParseAge(""); err != nil || age != nil {
		t.Fatalf("empty age = %v, %v; want nil, nil", age, err)
	}
	if age, err := ParseAge("0"); err != nil || age == nil || *age != 0 {
		t.Fatalf("0 age = %v, %v; want 0", age, err)
	}
	if age, err := ParseAge("150"); err != nil || age == nil || *age != 150 {
		t.Fatalf("150 age = %v, %v", age, err)
	}
	for _, bad := range []string{"-1", "151", "5.5", "abc"} {
		if _, err := ParseAge(bad); err == nil {
			t.Fatalf("ParseAge(%q) should fail", bad)
		}
	}
	for _, ok := range []string{"", "男", "女", " 男 "} {
		if _, err := NormalizeGender(ok); err != nil {
			t.Fatalf("NormalizeGender(%q) = %v", ok, err)
		}
	}
	if _, err := NormalizeGender("其他"); err == nil {
		t.Fatal("unknown gender should fail")
	}
}

func TestCreateAndUpdateProfile(t *testing.T) {
	svc, _, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ec")
	age35 := 35
	p := &Patient{Name: "王五", Phone: "13900000031", Gender: "男", Age: &age35}
	if err := svc.Create(ctx, tid, p); err != nil {
		t.Fatal(err)
	}
	got, err := svc.ByID(ctx, tid, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gender != "男" || got.Age == nil || *got.Age != 35 || got.AgeText() != "35" {
		t.Fatalf("created profile = %+v, ageText=%q", got, got.AgeText())
	}

	// 改姓名/性别/年龄；电话不在 Update 参数里，必须保持原值。
	age0 := 0
	if err := svc.Update(ctx, tid, p.ID, "王五五", "女", &age0); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.ByID(ctx, tid, p.ID)
	if got.Name != "王五五" || got.Gender != "女" || got.Age == nil || *got.Age != 0 || got.AgeText() != "0" {
		t.Fatalf("updated profile = %+v, ageText=%q", got, got.AgeText())
	}
	if got.Phone != "13900000031" {
		t.Fatalf("phone changed to %q", got.Phone)
	}

	// 性别/年龄必填：清空应失败，保留原值。
	if err := svc.Update(ctx, tid, p.ID, "王五五", "", nil); err == nil {
		t.Fatal("empty gender/age should fail")
	}
	if err := svc.Update(ctx, tid, p.ID, "王五五", "女", nil); err == nil {
		t.Fatal("empty age should fail")
	}
	if err := svc.Update(ctx, tid, p.ID, "王五五", "", &age0); err == nil {
		t.Fatal("empty gender should fail")
	}
	got, _ = svc.ByID(ctx, tid, p.ID)
	if got.Gender != "女" || got.Age == nil || *got.Age != 0 || got.AgeText() != "0" {
		t.Fatalf("required update changed profile = %+v", got)
	}

	// 非法性别/年龄拒绝；不存在的患者拒绝。
	badAge := 151
	if err := svc.Update(ctx, tid, p.ID, "王五五", "其他", &age0); err == nil {
		t.Fatal("unknown gender should fail")
	}
	if err := svc.Update(ctx, tid, p.ID, "王五五", "男", &badAge); err == nil {
		t.Fatal("age 151 should fail")
	}
	if err := svc.Update(ctx, tid, bson.NewObjectID(), "无此人", "男", &age0); err == nil {
		t.Fatal("missing patient should fail")
	}
}

func TestCreateProfileValidation(t *testing.T) {
	svc, _, ctx := testSvc(t)
	tid, _ := bson.ObjectIDFromHex("6ac4bd23b33e9a18faace5ed")
	badAge := 151
	if err := svc.Create(ctx, tid, &Patient{Name: "性别错", Phone: "13900000032", Gender: "其他"}); err == nil {
		t.Fatal("unknown gender should fail")
	}
	if err := svc.Create(ctx, tid, &Patient{Name: "年龄错", Phone: "13900000033", Age: &badAge}); err == nil {
		t.Fatal("age 151 should fail")
	}
}
