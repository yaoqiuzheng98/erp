package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"erp/internal/platform/attach"
	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/config"
	"erp/internal/platform/db"
	"erp/internal/platform/dict"
	"erp/internal/platform/env"
	"erp/internal/platform/event"
	"erp/internal/platform/httpserver"
	"erp/internal/platform/notify"
	"erp/internal/platform/org"
	"erp/internal/platform/plugin"
	"erp/internal/platform/rbac"
	"erp/internal/platform/seqno"
	"erp/internal/platform/session"
	"erp/internal/platform/task"
	"erp/internal/platform/tenant"
	"erp/internal/platform/web"

	// 插件编译期注册（blank import 触发 init）
	_ "erp/internal/plugins/basedata"
	_ "erp/internal/plugins/finance"
	_ "erp/internal/plugins/flow"
	_ "erp/internal/plugins/hr"
	_ "erp/internal/plugins/inventory"
	_ "erp/internal/plugins/purchase"
	_ "erp/internal/plugins/sales"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
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

	scheduler, err := task.New(d.Database)
	if err != nil {
		slog.Error("init scheduler", "err", err)
		os.Exit(1)
	}

	e := &env.Env{
		Cfg:      cfg,
		DB:       d,
		Sessions: session.NewManager(d.Database, cfg.Session.TTLHours),
		Auth:     auth.NewService(d.Database),
		Tenants:  tenant.NewService(d.Database),
		RBAC:     rbac.NewService(d.Database),
		Org:      org.NewService(d.Database),
		Dict:     dict.NewService(d.Database),
		Seq:      seqno.New(d.Database),
		Audit:    audit.New(d.Database),
		Notify:   notify.New(d.Database),
		Attach:   attach.New(d.Database, cfg.Storage.UploadDir),
		Events:   event.NewBus(),
		Tasks:    scheduler,
	}
	e.Gate = plugin.NewManager(d.Database)

	// 按依赖拓扑序：先注册服务（Service Locator），再挂任务与事件订阅
	for _, p := range plugin.TopoSorted() {
		p.RegisterServices(e)
	}
	for _, p := range plugin.All() {
		scheduler.RegisterPlugin(p.ID(), p.Tasks(e))
		p.SubscribeEvents(e.Events, e)
	}
	scheduler.Start()
	defer scheduler.Shutdown()

	tpl, err := web.Build(e)
	if err != nil {
		slog.Error("build templates", "err", err)
		os.Exit(1)
	}

	if cfg.Seed.Enabled {
		if err := seed(ctx, e, cfg); err != nil {
			slog.Error("seed", "err", err)
		}
	}

	r := httpserver.Build(e, tpl)
	slog.Info("erp listening", "addr", cfg.Server.Addr, "plugins", len(plugin.All()))
	if err := r.Run(cfg.Server.Addr); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

// seed 初始化系统超管与演示租户（幂等：已存在则跳过）。
func seed(ctx context.Context, e *env.Env, cfg *config.Config) error {
	// 系统超管
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
	err := e.DB.C("tenants").FindOne(ctx, bson.M{"name": "演示企业"}).Decode(&existing)
	if err == nil {
		return nil // 演示租户已存在
	}
	if err != mongo.ErrNoDocuments {
		return err
	}
	t, err := e.Tenants.Create(ctx, "演示企业")
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
	// 演示租户默认启用 basedata 插件
	if err := e.Gate.Enable(ctx, e, t.ID, "basedata"); err != nil {
		return err
	}
	slog.Info("seeded demo tenant", "code", "demo", "admin", cfg.Seed.DemoAdminUser)
	return nil
}
