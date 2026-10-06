// Package sysadmin 系统管理后台 handler：租户管理、全局审计。
package sysadmin

import (
	"log/slog"
	"net/http"

	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/env"
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
	g.POST("/tenants/:id/delete", h.deleteTenant)
	g.GET("/audit", h.auditLog)
}

func (h *Handler) home(c *gin.Context) {
	ctx := c.Request.Context()
	tenantCount, _ := h.e.Tenants.Count(ctx)
	userCount, _ := h.e.DB.C("users").EstimatedDocumentCount(ctx)
	web.Render(c, h.e, "sys/home", gin.H{
		"TenantCount": tenantCount,
		"UserCount":   userCount,
	})
}

func (h *Handler) tenants(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	list, _ := h.e.Tenants.List(c.Request.Context(), skip, limit)
	total, _ := h.e.Tenants.Count(c.Request.Context())
	pager.Total = total
	web.Render(c, h.e, "sys/tenants", gin.H{"Tenants": list, "Pager": pager})
}

// createTenant 创建租户，初始化管理员账号与默认角色。
func (h *Handler) createTenant(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.PostForm("name")
	if name == "" {
		web.SetFlash(c, "名称必填")
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	phone := c.PostForm("admin_phone")
	if !auth.ValidPhone(phone) {
		web.SetFlash(c, "管理员手机号格式不正确")
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	pass := c.PostForm("admin_pass")
	if len(pass) < auth.MinPasswordLen {
		web.SetFlash(c, "管理员密码至少 6 位")
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	var dup bson.M
	if err := h.e.DB.C("tenants").FindOne(ctx, bson.M{"name": name}).Decode(&dup); err == nil {
		web.SetFlash(c, "已存在同名租户: "+name)
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	t, err := h.e.Tenants.Create(ctx, name)
	if err != nil {
		web.SetFlash(c, "创建租户失败: "+err.Error())
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	if err := h.e.Auth.Create(ctx, t.ID, &auth.User{
		Name: "管理员", Phone: phone, IsTenantAdm: true,
	}, pass); err != nil {
		// 管理员建失败则整个门诊回滚，不留无主租户
		if rerr := h.e.Tenants.Purge(ctx, t.ID); rerr != nil {
			slog.Error("tenant rollback failed", "tenant", t.Name, "err", rerr)
		}
		web.SetFlash(c, "租户已创建，但管理员创建失败，已回滚: "+err.Error())
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	if err := h.e.RBAC.EnsureSeed(ctx, t.ID); err != nil {
		slog.Error("seed tenant roles", "tenant", t.Name, "err", err)
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

// deleteTenant 删除门诊及其全部数据（先清附件文件与元数据，再清库），不可恢复。
func (h *Handler) deleteTenant(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	t, err := h.e.Tenants.ByID(ctx, id)
	if err != nil {
		web.SetFlash(c, "门诊不存在")
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	if err := h.e.Attach.PurgeTenant(ctx, id); err != nil {
		web.SetFlash(c, "删除附件失败: "+err.Error())
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	if err := h.e.Tenants.Purge(ctx, id); err != nil {
		web.SetFlash(c, "删除失败: "+err.Error())
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	h.e.Audit.Log(ctx, audit.Entry{
		Username: "sysadmin", Action: "tenant.delete", Target: t.Name,
		IP: c.ClientIP(),
	})
	web.SetFlash(c, "门诊已删除: "+t.Name)
	c.Redirect(http.StatusFound, "/sysadmin/tenants")
}

func (h *Handler) auditLog(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 50)
	list, _ := h.e.Audit.List(c.Request.Context(), bson.M{}, skip, limit)
	total, _ := h.e.Audit.Count(c.Request.Context(), bson.M{})
	pager.Total = total
	web.Render(c, h.e, "sys/audit", gin.H{"Logs": list, "Pager": pager})
}
