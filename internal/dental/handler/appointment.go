package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"erp/internal/dental/appointment"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/tz"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (h *Handler) registerAppointment(g *gin.RouterGroup) {
	// /app 即预约列表（默认当天，今日预约已并入此处）。
	g.GET("", mw.RequirePerm("appt.read"), func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/app/appointments")
	})
	g.GET("/appointments", mw.RequirePerm("appt.read"), h.appointments)
	g.POST("/appointments", mw.RequirePerm("appt.write"), h.createAppt)
	g.GET("/appointments/:id/bill", mw.RequirePerm("appt.read"), h.billPage)
	g.POST("/appointments/:id/checkin", mw.RequirePerm("appt.write"), h.checkin)
	// 兼容旧版缓存页面上的 /arrive 入口（行为同签到）。
	g.POST("/appointments/:id/arrive", mw.RequirePerm("appt.write"), h.checkin)
	g.POST("/appointments/:id/serve", mw.RequirePerm("appt.write"), h.serve)
	g.POST("/appointments/:id/done", mw.RequirePerm("appt.write"), h.done)
	g.POST("/appointments/:id/noshow", mw.RequirePerm("appt.write"), h.noshow)
	g.POST("/appointments/:id/cancel", mw.RequirePerm("appt.write"), h.cancel)
}

func (h *Handler) appointments(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 30)
	// 默认看今天；全部走 ?date=all
	date := c.Query("date")
	if date == "" {
		date = tz.Today()
	} else if date == "all" {
		date = ""
	}
	doctorID, _ := bson.ObjectIDFromHex(c.Query("doctor_id"))
	list, total, err := h.appts.List(c.Request.Context(), mw.TenantID(c), date, doctorID, c.Query("phone"), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	pats, _, _ := h.pats.List(c.Request.Context(), mw.TenantID(c), "", 0, 500)
	items, _ := h.items.List(c.Request.Context(), mw.TenantID(c), true)
	doctors, _ := h.users.ListPractitioners(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "dental/appointments", gin.H{
		"Rows": list, "Pager": pager, "Date": date, "DoctorID": doctorID.Hex(), "Phone": c.Query("phone"),
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
		Date:  c.PostForm("date"), Slot: c.PostForm("slot"),
		Item: c.PostForm("item"), // 老单自由文本兜底；新单以明细为准
		Items: parseApptItems(c),
	}
	if t := mw.Tenant(c); t != nil {
		a.RegFee = t.RegFee
	}
	if err := h.appts.Create(c.Request.Context(), mw.TenantID(c), a); err != nil {
		slog.Error("backoffice appt failed", "tenant", mw.TenantID(c).Hex(),
			"patient", a.PatientID.Hex(), "doctor", a.DoctorID.Hex(),
			"date", a.Date, "slot", a.Slot, "err", err)
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "已预约")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

// billPage 诊疗单据页：明细 + 开单完成 + 结算状态，可打印后交前台收费。
func (h *Handler) billPage(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	tid := mw.TenantID(c)
	a, err := h.appts.ByID(c.Request.Context(), tid, id)
	if err != nil {
		c.String(http.StatusNotFound, "单据不存在")
		return
	}
	p, _ := h.pats.ByID(c.Request.Context(), tid, a.PatientID)
	b, _ := h.bill.BillByRef(c.Request.Context(), tid, id.Hex())
	items, _ := h.items.List(c.Request.Context(), tid, true)
	web.Render(c, h.e, "dental/bill", gin.H{"A": a, "P": p, "Bill": b, "Services": items})
}

// checkin 前台签到：报手机号找到单 → 分配医生 + 排号。
func (h *Handler) checkin(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	doc, no, err := h.appts.CheckIn(c.Request.Context(), mw.TenantID(c), id)
	if err != nil {
		web.SetFlash(c, "签到失败: "+err.Error())
	} else {
		web.SetFlash(c, "签到成功 → "+doc+" "+queueNo(no))
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func queueNo(no int) string {
	if no <= 0 {
		return ""
	}
	return strconv.Itoa(no) + "号"
}

// serve 开始就诊：候诊 → 就诊中。
func (h *Handler) serve(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.appts.StartServe(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	} else {
		web.SetFlash(c, "已开始就诊")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) done(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	charge, _ := strconv.ParseFloat(c.PostForm("charge"), 64)
	items := parseApptItems(c)
	deduct := false
	if t := mw.Tenant(c); t != nil {
		deduct = t.RegDeduct
	}
	next, err := h.appts.Complete(c.Request.Context(), mw.TenantID(c), id, items, charge, deduct, mw.User(c).Name)
	if err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	} else if next != "" {
		web.SetFlash(c, "已开单（待缴费），下一位："+next+"，请叫号")
	} else {
		web.SetFlash(c, "已开单（待缴费）")
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
