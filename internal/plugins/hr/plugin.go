// Package hr 人事插件：员工档案、考勤、请假（请假可挂审批流）。
package hr

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
	"erp/internal/plugins/hr/handler"
	"erp/internal/plugins/hr/service"

	"github.com/gin-gonic/gin"
)

//go:embed templates
var tplFS embed.FS

type Plugin struct {
	plugin.Base
}

func init() { plugin.Register(&Plugin{}) }

func (Plugin) ID() string       { return "hr" }
func (Plugin) Name() string     { return "人事" }
func (Plugin) Version() string  { return "0.1.0" }
func (Plugin) Templates() fs.FS { return tplFS }

func (Plugin) RegisterRoutes(g *gin.RouterGroup, e *env.Env) {
	handler.New(e).Register(g)
}

func (Plugin) Menus() []menu.Item {
	return []menu.Item{{
		ID: "hr", Title: "人事", Icon: "♟",
		Children: []menu.Item{
			{ID: "hr.employees", Title: "员工", Path: "/app/hr/employees", Perm: "hr.read"},
			{ID: "hr.attends", Title: "考勤", Path: "/app/hr/attends", Perm: "hr.read"},
			{ID: "hr.leaves", Title: "请假", Path: "/app/hr/leaves", Perm: "hr.read"},
		},
	}}
}

func (Plugin) Permissions() []rbac.PermissionDef {
	return []rbac.PermissionDef{
		{Code: "hr.read", Desc: "人事查看"},
		{Code: "hr.write", Desc: "人事维护"},
	}
}

func (Plugin) SubscribeEvents(b *event.Bus, e *env.Env) {
	svc := service.New(e)
	b.Subscribe(contract.TopicFlowApproved, func(ctx context.Context, ev event.Event) error {
		if r, ok := ev.Payload.(contract.FlowResult); ok && r.TargetType == "hr_leave" {
			return svc.OnFlowResult(ctx, ev.TenantID, r, true)
		}
		return nil
	})
	b.Subscribe(contract.TopicFlowRejected, func(ctx context.Context, ev event.Event) error {
		if r, ok := ev.Payload.(contract.FlowResult); ok && r.TargetType == "hr_leave" {
			return svc.OnFlowResult(ctx, ev.TenantID, r, false)
		}
		return nil
	})
}
