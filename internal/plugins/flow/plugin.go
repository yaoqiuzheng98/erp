// Package flow 审批流插件：接收 doc.submit 事件产生审批单，结论经事件回写业务插件。
package flow

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
	"erp/internal/plugins/flow/handler"
	"erp/internal/plugins/flow/service"

	"github.com/gin-gonic/gin"
)

//go:embed templates
var tplFS embed.FS

type Plugin struct {
	plugin.Base
}

func init() { plugin.Register(&Plugin{}) }

func (Plugin) ID() string       { return "flow" }
func (Plugin) Name() string     { return "审批流" }
func (Plugin) Version() string  { return "0.1.0" }
func (Plugin) Templates() fs.FS { return tplFS }

func (Plugin) RegisterRoutes(g *gin.RouterGroup, e *env.Env) {
	handler.New(e).Register(g)
}

func (Plugin) Menus() []menu.Item {
	return []menu.Item{{
		ID: "flow", Title: "审批中心", Icon: "✓",
		Children: []menu.Item{
			{ID: "flow.approvals", Title: "审批单", Path: "/app/flow/approvals", Perm: "flow.read"},
		},
	}}
}

func (Plugin) Permissions() []rbac.PermissionDef {
	return []rbac.PermissionDef{
		{Code: "flow.read", Desc: "审批单查看"},
		{Code: "flow.approve", Desc: "审批操作"},
	}
}

func (Plugin) SubscribeEvents(b *event.Bus, e *env.Env) {
	svc := service.New(e)
	b.Subscribe(contract.TopicDocSubmit, func(ctx context.Context, ev event.Event) error {
		if !e.Gate.IsEnabled(ctx, ev.TenantID, "flow") {
			return nil
		}
		if d, ok := ev.Payload.(contract.DocSubmit); ok {
			return svc.OnSubmit(ctx, ev.TenantID, d)
		}
		return nil
	})
}
