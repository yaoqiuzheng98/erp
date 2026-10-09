package handler

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

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
	g.GET("/appointments/:id/bill", mw.RequirePerm("appt.read"), h.billPage)
	g.POST("/appointments/:id/checkin", mw.RequirePerm("appt.write"), h.checkin)
	// 兼容旧版缓存页面上的 /arrive 入口（行为同签到）。
	g.POST("/appointments/:id/arrive", mw.RequirePerm("appt.write"), h.checkin)
	g.POST("/appointments/:id/serve", mw.RequirePerm("appt.write"), h.serve)
	g.POST("/appointments/:id/done", mw.RequirePerm("appt.write"), h.done)
	g.POST("/appointments/:id/noshow", mw.RequirePerm("appt.write"), h.noshow)
	g.POST("/appointments/:id/cancel", mw.RequirePerm("appt.write"), h.cancel)
	g.POST("/appointments/:id/payreg", mw.RequirePerm("appt.write"), h.payReg)
}

// payReg 前台收挂号费（现金/银行现场收，患者端走模拟支付；重复点幂等不重单）。
func (h *Handler) payReg(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	method := c.PostForm("method")
	if method == "" {
		method = "cash"
	}
	if err := h.appts.PayReg(c.Request.Context(), mw.TenantID(c), id, method, mw.User(c).Name); err != nil {
		web.SetFlash(c, "收费失败: "+err.Error())
	} else {
		h.audit(c, "billing.pay", "挂号费 "+id.Hex(), method)
		web.SetFlash(c, "挂号费已收")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
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
	items, _ := h.items.List(c.Request.Context(), mw.TenantID(c), true)
	doctors, _ := h.users.ListPractitioners(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "dental/appointments", gin.H{
		"Days": cols, "Rows": rows, "WeekStart": days[0], "WeekEnd": days[6],
		"WeekTotal": len(list), "WeekMonday": days[0],
		"PrevQ": prevQ, "NextQ": nextQ, "TodayQ": todayQ,
		"DoctorID": doctorHex, "Phone": phone,
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
	// 手工项（不在价目表）：三数组按序对齐，名称必填。
	names := c.PostFormArray("manual_name")
	mqtys := c.PostFormArray("manual_qty")
	mprices := c.PostFormArray("manual_price")
	for i, nm := range names {
		nm = strings.TrimSpace(nm)
		if nm == "" {
			continue
		}
		qty := 1.0
		if i < len(mqtys) {
			if q, err := strconv.ParseFloat(mqtys[i], 64); err == nil && q > 0 {
				qty = q
			} else {
				continue
			}
		}
		price := 0.0
		if i < len(mprices) {
			if p, err := strconv.ParseFloat(mprices[i], 64); err == nil && p >= 0 {
				price = p
			} else {
				continue
			}
		}
		out = append(out, appointment.ApptItem{Name: nm, Qty: qty, Price: price})
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
		h.audit(c, audit.ActApptCreate, a.PatientName+" "+a.Date+" "+a.Slot, "")
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
		h.audit(c, audit.ActApptCheckin, doc+" "+queueNo(no), "")
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
		h.audit(c, audit.ActApptServe, id.Hex(), "")
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
	next, err := h.appts.Complete(c.Request.Context(), mw.TenantID(c), id, items, charge, deduct,
		c.PostForm("diagnosis"), c.PostForm("result"), mw.User(c).Name)
	if err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	} else if next != "" {
		h.audit(c, audit.ActApptComplete, id.Hex(), "")
		web.SetFlash(c, "已开单（待缴费），下一位："+next+"，请叫号")
	} else {
		h.audit(c, audit.ActApptComplete, id.Hex(), "")
		web.SetFlash(c, "已开单（待缴费）")
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
	// 门诊后台取消含待缴费：未收单作废后再取消（患者端只调 Cancel，动不了已开单的）
	voided, err := h.appts.CancelUnpaid(c.Request.Context(), mw.TenantID(c), id)
	if err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	} else {
		detail := ""
		if len(voided) > 0 {
			detail = "作废: " + strings.Join(voided, ",")
		}
		h.audit(c, audit.ActApptCancel, id.Hex(), detail)
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}
