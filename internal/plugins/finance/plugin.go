// Package finance 财务插件：应收应付核销、收付款、费用、简易账簿。
package finance

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
	"erp/internal/plugins/finance/handler"
	"erp/internal/plugins/finance/model"
	"erp/internal/plugins/finance/service"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

//go:embed templates
var tplFS embed.FS

type Plugin struct {
	plugin.Base
}

func init() { plugin.Register(&Plugin{}) }

func (Plugin) ID() string             { return "finance" }
func (Plugin) Name() string           { return "财务" }
func (Plugin) Version() string        { return "0.1.0" }
func (Plugin) Templates() fs.FS       { return tplFS }
func (Plugin) Dependencies() []string { return []string{"basedata"} }

func (Plugin) RegisterRoutes(g *gin.RouterGroup, e *env.Env) {
	handler.New(e).Register(g)
}

func (Plugin) Menus() []menu.Item {
	return []menu.Item{{
		ID: "finance", Title: "财务", Icon: "￥",
		Children: []menu.Item{
			{ID: "finance.summary", Title: "账簿汇总", Path: "/app/finance", Perm: "finance.read"},
			{ID: "finance.ar", Title: "应收", Path: "/app/finance/receivables", Perm: "finance.read"},
			{ID: "finance.ap", Title: "应付", Path: "/app/finance/payables", Perm: "finance.read"},
			{ID: "finance.payments", Title: "收付款", Path: "/app/finance/payments", Perm: "finance.read"},
			{ID: "finance.expenses", Title: "费用", Path: "/app/finance/expenses", Perm: "finance.read"},
		},
	}}
}

func (Plugin) Permissions() []rbac.PermissionDef {
	return []rbac.PermissionDef{
		{Code: "finance.read", Desc: "财务查看"},
		{Code: "finance.write", Desc: "财务操作"},
	}
}

// SubscribeEvents 订单审核 → 生成应收/应付。
func (Plugin) SubscribeEvents(b *event.Bus, e *env.Env) {
	svc := service.New(e)
	b.Subscribe(contract.TopicSalesApproved, func(ctx context.Context, ev event.Event) error {
		if !e.Gate.IsEnabled(ctx, ev.TenantID, "finance") {
			return nil
		}
		o, ok := ev.Payload.(contract.OrderApproved)
		if !ok {
			return nil
		}
		name := partnerName(ctx, e, ev.TenantID, o.PartnerID, "customer")
		return svc.OnOrderApproved(ctx, ev.TenantID, o, model.BillAR, name)
	})
	b.Subscribe(contract.TopicPurchaseApproved, func(ctx context.Context, ev event.Event) error {
		if !e.Gate.IsEnabled(ctx, ev.TenantID, "finance") {
			return nil
		}
		o, ok := ev.Payload.(contract.OrderApproved)
		if !ok {
			return nil
		}
		name := partnerName(ctx, e, ev.TenantID, o.PartnerID, "supplier")
		return svc.OnOrderApproved(ctx, ev.TenantID, o, model.BillAP, name)
	})
}

// partnerName 经主数据服务查往来单位名称。
func partnerName(ctx context.Context, e *env.Env, tenantID, id bson.ObjectID, kind string) string {
	master, err := contract.Master(e)
	if err != nil {
		return ""
	}
	var list []contract.PartnerRef
	if kind == "customer" {
		list, _ = master.Customers(ctx, tenantID)
	} else {
		list, _ = master.Suppliers(ctx, tenantID)
	}
	for _, p := range list {
		if p.ID == id {
			return p.Name
		}
	}
	return ""
}
