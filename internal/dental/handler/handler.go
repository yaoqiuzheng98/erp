package handler

import (
	"erp/internal/billing"
	"erp/internal/dental/appointment"
	"erp/internal/dental/catalog"
	"erp/internal/dental/patient"
	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"

	"github.com/gin-gonic/gin"
)

// Handler 门诊 HTTP 层：薄层，只做参数解析→service→渲染。
type Handler struct {
	e     *env.Env
	pats  *patient.Service
	appts *appointment.Service
	items *catalog.Service
	users *auth.Service
	bill  *billing.Service
}

func New(e *env.Env, pats *patient.Service, appts *appointment.Service,
	items *catalog.Service, users *auth.Service, bill *billing.Service) *Handler {
	return &Handler{e: e, pats: pats, appts: appts, items: items, users: users, bill: bill}
}

func (h *Handler) Register(g *gin.RouterGroup) {
	h.registerPatient(g)
	h.registerAppointment(g)
	h.registerCatalog(g)
}

// audit 记门诊操作审计（开单/核销/建档等），与 admin 侧同格式。
func (h *Handler) audit(c *gin.Context, action, target, detail string) {
	u := mw.User(c)
	h.e.Audit.Log(c.Request.Context(), audit.Entry{
		TenantID: mw.TenantID(c), UserID: u.ID, Username: u.Phone,
		Action: action, Target: target, Detail: detail, IP: c.ClientIP(),
	})
}
