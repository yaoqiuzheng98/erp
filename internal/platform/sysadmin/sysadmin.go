// Package sysadmin 系统管理后台 handler：租户管理、插件目录、全局审计。
package sysadmin

import (
	"net/http"

	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/env"
	"erp/internal/platform/plugin"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Handler struct {
	e *env.Env
}

func Register(g *gin.RouterGroup, e *env.Env) {
	h := &Handler{e: e}
	g.GET("", h.home)
	g.GET("/tenants", h.tenants)
	g.POST("/tenants", h.createTenant)
	g.POST("/tenants/:id/toggle", h.toggleTenant)
	g.GET("/plugins", h.plugins)
	g.GET("/audit", h.auditLog)
}

func (h *Handler) home(c *gin.Context) {
	ctx := c.Request.Context()
	tenants, _ := h.e.Tenants.List(ctx)
	userCount, _ := h.e.DB.C("users").EstimatedDocumentCount(ctx)
	web.Render(c, h.e, "sys/home", gin.H{
		"TenantCount": len(tenants),
		"UserCount":   userCount,
		"PluginCount": len(plugin.All()),
	})
}

func (h *Handler) tenants(c *gin.Context) {
	list, _ := h.e.Tenants.List(c.Request.Context())
	web.Render(c, h.e, "sys/tenants", gin.H{"Tenants": list})
}

// createTenant 创建租户并初始化其管理员账号。
func (h *Handler) createTenant(c *gin.Context) {
	ctx := c.Request.Context()
	t, err := h.e.Tenants.Create(ctx, c.PostForm("name"))
	if err != nil {
		web.SetFlash(c, "创建租户失败: "+err.Error())
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	adminUser := c.PostForm("admin_user")
	if adminUser != "" {
		hash, _ := auth.HashPassword(c.PostForm("admin_pass"))
		_, _ = h.e.DB.C("users").InsertOne(ctx, &auth.User{
			TenantID: t.ID, Username: adminUser, Name: "租户管理员",
			PasswordHash: hash, Status: "active", IsTenantAdm: true,
		})
	}
	h.e.Audit.Log(ctx, audit.Entry{
		Username: "sysadmin", Action: "tenant.create", Target: t.Name,
		IP: c.ClientIP(),
	})
	web.SetFlash(c, "租户已创建")
	c.Redirect(http.StatusFound, "/sysadmin/tenants")
}

func (h *Handler) toggleTenant(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	t, err := h.e.Tenants.ByID(ctx, id)
	if err != nil {
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	status := "active"
	if t.Status == "active" {
		status = "suspended"
	}
	_ = h.e.Tenants.SetStatus(ctx, id, status)
	h.e.Audit.Log(ctx, audit.Entry{
		Username: "sysadmin", Action: "tenant.toggle", Target: t.Name, Detail: status,
		IP: c.ClientIP(),
	})
	c.Redirect(http.StatusFound, "/sysadmin/tenants")
}

func (h *Handler) plugins(c *gin.Context) {
	type row struct {
		ID, Name, Version string
		Deps              []string
	}
	var list []row
	for _, p := range plugin.All() {
		list = append(list, row{p.ID(), p.Name(), p.Version(), p.Dependencies()})
	}
	web.Render(c, h.e, "sys/plugins", gin.H{"Plugins": list})
}

func (h *Handler) auditLog(c *gin.Context) {
	list, _ := h.e.Audit.List(c.Request.Context(), bson.M{}, 300)
	web.Render(c, h.e, "sys/audit", gin.H{"Logs": list})
}
