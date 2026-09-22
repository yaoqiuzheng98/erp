package handler

import (
	"net/http"

	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"
	"erp/internal/plugins/flow/service"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Handler struct {
	e   *env.Env
	svc *service.Service
}

func New(e *env.Env) *Handler {
	return &Handler{e: e, svc: service.New(e)}
}

func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("/approvals", mw.RequirePerm("flow.read"), h.list)
	g.POST("/approvals/:id/approve", mw.RequirePerm("flow.approve"), h.approve)
	g.POST("/approvals/:id/reject", mw.RequirePerm("flow.approve"), h.reject)
}

func (h *Handler) list(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	status := c.DefaultQuery("status", "pending")
	list, total, err := h.svc.List(c.Request.Context(), mw.TenantID(c), status, skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "flow/approvals", gin.H{
		"List": list, "Pager": pager, "Status": status,
		"CanApprove": mw.Perms(c)["*"] || mw.Perms(c)["flow.approve"],
	})
}

func (h *Handler) decide(c *gin.Context, approved bool) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Decide(c.Request.Context(), mw.TenantID(c), id,
		mw.User(c).Username, c.PostForm("comment"), approved); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	} else {
		web.SetFlash(c, "已处理")
	}
	c.Redirect(http.StatusFound, "/app/flow/approvals")
}

func (h *Handler) approve(c *gin.Context) { h.decide(c, true) }
func (h *Handler) reject(c *gin.Context)  { h.decide(c, false) }
