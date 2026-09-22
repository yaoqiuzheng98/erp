// Package env 聚合平台服务，经构造函数注入（DI），插件只依赖 Env。
package env

import (
	"context"
	"sync"

	"erp/internal/platform/attach"
	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/config"
	"erp/internal/platform/db"
	"erp/internal/platform/dict"
	"erp/internal/platform/event"
	"erp/internal/platform/notify"
	"erp/internal/platform/org"
	"erp/internal/platform/rbac"
	"erp/internal/platform/seqno"
	"erp/internal/platform/session"
	"erp/internal/platform/task"
	"erp/internal/platform/tenant"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// PluginGate 由 plugin.Manager 实现，避免 env→plugin 循环依赖。
type PluginGate interface {
	IsEnabled(ctx context.Context, tenantID bson.ObjectID, pluginID string) bool
	EnabledSet(ctx context.Context, tenantID bson.ObjectID) map[string]bool
	ListWithStatus(ctx context.Context, tenantID bson.ObjectID) ([]map[string]any, error)
	Enable(ctx context.Context, e *Env, tenantID bson.ObjectID, pluginID string) error
	Disable(ctx context.Context, e *Env, tenantID bson.ObjectID, pluginID string) error
}

type Env struct {
	Cfg      *config.Config
	DB       *db.DB
	Sessions *session.Manager
	Auth     *auth.Service
	Tenants  *tenant.Service
	RBAC     *rbac.Service
	Org      *org.Service
	Dict     *dict.Service
	Seq      *seqno.Generator
	Audit    *audit.Service
	Notify   *notify.Service
	Attach   *attach.Service
	Events   *event.Bus
	Tasks    *task.Scheduler
	Gate     PluginGate // 启动时装配 plugin.Manager

	svcMu    sync.RWMutex
	services map[string]any // Service Locator：插件间公开的服务
}

// Provide 注册插件公开服务（如 contract.MasterDataAPI 实现）。
func (e *Env) Provide(name string, v any) {
	e.svcMu.Lock()
	defer e.svcMu.Unlock()
	if e.services == nil {
		e.services = map[string]any{}
	}
	e.services[name] = v
}

// Service 按名取插件公开服务，未注册返回 nil。
func (e *Env) Service(name string) any {
	e.svcMu.RLock()
	defer e.svcMu.RUnlock()
	return e.services[name]
}
