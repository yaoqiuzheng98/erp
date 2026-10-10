package handler

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"erp/internal/dental/appointment"
	"erp/internal/platform/audit"
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
	g.POST("/appointments/:id/checkin", mw.RequirePerm("appt.write"), h.checkin)
	// 兼容旧版缓存页面上的 /arrive 入口（行为同签到）。
	g.POST("/appointments/:id/arrive", mw.RequirePerm("appt.write"), h.checkin)
	g.POST("/appointments/:id/noshow", mw.RequirePerm("appt.write"), h.noshow)
	g.POST("/appointments/:id/cancel", mw.RequirePerm("appt.write"), h.cancel)
}

// dayCol 周视图表头列：日期 + 星期 + 今天高亮 + 当天单数。
type dayCol struct {
	Date    string
	Weekday string
	IsToday bool
	Total   int
}

// hourRow 周视图行：整点小时 + 7 天格子（与 Days 下标对齐）。
type hourRow struct {
	Hour  int
	Label string
	Cells [7][]appointment.Appointment
}

var weekdays = []string{"周一", "周二", "周三", "周四", "周五", "周六", "周日"}

// 周视图时间轴：8~20 点常驻（空也占行，看出空闲）；区间外时段另起行。
const weekOpenH, weekCloseH = 8, 20

// hourOf 时段 HH:MM 取整点；脏格式归 -1（未定时行）。
func hourOf(slot string) int {
	if len(slot) == 5 && slot[2] == ':' {
		if h, err := strconv.Atoi(slot[:2]); err == nil && h >= 0 && h <= 23 {
			return h
		}
	}
	return -1
}

// weekQuery 组翻周链接的保留参数（医生/电话筛选不断）。
func weekQuery(week, doctorID, phone string) template.URL {
	v := url.Values{}
	v.Set("week", week)
	if doctorID != "" {
		v.Set("doctor_id", doctorID)
	}
	if phone != "" {
		v.Set("phone", phone)
	}
	return template.URL(v.Encode())
}

func (h *Handler) appointments(c *gin.Context) {
	anchor := c.Query("week")
	if anchor == "" {
		// 兼容旧 ?date= 入口（回跳/书签）：date=all 视为本周。
		if d := c.Query("date"); d != "" && d != "all" {
			anchor = d
		}
	}
	days := tz.WeekOf(anchor)
	doctorID, _ := bson.ObjectIDFromHex(c.Query("doctor_id"))
	phone := c.Query("phone")
	list, err := h.appts.ListRange(c.Request.Context(), mw.TenantID(c), days[0], days[6], doctorID, phone, 1000)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	today := tz.Today()
	dayIdx := map[string]int{}
	cols := make([]dayCol, 7)
	for i, d := range days {
		dayIdx[d] = i
		cols[i] = dayCol{Date: d, Weekday: weekdays[i], IsToday: d == today}
	}
	// 按日期+小时分桶（ListRange 已按日期时段排好，桶内天然有序）。
	cells := map[int]*hourRow{}
	rowOf := func(hr int) *hourRow {
		if r, ok := cells[hr]; ok {
			return r
		}
		label := "未定时"
		if hr >= 0 {
			label = fmt.Sprintf("%02d:00", hr)
		}
		r := &hourRow{Hour: hr, Label: label}
		cells[hr] = r
		return r
	}
	for _, a := range list {
		i, ok := dayIdx[a.Date]
		if !ok {
			continue
		}
		cols[i].Total++
		r := rowOf(hourOf(a.Slot))
		r.Cells[i] = append(r.Cells[i], a)
	}
	// 行序：8~20 点常驻 + 区间外小时升序（未定时沉底）。
	rows := make([]hourRow, 0, weekCloseH-weekOpenH+1+len(cells))
	var extras []int
	for hr := range cells {
		if hr < weekOpenH || hr > weekCloseH {
			extras = append(extras, hr)
		}
	}
	sort.Ints(extras)
	for hr := weekOpenH; hr <= weekCloseH; hr++ {
		rows = append(rows, *rowOf(hr))
	}
	for _, hr := range extras {
		rows = append(rows, *cells[hr])
	}
	mon, _ := tz.DayStart(days[0])
	// 零值医生不进链接（否则翻周 URL 挂一串 0）。
	doctorHex := ""
	if !doctorID.IsZero() {
		doctorHex = doctorID.Hex()
	}
	prevQ := weekQuery(mon.AddDate(0, 0, -7).Format("2006-01-02"), doctorHex, phone)
	nextQ := weekQuery(mon.AddDate(0, 0, 7).Format("2006-01-02"), doctorHex, phone)
	todayQ := weekQuery(tz.Today(), doctorHex, phone)
	pats, _, _ := h.pats.List(c.Request.Context(), mw.TenantID(c), "", 0, 2000)
	doctors, _ := h.users.ListActive(c.Request.Context(), mw.TenantID(c))
	slotMinutes := appointment.DefaultSlotMinutes
	if t := mw.Tenant(c); t != nil {
		slotMinutes, _ = t.SlotConfig()
	}
	web.Render(c, h.e, "dental/appointments", gin.H{
		"Days": cols, "Rows": rows, "WeekStart": days[0], "WeekEnd": days[6],
		"WeekTotal": len(list), "WeekMonday": days[0],
		"PrevQ": prevQ, "NextQ": nextQ, "TodayQ": todayQ,
		"DoctorID": doctorHex, "Phone": phone,
		"Patients": pats, "Doctors": doctors,
		"Slots": appointment.DaySlots(slotMinutes),
	})
}

func (h *Handler) createAppt(c *gin.Context) {
	patID, _ := bson.ObjectIDFromHex(c.PostForm("patient_id"))
	docID, _ := bson.ObjectIDFromHex(c.PostForm("doctor_id"))
	a := &appointment.Appointment{
		PatientID: patID, DoctorID: docID,
		Date: c.PostForm("date"), Slot: c.PostForm("slot"),
		Item: c.PostForm("item"),
	}
	slotMinutes, slotCapacity := appointment.DefaultSlotMinutes, appointment.DefaultSlotCapacity
	if t := mw.Tenant(c); t != nil {
		slotMinutes, slotCapacity = t.SlotConfig()
	}
	if err := h.appts.Create(c.Request.Context(), mw.TenantID(c), a, slotMinutes, slotCapacity); err != nil {
		slog.Error("backoffice appt failed", "tenant", mw.TenantID(c).Hex(),
			"patient", a.PatientID.Hex(), "doctor", a.DoctorID.Hex(),
			"date", a.Date, "slot", a.Slot, "err", err)
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		h.audit(c, audit.ActApptCreate, a.PatientName+" "+a.Date+" "+a.Slot, "")
		web.SetFlash(c, "已预约")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

// checkin 前台签到：booked → done（签到即完成），未指定医生的自动分配。
func (h *Handler) checkin(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	doc, err := h.appts.CheckIn(c.Request.Context(), mw.TenantID(c), id)
	if err != nil {
		web.SetFlash(c, "签到失败: "+err.Error())
	} else {
		h.audit(c, audit.ActApptCheckin, doc, "")
		web.SetFlash(c, "签到完成，接诊医生："+doc)
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) noshow(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.appts.NoShow(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	} else {
		h.audit(c, audit.ActApptNoshow, id.Hex(), "")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) cancel(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	// 门诊后台取消已预约单
	err := h.appts.CancelUnpaid(c.Request.Context(), mw.TenantID(c), id)
	if err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	} else {
		h.audit(c, audit.ActApptCancel, id.Hex(), "")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}
