package handler

import (
	"net/http"
	"net/url"

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
	g.POST("/patients/:id/update", mw.RequirePerm("patient.write"), h.updatePatient)
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
	age, err := patient.ParseAge(c.PostForm("age"))
	if err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
		c.Redirect(http.StatusFound, "/app/patients")
		return
	}
	p := &patient.Patient{
		Name: c.PostForm("name"), Phone: c.PostForm("phone"),
		Gender: c.PostForm("gender"), Age: age,
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

// updatePatient 修改患者资料：姓名/性别/年龄；电话不允许修改。
func (h *Handler) updatePatient(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		web.SetFlash(c, "修改失败: 患者不存在")
		c.Redirect(http.StatusFound, "/app/patients")
		return
	}
	age, err := patient.ParseAge(c.PostForm("age"))
	if err != nil {
		web.SetFlash(c, "修改失败: "+err.Error())
		c.Redirect(http.StatusFound, patientListURL(c))
		return
	}
	if err := h.pats.Update(c.Request.Context(), mw.TenantID(c), id,
		c.PostForm("name"), c.PostForm("gender"), age); err != nil {
		web.SetFlash(c, "修改失败: "+err.Error())
	} else {
		h.audit(c, audit.ActPatientUpdate, c.PostForm("name")+" "+id.Hex(), "")
		web.SetFlash(c, "患者信息已修改")
	}
	c.Redirect(http.StatusFound, patientListURL(c))
}

// patientListURL 修改后回到原列表（保留搜索词和页码）。
func patientListURL(c *gin.Context) string {
	v := url.Values{}
	if q := c.PostForm("q"); q != "" {
		v.Set("q", q)
	}
	if p := c.PostForm("page"); p != "" && p != "1" {
		v.Set("page", p)
	}
	if len(v) > 0 {
		return "/app/patients?" + v.Encode()
	}
	return "/app/patients"
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
		// 解绑同时清掉该患者全部会话：旧 token 即刻失效（永久会话不会自己过期）。
		_ = h.e.Sessions.DeleteByUser(c.Request.Context(), tid, id)
		h.audit(c, audit.ActPatientCreate, "解绑微信 "+p.Name+" "+p.Phone, "")
		web.SetFlash(c, "已解绑，"+p.Name+" 的微信可重新建档")
	}
	c.Redirect(http.StatusFound, "/app/patients/"+id.Hex())
}
