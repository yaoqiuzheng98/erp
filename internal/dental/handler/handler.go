package handler

import (
	"erp/internal/dental/appointment"
	"erp/internal/dental/catalog"
	"erp/internal/dental/patient"
	"erp/internal/dental/staff"
	"erp/internal/platform/env"

	"github.com/gin-gonic/gin"
)

// Handler 门诊 HTTP 层：薄层，只做参数解析→service→渲染。
type Handler struct {
	e     *env.Env
	pats  *patient.Service
	appts *appointment.Service
	items *catalog.Service
	staff *staff.Service
}

func New(e *env.Env, pats *patient.Service, appts *appointment.Service,
	items *catalog.Service, staff *staff.Service) *Handler {
	return &Handler{e: e, pats: pats, appts: appts, items: items, staff: staff}
}

func (h *Handler) Register(g *gin.RouterGroup) {
	h.registerPatient(g)
	h.registerAppointment(g)
	h.registerCatalog(g)
	h.registerStaff(g)
}
