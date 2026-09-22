// Package inventory 库存管理插件：出入库单、盘点、库存余额、低库存预警。
package inventory

import (
	"context"
	"embed"
	"io/fs"
	"time"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	"erp/internal/platform/event"
	"erp/internal/platform/menu"
	"erp/internal/platform/plugin"
	"erp/internal/platform/rbac"
	"erp/internal/platform/task"
	"erp/internal/plugins/inventory/handler"
	"erp/internal/plugins/inventory/service"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

//go:embed templates
var tplFS embed.FS

type Plugin struct {
	plugin.Base
}

func init() { plugin.Register(&Plugin{}) }

func (Plugin) ID() string             { return "inventory" }
func (Plugin) Name() string           { return "库存管理" }
func (Plugin) Version() string        { return "0.1.0" }
func (Plugin) Templates() fs.FS       { return tplFS }
func (Plugin) Dependencies() []string { return []string{"basedata"} }

func (Plugin) RegisterRoutes(g *gin.RouterGroup, e *env.Env) {
	handler.New(e).Register(g)
}

func (Plugin) Menus() []menu.Item {
	return []menu.Item{{
		ID: "inventory", Title: "库存管理", Icon: "▤",
		Children: []menu.Item{
			{ID: "inventory.docs", Title: "出入库单", Path: "/app/inventory/docs", Perm: "inventory.doc.read"},
			{ID: "inventory.balances", Title: "库存余额", Path: "/app/inventory/balances", Perm: "inventory.balance.read"},
		},
	}}
}

func (Plugin) Permissions() []rbac.PermissionDef {
	return []rbac.PermissionDef{
		{Code: "inventory.doc.read", Desc: "库存单据查看"},
		{Code: "inventory.doc.write", Desc: "库存单据操作"},
		{Code: "inventory.balance.read", Desc: "库存余额查看"},
	}
}

func (Plugin) Tasks(e *env.Env) []task.TaskDef {
	svc := service.New(e)
	return []task.TaskDef{{
		Name:      "low-stock-check",
		Every:     time.Hour,
		Singleton: true,
		Run:       svc.CheckLowStock,
	}}
}

// SubscribeEvents 订阅订单审核事件，自动生成出入库单。
func (Plugin) SubscribeEvents(b *event.Bus, e *env.Env) {
	svc := service.New(e)
	b.Subscribe(contract.TopicSalesApproved, func(ctx context.Context, ev event.Event) error {
		if !e.Gate.IsEnabled(ctx, ev.TenantID, "inventory") {
			return nil
		}
		if o, ok := ev.Payload.(contract.OrderApproved); ok {
			return svc.AutoOutbound(ctx, ev.TenantID, o)
		}
		return nil
	})
	b.Subscribe(contract.TopicPurchaseApproved, func(ctx context.Context, ev event.Event) error {
		if !e.Gate.IsEnabled(ctx, ev.TenantID, "inventory") {
			return nil
		}
		if o, ok := ev.Payload.(contract.OrderApproved); ok {
			return svc.AutoInbound(ctx, ev.TenantID, o)
		}
		return nil
	})
}

func (Plugin) OnInstall(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error {
	return service.New(e).EnsureIndexes(ctx)
}
