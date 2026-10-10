package handler

import (
	"erp/internal/dental/appointment"
	"erp/internal/dental/catalog"
	"erp/internal/dental/patient"
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
}

func New(e *env.Env, pats *patient.Service, appts *appointment.Service,
	items *catalog.Service, users *auth.Service) *Handler {
	return &Handler{e: e, pats: pats, appts: appts, items: items, users: users}
}

func (h *Handler) Register(g *gin.RouterGroup) {
	h.registerPatient(g)
	h.registerAppointment(g)
	h.registerCatalog(g)
}

// audit 记门诊操作审计（签到/取消/建档等），与 admin 侧同格式。
func (h *Handler) audit(c *gin.Context, action, target, detail string) {
	mw.Audit(c, h.e, action, target, detail)
}
