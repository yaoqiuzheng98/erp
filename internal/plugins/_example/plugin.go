// Package _example 插件骨架（本目录以 _ 开头，不参与编译）。
// 复制为 internal/plugins/<id>/ 后改包名并在 main.go blank import 即可。
package _example

import (
	"context"
	"embed"
	"io/fs"
	"time"

	"erp/internal/platform/env"
	"erp/internal/platform/event"
	"erp/internal/platform/menu"
	"erp/internal/platform/plugin"
	"erp/internal/platform/rbac"
	"erp/internal/platform/task"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

//go:embed templates
var tplFS embed.FS

// Plugin 内嵌 plugin.Base（Null Object），只覆盖需要的方法。
type Plugin struct {
	plugin.Base
}

// init 编译期注册；main.go 中 blank import 触发。
func init() { plugin.Register(&Plugin{}) }

func (Plugin) ID() string       { return "example" } // 决定路由 /app/example 与集合前缀
func (Plugin) Name() string     { return "示例插件" }
func (Plugin) Version() string  { return "0.1.0" }
func (Plugin) Templates() fs.FS { return tplFS }

// Dependencies 声明硬依赖；未启用依赖时本插件不可启用。
func (Plugin) Dependencies() []string { return []string{"basedata"} }

// RegisterRoutes 在 /app/example 分组注册页面路由。
func (Plugin) RegisterRoutes(g *gin.RouterGroup, e *env.Env) {
	g.GET("", func(c *gin.Context) {
		c.HTML(200, "example/index", gin.H{})
	})
}

// Menus 贡献侧边栏菜单；Perm 为空则无权限要求。
func (Plugin) Menus() []menu.Item {
	return []menu.Item{{
		ID: "example", Title: "示例", Icon: "☆",
		Children: []menu.Item{
			{ID: "example.list", Title: "示例页", Path: "/app/example", Perm: "example.read"},
		},
	}}
}

// Permissions 声明权限码，租户在角色管理中勾选分配。
func (Plugin) Permissions() []rbac.PermissionDef {
	return []rbac.PermissionDef{{Code: "example.read", Desc: "示例查看"}}
}

// Tasks 声明定时任务（gocron）；Singleton 防止重叠执行。
func (Plugin) Tasks(e *env.Env) []task.TaskDef {
	return []task.TaskDef{{
		Name: "example.daily", Cron: "0 2 * * *", Singleton: true,
		Run: func(ctx context.Context) error { return nil },
	}}
}

// SubscribeEvents 订阅平台事件；handler 内自行判断租户是否已启用本插件。
func (Plugin) SubscribeEvents(b *event.Bus, e *env.Env) {
	b.Subscribe("doc.submit", func(ctx context.Context, ev event.Event) error {
		if !e.Gate.IsEnabled(ctx, ev.TenantID, "example") {
			return nil
		}
		return nil
	})
}

// RegisterServices 把对外服务放进 Env，供其他插件经 contract 接口取用。
func (Plugin) RegisterServices(e *env.Env) { e.Provide("example.api", struct{}{}) }

// OnInstall 首次启用时执行：建索引等一次性工作。
func (Plugin) OnInstall(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error {
	_, err := e.DB.C("plg_example_item").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "code", Value: 1}},
	})
	return err
}

// OnEnable / OnDisable 可选；数据在禁用时保留。
func (Plugin) OnEnable(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error {
	_ = time.Now()
	return nil
}
