// Package httpserver 装配 gin.Engine：中间件链与业务路由（单垂直，无插件）。
package httpserver

import (
	"html/template"
	"log/slog"
	"net/http"

	"erp/internal/billing"
	dental "erp/internal/dental/handler"
	"erp/internal/platform/admin"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/sysadmin"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
)

type Services struct {
	Dental  *dental.Handler
	Billing *billing.Service
}

func Build(e *env.Env, svc Services, tpl *template.Template) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())
	r.SetHTMLTemplate(tpl)

	if staticFS, err := web.StaticFS(); err == nil {
		r.StaticFS("/static", staticFS)
	}

	r.GET("/", func(c *gin.Context) {
		if _, err := c.Cookie(e.Cfg.Session.CookieName); err == nil {
			c.Redirect(http.StatusFound, "/app")
		} else {
			c.Redirect(http.StatusFound, "/login")
		}
	})

	// ---------- 租户认证 ----------
	r.GET("/login", mw.Session(e), func(c *gin.Context) {
		if mw.User(c) != nil {
			c.Redirect(http.StatusFound, "/app")
			return
		}
		web.Render(c, e, "login", nil)
	})
	r.POST("/login", mw.Session(e), loginTenant(e))
	r.POST("/logout", logout(e, e.Cfg.Session.CookieName))

	// ---------- 系统后台认证 ----------
	r.GET("/sysadmin/login", mw.SysSession(e), func(c *gin.Context) {
		web.Render(c, e, "sys/login", nil)
	})
	r.POST("/sysadmin/login", mw.SysSession(e), loginSys(e))
	r.POST("/sysadmin/logout", logout(e, e.Cfg.Session.SysCookieName))

	// ---------- 门诊业务 /app（首页即今日预约） ----------
	app := r.Group("/app",
		mw.Session(e), mw.TenantResolver(e), mw.CSRF(), mw.Auth())
	app.GET("/notifications", notifications(e))
	app.POST("/notifications/:id/read", notificationRead(e))
	app.POST("/attach", attachUpload(e))
	app.GET("/attach/:id", attachDownload(e))
	svc.Dental.Register(app)
	billing.NewHandler(e, svc.Billing).Register(app.Group("/billing"))

	// ---------- 租户管理区 /admin ----------
	adm := r.Group("/admin",
		mw.Session(e), mw.TenantResolver(e), mw.CSRF(), mw.Auth())
	admin.New(e).Register(adm)

	// ---------- 系统后台 /sysadmin ----------
	sys := r.Group("/sysadmin",
		mw.SysSession(e), mw.CSRF(), mw.SysAuth())
	sysadmin.Register(sys, e)

	return r
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		slog.Info("http", "m", c.Request.Method, "p", c.Request.URL.Path, "s", c.Writer.Status())
	}
}
