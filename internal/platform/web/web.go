// Package web 模板装配与渲染（适配器模式：封装 c.HTML）。
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"erp/internal/platform/auth"
	"erp/internal/platform/env"
	"erp/internal/platform/menu"
	"erp/internal/platform/middleware"
	"erp/internal/platform/plugin"
	"erp/internal/platform/tenant"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

//go:embed templates static
var FS embed.FS

// Page 渲染公共数据：布局所需的用户/租户/菜单/CSRF/flash。
type Page struct {
	Title      string
	Path       string
	Data       any
	User       *auth.User
	Sys        *auth.SysAdmin
	Tenant     *tenant.Tenant
	Menu       []menu.Item
	CSRF       string
	Flash      string
	NotifCount int64
	Perms      map[string]bool
}

var adminMenu = []menu.Item{
	{ID: "admin", Title: "系统管理", Icon: "⚙", Children: []menu.Item{
		{ID: "admin.users", Title: "用户", Path: "/admin/users", Perm: "admin.users"},
		{ID: "admin.roles", Title: "角色", Path: "/admin/roles", Perm: "admin.roles"},
		{ID: "admin.depts", Title: "部门", Path: "/admin/depts", Perm: "admin.depts"},
		{ID: "admin.dicts", Title: "字典", Path: "/admin/dicts", Perm: "admin.dicts"},
		{ID: "admin.plugins", Title: "插件", Path: "/admin/plugins", Perm: "admin.plugins"},
		{ID: "admin.audit", Title: "审计日志", Path: "/admin/audit", Perm: "admin.audit"},
	}},
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"date": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.Format("2006-01-02 15:04")
		},
		"hex": func(id any) string {
			if oid, ok := id.(bson.ObjectID); ok {
				return oid.Hex()
			}
			return fmt.Sprintf("%v", id)
		},
		"mul": func(a, b float64) string { return fmt.Sprintf("%.2f", a*b) },
		"f2":  func(a float64) string { return fmt.Sprintf("%.2f", a) },
		"industryName": tenant.IndustryName,
	}
}

// Build 解析平台模板 + 全部注册插件模板到同一 *template.Template。
func Build(e *env.Env) (*template.Template, error) {
	t := template.New("root").Funcs(funcMap())
	var err error
	t, err = t.ParseFS(FS, "templates/*/*.html", "templates/*/*/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse platform templates: %w", err)
	}
	return plugin.ParseTemplates(t)
}

func StaticFS() (http.FileSystem, error) {
	sub, err := fs.Sub(FS, "static")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}

// buildPage 从 gin context 装配 Page。
func buildPage(c *gin.Context, e *env.Env, data any) *Page {
	p := &Page{
		Title: "",
		Path:  c.Request.URL.Path,
		Data:  data,
		CSRF:  c.GetString(middleware.CtxCSRF),
		Flash: takeFlash(c),
		Perms: middleware.Perms(c),
	}
	if u := middleware.User(c); u != nil {
		p.User = u
	}
	if v, ok := c.Get(middleware.CtxSys); ok {
		p.Sys = v.(*auth.SysAdmin)
	}
	if t := middleware.Tenant(c); t != nil {
		p.Tenant = t
		enabled := e.Gate.EnabledSet(c.Request.Context(), t.ID)
		items := []menu.Item{{ID: "home", Title: "工作台", Path: "/app", Icon: "⌂"}}
		items = append(items, plugin.MenusOf(enabled)...)
		items = append(items, adminMenu...)
		p.Menu = menu.Filter(items, func(code string) bool {
			return p.Perms["*"] || p.Perms[code]
		})
		p.NotifCount = e.Notify.UnreadCount(c.Request.Context(), t.ID, p.User.ID)
	}
	return p
}

// Render 渲染完整页面（含布局）。
func Render(c *gin.Context, e *env.Env, name string, data any) {
	c.HTML(http.StatusOK, name, buildPage(c, e, data))
}

// RenderFrag 渲染 HTMX 片段（仅内容块）。
func RenderFrag(c *gin.Context, e *env.Env, name string, data any) {
	c.HTML(http.StatusOK, name, buildPage(c, e, data))
}

// IsHTMX 判断当前请求是否为 HTMX 局部请求。
func IsHTMX(c *gin.Context) bool {
	return c.GetHeader("HX-Request") != ""
}

// Pager 简单分页器。
type Pager struct {
	Page  int
	Size  int
	Total int64
}

// ParsePager 解析 ?page= 参数，返回 skip/limit 与 Pager（Total 由 handler 后填）。
func ParsePager(c *gin.Context, defaultSize int) (skip int64, limit int64, p Pager) {
	page := 1
	if v := c.Query("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if defaultSize <= 0 {
		defaultSize = 20
	}
	p = Pager{Page: page, Size: defaultSize}
	return int64((page - 1) * defaultSize), int64(defaultSize), p
}

func (p Pager) Pages() int {
	if p.Size <= 0 {
		return 1
	}
	n := int(p.Total) / p.Size
	if int(p.Total)%p.Size != 0 {
		n++
	}
	if n < 1 {
		n = 1
	}
	return n
}

func (p Pager) HasPrev() bool { return p.Page > 1 }
func (p Pager) HasNext() bool { return p.Page < p.Pages() }
func (p Pager) Prev() int     { return p.Page - 1 }
func (p Pager) Next() int     { return p.Page + 1 }

const flashCookie = "erp_flash"

// SetFlash 设置一次性提示消息（cookie 携带，下一次渲染后清除）。
func SetFlash(c *gin.Context, msg string) {
	c.SetCookie(flashCookie, msg, 30, "/", "", false, true)
}

func takeFlash(c *gin.Context) string {
	v, err := c.Cookie(flashCookie)
	if err != nil || v == "" {
		return ""
	}
	c.SetCookie(flashCookie, "", -1, "/", "", false, true)
	return v
}
