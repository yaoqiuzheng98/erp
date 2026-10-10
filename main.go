package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"erp/internal/dental/appointment"
	"erp/internal/dental/handler"
	"erp/internal/dental/patient"
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
		Attach:   attach.New(d.Database, cfg.Storage.MaxImageMB),
	}
	// 业务服务直连装配（员工即 e.Auth 用户）
	patSvc := patient.New(d.Database)
	apptSvc := appointment.New(d.Database, e.Seq, patSvc, e.Auth)
	dentalHandler := handler.New(e, patSvc, apptSvc, e.Auth)

	// 唯一索引（幂等）
	usersCol := d.Database.Collection("users")
	_ = usersCol.Indexes().DropOne(ctx, "tenant_id_1_username_1") // 老用户名索引（改手机号登录）
	// 会话过期自动清（TTL 按 expires_at 秒级回收，约一分钟延迟）
	_, _ = d.Database.Collection("sessions").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	for _, ix := range []struct {
		col  string
		keys bson.D
	}{
		{"tenants", bson.D{{Key: "name", Value: 1}}},
		{"users", bson.D{{Key: "tenant_id", Value: 1}, {Key: "phone", Value: 1}}},
	} {
		if _, err := d.Database.Collection(ix.col).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: ix.keys, Options: options.Index().SetUnique(true),
		}); err != nil {
			slog.Error("ensure index", "col", ix.col, "err", err)
			os.Exit(1)
		}
	}
	if err := patSvc.EnsureIndexes(ctx); err != nil {
		slog.Error("ensure patient indexes", "err", err)
		os.Exit(1)
	}

	tpl, err := web.Build()
	if err != nil {
		slog.Error("build templates", "err", err)
		os.Exit(1)
	}

	if cfg.Seed.Enabled {
		if err := seed(ctx, e, cfg); err != nil {
			slog.Error("seed", "err", err)
		}
	}

	r := httpserver.Build(e, httpserver.Services{
		Dental: dentalHandler,
		Patients: patSvc, Appts: apptSvc,
	}, tpl)
	slog.Info("dental listening", "addr", cfg.Server.Addr)
	if err := r.Run(cfg.Server.Addr); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

// seed 初始化系统超管（幂等：已存在则跳过）。
func seed(ctx context.Context, e *env.Env, cfg *config.Config) error {
	var existing auth.SysAdmin
	if err := e.DB.C("sys_admins").FindOne(ctx,
		bson.M{"username": cfg.Seed.SysadminUser}).Decode(&existing); err == nil {
		return nil
	}
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
	return nil
}
