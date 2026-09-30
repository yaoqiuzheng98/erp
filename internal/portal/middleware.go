package portal

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"erp/internal/dental/patient"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/session"

	"github.com/gin-gonic/gin"
)

// PatCookie 患者端会话 cookie（H5 用；小程序走 Authorization: Bearer）。
const PatCookie = "erp_pat"

// CtxPatient 上下文中的患者对象 key。
const CtxPatient = "portal.patient"

// TenantByCode 按短码解析租户（公开入口）：非法/停用直接 404。
func TenantByCode(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, err := e.Tenants.ByCode(c.Request.Context(), c.Param("code"))
		if err != nil {
			c.String(http.StatusNotFound, "门诊不存在")
			c.Abort()
			return
		}
		c.Set(mw.CtxTenant, t)
		c.Next()
	}
}

// Guard 患者侧守卫：认证 + 限流。
type Guard struct {
	e    *env.Env
	pats *patient.Service

	mu     sync.Mutex
	window time.Time
	hits   map[string]int
}

func NewGuard(e *env.Env, pats *patient.Service) *Guard {
	return &Guard{e: e, pats: pats, hits: map[string]int{}}
}

// PatientAuth 可选认证：cookie 或 Bearer token 有效则注入患者，无效也不拦，
// 由 handler 决定（H5 跳登录页，API 回 401）。
func (g *Guard) PatientAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Cookie(PatCookie)
		if token == "" {
			if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
				token = strings.TrimPrefix(h, "Bearer ")
			}
		}
		if token == "" {
			c.Next()
			return
		}
		s, err := g.e.Sessions.Get(c.Request.Context(), token, session.KindPatient)
		if err != nil {
			c.Next()
			return
		}
		p, err := g.pats.ByID(c.Request.Context(), s.TenantID, s.UserID)
		if err != nil {
			c.Next()
			return
		}
		c.Set(CtxPatient, p)
		// 同时注入会话，H5 表单的 CSRF 校验依赖它；API 分组无 CSRF，不受影响。
		c.Set(mw.CtxSession, s)
		c.Set(mw.CtxCSRF, s.CSRF)
		c.Next()
	}
}

// Patient 取当前患者，未登录返回 nil。
func Patient(c *gin.Context) *patient.Patient {
	if v, ok := c.Get(CtxPatient); ok {
		return v.(*patient.Patient)
	}
	return nil
}

// RateLimit 简易固定窗口限流（单进程内存版）：每分钟每 IP n 次，超了 429。
func (g *Guard) RateLimit(n int) gin.HandlerFunc {
	return func(c *gin.Context) {
		now := time.Now().Truncate(time.Minute)
		g.mu.Lock()
		if now.After(g.window) {
			g.window = now
			g.hits = map[string]int{}
		}
		ip := c.ClientIP()
		g.hits[ip]++
		over := g.hits[ip] > n
		g.mu.Unlock()
		if over {
			c.String(http.StatusTooManyRequests, "请求过于频繁，稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}
