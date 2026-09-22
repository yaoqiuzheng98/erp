// Package sales 销售管理插件：报价→订单→出库→应收。
package sales

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
	"erp/internal/plugins/sales/handler"
	"erp/internal/plugins/sales/service"

	"github.com/gin-gonic/gin"
)

//go:embed templates
var tplFS embed.FS

type Plugin struct {
	plugin.Base
}

func init() { plugin.Register(&Plugin{}) }

func (Plugin) ID() string             { return "sales" }
func (Plugin) Name() string           { return "销售管理" }
func (Plugin) Version() string        { return "0.1.0" }
func (Plugin) Templates() fs.FS       { return tplFS }
func (Plugin) Dependencies() []string { return []string{"basedata", "inventory"} }

func (Plugin) RegisterRoutes(g *gin.RouterGroup, e *env.Env) {
	handler.New(e).Register(g)
}

func (Plugin) Menus() []menu.Item {
	return []menu.Item{{
		ID: "sales", Title: "销售管理", Icon: "▲",
		Children: []menu.Item{
			{ID: "sales.orders", Title: "销售订单", Path: "/app/sales/orders", Perm: "sales.order.read"},
		},
	}}
}

func (Plugin) Permissions() []rbac.PermissionDef {
	return []rbac.PermissionDef{
		{Code: "sales.order.read", Desc: "销售订单查看"},
		{Code: "sales.order.write", Desc: "销售订单维护"},
		{Code: "sales.order.approve", Desc: "销售订单审核"},
	}
}

// SubscribeEvents 接收审批流结论（启用 flow 插件时生效）。
func (Plugin) SubscribeEvents(b *event.Bus, e *env.Env) {
	svc := service.New(e)
	b.Subscribe(contract.TopicFlowApproved, func(ctx context.Context, ev event.Event) error {
		if r, ok := ev.Payload.(contract.FlowResult); ok && r.TargetType == "sales_order" {
			return svc.OnFlowResult(ctx, ev.TenantID, r, true)
		}
		return nil
	})
	b.Subscribe(contract.TopicFlowRejected, func(ctx context.Context, ev event.Event) error {
		if r, ok := ev.Payload.(contract.FlowResult); ok && r.TargetType == "sales_order" {
			return svc.OnFlowResult(ctx, ev.TenantID, r, false)
		}
		return nil
	})
}
