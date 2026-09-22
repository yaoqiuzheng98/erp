// Package sysadmin 系统管理后台 handler：租户管理、插件目录、全局审计。
package sysadmin

import (
	"net/http"
	"strings"

	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/env"
	"erp/internal/platform/plugin"
	"erp/internal/platform/tenant"
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
	g.POST("/tenants/:id/industry", h.setIndustry)
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

// industryOptions 内置行业目录 + 插件声明的新行业码（未知名称按码显示）。
func industryOptions() []tenant.Industry {
	opts := append([]tenant.Industry{}, tenant.Industries...)
	seen := map[string]bool{}
	for _, i := range opts {
		seen[i.Code] = true
	}
	for _, p := range plugin.All() {
		for _, ind := range p.Industries() {
			if !seen[ind] {
				seen[ind] = true
				opts = append(opts, tenant.Industry{Code: ind, Name: ind})
			}
		}
	}
	return opts
}

func industryValid(code string) bool {
	if code == "" {
		return true
	}
	for _, o := range industryOptions() {
		if o.Code == code {
			return true
		}
	}
	return false
}

func (h *Handler) tenants(c *gin.Context) {
	list, _ := h.e.Tenants.List(c.Request.Context())
	web.Render(c, h.e, "sys/tenants", gin.H{"Tenants": list, "Industries": industryOptions()})
}

// createTenant 创建租户并初始化其管理员账号。
func (h *Handler) createTenant(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.PostForm("name")
	industry := c.PostForm("industry")
	if !industryValid(industry) {
		web.SetFlash(c, "行业标识无效: "+industry)
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	var dup bson.M
	if err := h.e.DB.C("tenants").FindOne(ctx, bson.M{"name": name}).Decode(&dup); err == nil {
		web.SetFlash(c, "已存在同名租户: "+name)
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	t, err := h.e.Tenants.Create(ctx, name, industry)
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

// setIndustry 修改租户行业；若有已启用插件不适用于新行业则拒绝。
func (h *Handler) setIndustry(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	industry := c.PostForm("industry")
	t, err := h.e.Tenants.ByID(ctx, id)
	if err != nil || !industryValid(industry) {
		web.SetFlash(c, "行业标识无效")
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	enabled := h.e.Gate.EnabledSet(ctx, id)
	var conflicts []string
	for _, p := range plugin.All() {
		if enabled[p.ID()] && !plugin.AppliesTo(p, industry) {
			conflicts = append(conflicts, p.ID())
		}
	}
	if len(conflicts) > 0 {
		web.SetFlash(c, "已启用插件不适用该行业，请先禁用: "+strings.Join(conflicts, ", "))
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	_ = h.e.Tenants.SetIndustry(ctx, id, industry)
	h.e.Audit.Log(ctx, audit.Entry{
		Username: "sysadmin", Action: "tenant.industry", Target: t.Name, Detail: industry,
		IP: c.ClientIP(),
	})
	web.SetFlash(c, "行业已更新")
	c.Redirect(http.StatusFound, "/sysadmin/tenants")
}

func (h *Handler) plugins(c *gin.Context) {
	type row struct {
		ID, Name, Version string
		Deps              []string
		Industries        string
	}
	var list []row
	for _, p := range plugin.All() {
		inds := p.Industries()
		industry := "通用"
		if len(inds) > 0 {
			names := make([]string, len(inds))
			for i, code := range inds {
				names[i] = tenant.IndustryName(code)
			}
			industry = strings.Join(names, "、")
		}
		list = append(list, row{p.ID(), p.Name(), p.Version(), p.Dependencies(), industry})
	}
	web.Render(c, h.e, "sys/plugins", gin.H{"Plugins": list})
}

func (h *Handler) auditLog(c *gin.Context) {
	list, _ := h.e.Audit.List(c.Request.Context(), bson.M{}, 300)
	web.Render(c, h.e, "sys/audit", gin.H{"Logs": list})
}
