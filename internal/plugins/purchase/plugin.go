// Package purchase 采购管理插件：请购→订单→入库→应付。
package purchase

import (
	"context"
	"embed"
	"io/fs"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	"erp/internal/platform/event"
	"erp/internal/platform/menu"
	"erp/internal/platform/plugin"
	"erp/internal/platform/rbac"
	"erp/internal/plugins/purchase/handler"
	"erp/internal/plugins/purchase/service"

	"github.com/gin-gonic/gin"
)

//go:embed templates
var tplFS embed.FS

type Plugin struct {
	plugin.Base
}

func init() { plugin.Register(&Plugin{}) }

func (Plugin) ID() string             { return "purchase" }
func (Plugin) Name() string           { return "采购管理" }
func (Plugin) Version() string        { return "0.1.0" }
func (Plugin) Templates() fs.FS       { return tplFS }
func (Plugin) Dependencies() []string { return []string{"basedata", "inventory"} }

func (Plugin) RegisterRoutes(g *gin.RouterGroup, e *env.Env) {
	handler.New(e).Register(g)
}

func (Plugin) Menus() []menu.Item {
	return []menu.Item{{
		ID: "purchase", Title: "采购管理", Icon: "▼",
		Children: []menu.Item{
			{ID: "purchase.orders", Title: "采购订单", Path: "/app/purchase/orders", Perm: "purchase.order.read"},
		},
	}}
}

func (Plugin) Permissions() []rbac.PermissionDef {
	return []rbac.PermissionDef{
		{Code: "purchase.order.read", Desc: "采购订单查看"},
		{Code: "purchase.order.write", Desc: "采购订单维护"},
		{Code: "purchase.order.approve", Desc: "采购订单审核"},
	}
}

func (Plugin) SubscribeEvents(b *event.Bus, e *env.Env) {
	svc := service.New(e)
	b.Subscribe(contract.TopicFlowApproved, func(ctx context.Context, ev event.Event) error {
		if r, ok := ev.Payload.(contract.FlowResult); ok && r.TargetType == "purchase_order" {
			return svc.OnFlowResult(ctx, ev.TenantID, r, true)
		}
		return nil
	})
	b.Subscribe(contract.TopicFlowRejected, func(ctx context.Context, ev event.Event) error {
		if r, ok := ev.Payload.(contract.FlowResult); ok && r.TargetType == "purchase_order" {
			return svc.OnFlowResult(ctx, ev.TenantID, r, false)
		}
		return nil
	})
}
