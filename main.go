package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"erp/internal/billing"
	"erp/internal/dental"
	"erp/internal/platform/attach"
	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/config"
	"erp/internal/platform/db"
	"erp/internal/platform/env"
	"erp/internal/platform/httpserver"
	"erp/internal/platform/notify"
	"erp/internal/platform/rbac"
	"erp/internal/platform/seqno"
	"erp/internal/platform/session"
	"erp/internal/platform/tenant"
	"erp/internal/platform/web"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	cfgPath := flag.String("config", "./config.toml", "config file path")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()
	d, err := db.Connect(ctx, cfg.Mongo.URI, cfg.Mongo.Database)
	if err != nil {
		slog.Error("connect mongo", "err", err)
		os.Exit(1)
	}
	defer d.Close(ctx)

	e := &env.Env{
		Cfg:      cfg,
		DB:       d,
		Sessions: session.NewManager(d.Database, cfg.Session.TTLHours),
		Auth:     auth.NewService(d.Database),
		Tenants:  tenant.NewService(d.Database),
		RBAC:     rbac.NewService(d.Database),
		Seq:      seqno.New(d.Database),
		Audit:    audit.New(d.Database),
		Notify:   notify.New(d.Database),
		Attach:   attach.New(d.Database, cfg.Storage.UploadDir),
	}
	// 业务服务直连装配
	billingSvc := billing.New(d.Database, e.Seq)
	dentalSvc := dental.New(d.Database, e.Seq, billingSvc)

	// 唯一索引（幂等）
	for _, ix := range []struct {
		col  string
		keys bson.D
	}{
		{"tenants", bson.D{{Key: "name", Value: 1}}},
		{"users", bson.D{{Key: "tenant_id", Value: 1}, {Key: "username", Value: 1}}},
	} {
		if _, err := d.Database.Collection(ix.col).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: ix.keys, Options: options.Index().SetUnique(true),
		}); err != nil {
			slog.Error("ensure index", "col", ix.col, "err", err)
			os.Exit(1)
		}
	}
	if err := billingSvc.EnsureIndexes(ctx); err != nil {
		slog.Error("ensure billing indexes", "err", err)
		os.Exit(1)
	}

	tpl, err := web.Build()
	if err != nil {
		slog.Error("build templates", "err", err)
		os.Exit(1)
	}

	if cfg.Seed.Enabled {
		if err := seed(ctx, e, dentalSvc, cfg); err != nil {
			slog.Error("seed", "err", err)
		}
	}

	r := httpserver.Build(e, httpserver.Services{Dental: dentalSvc, Billing: billingSvc}, tpl)
	slog.Info("dental listening", "addr", cfg.Server.Addr)
	if err := r.Run(cfg.Server.Addr); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

// seed 初始化系统超管与演示门诊（幂等：已存在则跳过）。
func seed(ctx context.Context, e *env.Env, dentalSvc *dental.Service, cfg *config.Config) error {
	if n, _ := e.DB.C("sys_admins").EstimatedDocumentCount(ctx); n == 0 {
		hash, err := auth.HashPassword(cfg.Seed.SysadminPass)
		if err != nil {
			return err
		}
		_, err = e.DB.C("sys_admins").InsertOne(ctx, &auth.SysAdmin{
			Username: cfg.Seed.SysadminUser, PasswordHash: hash,
		})
		if err != nil {
			return err
		}
		slog.Info("seeded sysadmin", "user", cfg.Seed.SysadminUser)
	}

	if !cfg.Seed.DemoTenant {
		return nil
	}
	var existing bson.M
	err := e.DB.C("tenants").FindOne(ctx, bson.M{"name": "演示门诊"}).Decode(&existing)
	if err == nil {
		return nil
	}
	if err != mongo.ErrNoDocuments {
		return err
	}
	t, err := e.Tenants.Create(ctx, "演示门诊")
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(cfg.Seed.DemoAdminPass)
	if err != nil {
		return err
	}
	_, err = e.DB.C("users").InsertOne(ctx, &auth.User{
		TenantID: t.ID, Username: cfg.Seed.DemoAdminUser, Name: "管理员",
		PasswordHash: hash, Status: "active", IsTenantAdm: true,
	})
	if err != nil {
		return err
	}
	if err := dentalSvc.EnsureSeed(ctx, t.ID); err != nil {
		return err
	}
	slog.Info("seeded demo clinic", "admin", cfg.Seed.DemoAdminUser)
	return nil
}
