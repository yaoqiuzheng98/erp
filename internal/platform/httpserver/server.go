// Package httpserver 装配 gin.Engine：中间件链与业务路由（单垂直，无插件）。
package httpserver

import (
	"html/template"
	"log/slog"
	"net/http"

	"erp/internal/billing"
	"erp/internal/dental/appointment"
	"erp/internal/dental/catalog"
	dental "erp/internal/dental/handler"
	"erp/internal/dental/patient"
	"erp/internal/platform/admin"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/sysadmin"
	"erp/internal/platform/web"
	"erp/internal/portal"

	"github.com/gin-gonic/gin"
)

type Services struct {
	Dental   *dental.Handler
	Billing  *billing.Service
	Patients *patient.Service
	Appts    *appointment.Service
	Catalog  *catalog.Service
}

func Build(e *env.Env, svc Services, tpl *template.Template) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())
	// 生产/测试均在 Caddy 反代后（容器 8080 不对外发布），信任内网段
	// 让 c.ClientIP() 穿透 X-Forwarded-For 取到真实客户端 IP（审计/限流用）。
	if err := r.SetTrustedProxies([]string{
		"127.0.0.1", "::1",
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	}); err != nil {
		slog.Error("set trusted proxies", "err", err)
	}
	r.SetHTMLTemplate(tpl)

	if staticFS, err := web.StaticFS(); err == nil {
		r.StaticFS("/static", staticFS)
	}

	// 限流守卫：桶按「限额+IP」隔离，一个实例可同时用于浏览(300)与登录(20)
	loginGuard := portal.NewGuard(e, svc.Patients)

	r.GET("/", func(c *gin.Context) {
		if _, err := c.Cookie(e.Cfg.Session.CookieName); err == nil {
			c.Redirect(http.StatusFound, "/app/appointments")
		} else {
			c.Redirect(http.StatusFound, "/login")
		}
	})

	// ---------- 租户认证（登录防爆破：同 IP 每分钟 20 次） ----------
	r.GET("/login", mw.Session(e), func(c *gin.Context) {
		if mw.User(c) != nil {
			c.Redirect(http.StatusFound, "/app/appointments")
			return
		}
		web.Render(c, e, "login", nil)
	})
	r.POST("/login", mw.Session(e), loginGuard.RateLimit(20), loginTenant(e))
	r.POST("/logout", logout(e, e.Cfg.Session.CookieName))

	// ---------- 系统后台认证 ----------
	r.GET("/sysadmin/login", mw.SysSession(e), func(c *gin.Context) {
		web.Render(c, e, "sys/login", nil)
	})
	r.POST("/sysadmin/login", mw.SysSession(e), loginGuard.RateLimit(20), loginSys(e))
	r.POST("/sysadmin/logout", logout(e, e.Cfg.Session.SysCookieName))

	// ---------- 门诊业务 /app（首页即今日预约） ----------
	app := r.Group("/app",
		mw.Session(e), mw.TenantResolver(e), mw.CSRF(), mw.Auth())
	app.GET("/notifications", notifications(e))
	app.POST("/notifications/:id/read", notificationRead(e))
	app.GET("/password", passwordPage(e))
	app.POST("/password", passwordChange(e))
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

	// ---------- 患者端 H5 /p/:tid（公开 + 患者会话） ----------
	guard := portal.NewGuard(e, svc.Patients)
	portal.NewWeb(e, svc.Patients, svc.Appts, svc.Catalog, svc.Billing).Register(
		r.Group("/p/:tid", portal.TenantByID(e), guard.RateLimit(300), guard.PatientAuth(), mw.CSRF()),
	)

	// ---------- 患者端 JSON API /api/p/:tid（小程序预留，Bearer token） ----------
	portal.NewAPI(e, guard, svc.Patients, svc.Appts, svc.Catalog, svc.Billing).Register(
		r.Group("/api/p/:tid", portal.TenantByID(e), guard.RateLimit(300), guard.PatientAuth()),
	)

	return r
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		slog.Info("http", "m", c.Request.Method, "p", c.Request.URL.Path, "s", c.Writer.Status())
	}
}
