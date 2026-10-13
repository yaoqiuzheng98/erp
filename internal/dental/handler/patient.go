package handler

import (
	"net/http"

	"erp/internal/dental/patient"
	"erp/internal/platform/audit"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (h *Handler) registerPatient(g *gin.RouterGroup) {
	g.GET("/patients", mw.RequirePerm("patient.read"), h.patients)
	g.POST("/patients", mw.RequirePerm("patient.write"), h.createPatient)
	g.GET("/patients/:id", mw.RequirePerm("patient.read"), h.patient)
	g.POST("/patients/:id/unbind-wx", mw.RequirePerm("patient.write"), h.unbindWX)
}

func (h *Handler) patients(c *gin.Context) {
	tenantID := mw.TenantID(c)
	skip, limit, pager := web.ParsePager(c, 30)
	q := c.Query("q")
	list, total, err := h.pats.List(c.Request.Context(), tenantID, q, skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "dental/patients", gin.H{"Rows": list, "Pager": pager, "Q": q})
}

func (h *Handler) createPatient(c *gin.Context) {
	p := &patient.Patient{
		Name: c.PostForm("name"), Phone: c.PostForm("phone"),
	}
	if err := h.pats.Create(c.Request.Context(), mw.TenantID(c), p); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		h.audit(c, audit.ActPatientCreate, p.Name+" "+p.Phone, "")
		web.SetFlash(c, "患者已建档")
	}
	c.Redirect(http.StatusFound, "/app/patients")
}

func (h *Handler) patient(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	tid := mw.TenantID(c)
	p, err := h.pats.ByID(c.Request.Context(), tid, id)
	if err != nil {
		c.String(http.StatusNotFound, "患者不存在")
		return
	}
	appts, _ := h.appts.OfPatient(c.Request.Context(), tid, id)
	web.Render(c, h.e, "dental/patient", gin.H{
		"P": p, "Appts": appts,
	})
}

// unbindWX 解绑微信：清掉患者上的 openId，之后该微信可重新建档/绑定。
func (h *Handler) unbindWX(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	tid := mw.TenantID(c)
	p, err := h.pats.ByID(c.Request.Context(), tid, id)
	if err != nil {
		web.SetFlash(c, "患者不存在")
		c.Redirect(http.StatusFound, "/app/patients")
		return
	}
	if err := h.pats.ClearOpenID(c.Request.Context(), tid, id); err != nil {
		web.SetFlash(c, "解绑失败: "+err.Error())
	} else {
		h.audit(c, audit.ActPatientCreate, "解绑微信 "+p.Name+" "+p.Phone, "")
		web.SetFlash(c, "已解绑，"+p.Name+" 的微信可重新建档")
	}
	c.Redirect(http.StatusFound, "/app/patients/"+id.Hex())
}
