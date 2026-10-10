// Package env 聚合平台基础设施，经构造函数注入。
package env

import (
	"erp/internal/platform/attach"
	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/config"
	"erp/internal/platform/db"
	"erp/internal/platform/notify"
	"erp/internal/platform/rbac"
	"erp/internal/platform/seqno"
	"erp/internal/platform/session"
	"erp/internal/platform/tenant"
)

// Env 平台基础设施。业务服务（dental）在 main 中直连装配，
// 不经过 Env，避免 god object。
type Env struct {
	Cfg      *config.Config
	DB       *db.DB
	Sessions *session.Manager
	Auth     *auth.Service
	Tenants  *tenant.Service
	RBAC     *rbac.Service
	Seq      *seqno.Generator
	Audit    *audit.Service
	Notify   *notify.Service
	Attach   *attach.Service
}
