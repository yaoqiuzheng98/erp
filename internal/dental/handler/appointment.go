package handler

import (
	"net/http"
	"strconv"

	"erp/internal/dental/appointment"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (h *Handler) registerAppointment(g *gin.RouterGroup) {
	g.GET("", mw.RequirePerm("appt.read"), h.today)
	g.GET("/appointments", mw.RequirePerm("appt.read"), h.appointments)
	g.POST("/appointments", mw.RequirePerm("appt.write"), h.createAppt)
	g.POST("/appointments/:id/arrive", mw.RequirePerm("appt.write"), h.arrive)
	g.POST("/appointments/:id/done", mw.RequirePerm("appt.write"), h.done)
	g.POST("/appointments/:id/noshow", mw.RequirePerm("appt.write"), h.noshow)
	g.POST("/appointments/:id/cancel", mw.RequirePerm("appt.write"), h.cancel)
}

// today 今日预约工作台（/app 首页）。
func (h *Handler) today(c *gin.Context) {
	list, _ := h.appts.Today(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "dental/index", gin.H{"Rows": list})
}

func (h *Handler) appointments(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 30)
	date := c.Query("date")
	doctorID, _ := bson.ObjectIDFromHex(c.Query("doctor_id"))
	list, total, err := h.appts.List(c.Request.Context(), mw.TenantID(c), date, doctorID, skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	pats, _, _ := h.pats.List(c.Request.Context(), mw.TenantID(c), "", 0, 500)
	items, _ := h.items.List(c.Request.Context(), mw.TenantID(c), true)
	doctors, _ := h.staff.ListDoctors(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "dental/appointments", gin.H{
		"Rows": list, "Pager": pager, "Date": date, "DoctorID": doctorID.Hex(),
		"Patients": pats, "Services": items, "Doctors": doctors,
	})
}

// parseApptItems 解析明细表单：service_id[] 多选 + qty_<hex> 数量。
func parseApptItems(c *gin.Context) []appointment.ApptItem {
	ids := c.PostFormArray("service_id")
	qtys := c.PostFormArray("qty")
	var out []appointment.ApptItem
	for i, sid := range ids {
		oid, err := bson.ObjectIDFromHex(sid)
		if err != nil || oid.IsZero() {
			continue
		}
		qty := 1.0
		if v := c.PostForm("qty_" + sid); v != "" {
			if q, err := strconv.ParseFloat(v, 64); err == nil && q > 0 {
				qty = q
			} else {
				continue
			}
		} else if i < len(qtys) && len(qtys) == len(ids) {
			if q, err := strconv.ParseFloat(qtys[i], 64); err == nil && q > 0 {
				qty = q
			} else if qtys[i] != "" {
				continue
			}
		}
		out = append(out, appointment.ApptItem{ServiceID: oid, Qty: qty})
	}
	return out
}

func (h *Handler) createAppt(c *gin.Context) {
	patID, _ := bson.ObjectIDFromHex(c.PostForm("patient_id"))
	docID, _ := bson.ObjectIDFromHex(c.PostForm("doctor_id"))
	a := &appointment.Appointment{
		PatientID: patID, DoctorID: docID,
		Chair: c.PostForm("chair"),
		Date:  c.PostForm("date"), Slot: c.PostForm("slot"),
		Item:  c.PostForm("item"),
		Items: parseApptItems(c),
	}
	if err := h.appts.Create(c.Request.Context(), mw.TenantID(c), a); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "已预约")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) arrive(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.appts.Arrive(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) done(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	charge, _ := strconv.ParseFloat(c.PostForm("charge"), 64)
	items := parseApptItems(c)
	if err := h.appts.Complete(c.Request.Context(), mw.TenantID(c), id, items, charge, mw.User(c).Username); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	} else {
		web.SetFlash(c, "已完成就诊")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) noshow(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.appts.NoShow(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) cancel(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.appts.Cancel(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}
