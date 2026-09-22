// Package sysadmin 系统管理后台 handler：租户管理、插件目录、全局审计。
package sysadmin

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/env"
	"erp/internal/platform/industry"
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

// industryOptions 返回 GB/T 4754 全量行业节点（国标顺序），供租户行业 datalist。
func (h *Handler) industryOptions(ctx context.Context) []industry.Node {
	return h.e.Industries.All(ctx)
}

// parseIndustry 解析 datalist 提交的 "code 名称" 或纯 code；非法返回 false。
func (h *Handler) parseIndustry(ctx context.Context, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true // 通用
	}
	code, _, _ := strings.Cut(raw, " ")
	if !h.e.Industries.Exists(ctx, code) {
		return "", false
	}
	return code, true
}

func (h *Handler) tenants(c *gin.Context) {
	list, _ := h.e.Tenants.List(c.Request.Context())
	nodes := h.industryOptions(c.Request.Context())
	type jn struct {
		Code   string `json:"code"`
		Name   string `json:"name"`
		Parent string `json:"parent"`
	}
	jns := make([]jn, len(nodes))
	for i, n := range nodes {
		jns[i] = jn{n.Code, n.Name, n.Parent}
	}
	raw, _ := json.Marshal(jns)
	web.Render(c, h.e, "sys/tenants", gin.H{"Tenants": list, "IndustriesJSON": template.JS(raw)})
}

// createTenant 创建租户并初始化其管理员账号。
func (h *Handler) createTenant(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.PostForm("name")
	indCode, ok := h.parseIndustry(ctx, c.PostForm("industry"))
	if !ok {
		web.SetFlash(c, "行业标识无效: "+c.PostForm("industry"))
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	var dup bson.M
	if err := h.e.DB.C("tenants").FindOne(ctx, bson.M{"name": name}).Decode(&dup); err == nil {
		web.SetFlash(c, "已存在同名租户: "+name)
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	t, err := h.e.Tenants.Create(ctx, name, indCode)
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
	indCode, ok := h.parseIndustry(ctx, c.PostForm("industry"))
	t, err := h.e.Tenants.ByID(ctx, id)
	if err != nil || !ok {
		web.SetFlash(c, "行业标识无效")
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	// 新行业节点的祖先码链（启用校验同款语义）
	path := []string{indCode}
	if indCode == "" {
		path = nil
	} else if n, ok := h.e.Industries.Get(ctx, indCode); ok {
		path = n.Path
	}
	enabled := h.e.Gate.EnabledSet(ctx, id)
	var conflicts []string
	for _, p := range plugin.All() {
		if enabled[p.ID()] && !plugin.AppliesTo(p, path) {
			conflicts = append(conflicts, p.ID())
		}
	}
	if len(conflicts) > 0 {
		web.SetFlash(c, "已启用插件不适用该行业，请先禁用: "+strings.Join(conflicts, ", "))
		c.Redirect(http.StatusFound, "/sysadmin/tenants")
		return
	}
	_ = h.e.Tenants.SetIndustry(ctx, id, indCode)
	h.e.Audit.Log(ctx, audit.Entry{
		Username: "sysadmin", Action: "tenant.industry", Target: t.Name, Detail: indCode,
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
		indNames := "通用"
		if len(inds) > 0 {
			names := make([]string, len(inds))
			for i, code := range inds {
				names[i] = h.e.Industries.Name(c.Request.Context(), code)
			}
			indNames = strings.Join(names, "、")
		}
		list = append(list, row{p.ID(), p.Name(), p.Version(), p.Dependencies(), indNames})
	}
	web.Render(c, h.e, "sys/plugins", gin.H{"Plugins": list})
}

func (h *Handler) auditLog(c *gin.Context) {
	list, _ := h.e.Audit.List(c.Request.Context(), bson.M{}, 300)
	web.Render(c, h.e, "sys/audit", gin.H{"Logs": list})
}
