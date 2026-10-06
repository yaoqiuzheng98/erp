// Package middleware Gin 中间件链（责任链 + 装饰器模式）。
package middleware

import (
	"net/http"

	"erp/internal/platform/auth"
	"erp/internal/platform/env"
	"erp/internal/platform/rbac"
	"erp/internal/platform/session"
	"erp/internal/platform/tenant"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	CtxSession = "session"
	CtxUser    = "user"
	CtxSys     = "sysadmin"
	CtxTenant  = "tenant"
	CtxPerms   = "perms"
	CtxCSRF    = "csrf"
)

// Session 解析租户会话 cookie，加载用户文档。
func Session(e *env.Env) gin.HandlerFunc {
	cookieName := e.Cfg.Session.CookieName
	return func(c *gin.Context) {
		token, err := c.Cookie(cookieName)
		if err == nil && token != "" {
			if s, err := e.Sessions.Get(c.Request.Context(), token, session.KindTenant); err == nil {
				c.Set(CtxSession, s)
				c.Set(CtxCSRF, s.CSRF)
				if u, err := e.Auth.UserByID(c.Request.Context(), s.TenantID, s.UserID); err == nil && u.Status == "active" {
					c.Set(CtxUser, u)
				}
			}
		}
		c.Next()
	}
}

// SysSession 解析系统后台会话 cookie。
func SysSession(e *env.Env) gin.HandlerFunc {
	cookieName := e.Cfg.Session.SysCookieName
	return func(c *gin.Context) {
		token, err := c.Cookie(cookieName)
		if err == nil && token != "" {
			if s, err := e.Sessions.Get(c.Request.Context(), token, session.KindSys); err == nil {
				c.Set(CtxSession, s)
				c.Set(CtxCSRF, s.CSRF)
				if a, err := e.Auth.SysAdminByID(c.Request.Context(), s.UserID); err == nil {
					c.Set(CtxSys, a)
				}
			}
		}
		c.Next()
	}
}

// TenantResolver 用户→租户，校验租户状态；并计算权限集。
func TenantResolver(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		u, ok := c.Get(CtxUser)
		if !ok {
			c.Next()
			return
		}
		user, ok := u.(*auth.User)
		if !ok {
			c.Next()
			return
		}
		t, err := e.Tenants.ByID(c.Request.Context(), user.TenantID)
		if err != nil || t.Status != "active" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "租户不可用"})
			return
		}
		c.Set(CtxTenant, t)
		perms := e.RBAC.PermSetOf(c.Request.Context(), user.TenantID, user.RoleIDs, user.IsTenantAdm)
		c.Set(CtxPerms, perms)
		c.Next()
	}
}

// CSRF 校验非 GET 请求的 token（表单 _csrf 或 X-CSRF-Token 头）。
func CSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
			c.Next()
			return
		}
		sess, ok := c.Get(CtxSession)
		if !ok {
			c.Next()
			return
		}
		s, ok := sess.(*session.Session)
		if !ok {
			c.Next()
			return
		}
		want := s.CSRF
		got := c.GetHeader("X-CSRF-Token")
		if got == "" {
			got = c.PostForm("_csrf")
		}
		if got != want {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "CSRF token 校验失败"})
			return
		}
		c.Next()
	}
}

// Auth 要求已登录租户用户，否则跳 /login。
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := c.Get(CtxUser); !ok {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}
		c.Next()
	}
}

// SysAuth 要求已登录系统超管。
func SysAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := c.Get(CtxSys); !ok {
			c.Redirect(http.StatusFound, "/sysadmin/login")
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequirePerm 权限码校验（装饰器模式）。
func RequirePerm(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		perms, _ := c.Get(CtxPerms)
		set, _ := perms.(map[string]bool)
		if !rbac.Has(set, code) {
			c.HTML(http.StatusForbidden, "error", gin.H{
				"Data": gin.H{"Msg": "没有权限: " + code},
				"CSRF": c.GetString(CtxCSRF),
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// Helpers 供 handler 取上下文对象（类型不对一律返回零值，不 panic）。
func User(c *gin.Context) *auth.User {
	if v, ok := c.Get(CtxUser); ok {
		if u, ok := v.(*auth.User); ok {
			return u
		}
	}
	return nil
}

func Tenant(c *gin.Context) *tenant.Tenant {
	if v, ok := c.Get(CtxTenant); ok {
		if t, ok := v.(*tenant.Tenant); ok {
			return t
		}
	}
	return nil
}

func TenantID(c *gin.Context) bson.ObjectID {
	if t := Tenant(c); t != nil {
		return t.ID
	}
	return bson.NilObjectID
}

func Perms(c *gin.Context) map[string]bool {
	if v, ok := c.Get(CtxPerms); ok {
		if m, ok := v.(map[string]bool); ok {
			return m
		}
	}
	return map[string]bool{}
}
