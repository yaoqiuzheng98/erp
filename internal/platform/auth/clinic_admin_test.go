package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func clinicAdminSvc(t *testing.T) (*Service, *mongo.Database, context.Context) {
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
	db := cli.Database("erp_unittest_clinic_admin")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
		_ = cli.Disconnect(ctx)
	})
	svc := NewService(db)
	return svc, db, ctx
}

// TestEnsureClinicSuperAdmin 门诊通用超管：建号、隐藏、可登录、密码同步。
func TestEnsureClinicSuperAdmin(t *testing.T) {
	svc, db, ctx := clinicAdminSvc(t)
	tid := bson.NewObjectID()
	if _, err := db.Collection("tenants").InsertOne(ctx, bson.M{
		"_id": tid, "name": "测试门诊", "status": "active", "created_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	const phone = "15889862321"
	if err := svc.EnsureClinicSuperAdmin(ctx, tid, phone, "123456"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	var u User
	if err := db.Collection("users").FindOne(ctx, bson.M{"tenant_id": tid, "phone": phone}).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if !u.IsClinicSuperAdmin || !u.IsTenantAdm || u.Status != "active" || !u.HideHome || u.Treat {
		t.Fatalf("bad flags: %+v", u)
	}
	if !CheckPassword(u.PasswordHash, "123456") {
		t.Fatal("password not set")
	}
	// 员工列表隐藏
	list, err := svc.List(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("superadmin should be hidden, got %d", len(list))
	}
	// 可登录
	got, err := svc.LoginTenant(ctx, "测试门诊", phone, "123456")
	if err != nil || got.ID != u.ID {
		t.Fatalf("login = %+v, %v", got, err)
	}
	// 配置改密码后幂等覆盖：旧密码失效，新密码可登录
	if err := svc.EnsureClinicSuperAdmin(ctx, tid, phone, "newpass123"); err != nil {
		t.Fatalf("ensure new pass: %v", err)
	}
	if _, err := svc.LoginTenant(ctx, "测试门诊", phone, "123456"); err == nil {
		t.Fatal("old password should fail after sync")
	}
	if _, err := svc.LoginTenant(ctx, "测试门诊", phone, "newpass123"); err != nil {
		t.Fatalf("new password login: %v", err)
	}
	if n, _ := db.Collection("users").CountDocuments(ctx, bson.M{"tenant_id": tid, "phone": phone}); n != 1 {
		t.Fatalf("superadmin count = %d, want 1", n)
	}
}
