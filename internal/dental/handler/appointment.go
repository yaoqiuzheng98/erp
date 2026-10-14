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
	"erp/internal/platform/auth"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/model"
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

// slotRow 周视图行：一个放号档一行 + 7 天格子（与 Days 下标对齐）。
type slotRow struct {
	Slot  string // 档起点 HH:MM（快捷新建预填用）；区间外/未定时为空
	Label string // 展示区间（如 08:30-09:00）
	Extra bool   // 区间外历史档/未定时：只展示，不给快捷新建
	Cells [7][]appointment.Appointment
}

var weekdays = []string{"周一", "周二", "周三", "周四", "周五", "周六", "周日"}

// isHHMM 时段格式（周视图分桶兜底用，脏值归未定时行）。
func isHHMM(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// slotRangeLabel 档起点 → 区间标签（脏值原样返回）。
func slotRangeLabel(slot string, minutes int) string {
	if !isHHMM(slot) {
		return slot
	}
	h, _ := strconv.Atoi(slot[:2])
	m, _ := strconv.Atoi(slot[3:])
	end := h*60 + m + minutes
	return fmt.Sprintf("%s-%02d:%02d", slot, end/60, end%60)
}

// weekQuery 组翻周链接的保留参数（电话筛选不断）。
func weekQuery(week, phone string) template.URL {
	v := url.Values{}
	v.Set("week", week)
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
	phone := c.Query("phone")
	list, err := h.appts.ListRange(c.Request.Context(), mw.TenantID(c), days[0], days[6], phone, 1000)
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
	// 放号档：一行一档，时间列展示区间（与门诊配置的放号时间范围一致）。
	slotMinutes := appointment.DefaultSlotMinutes
	var window []model.HourSeg
	if t := mw.Tenant(c); t != nil {
		slotMinutes, _ = t.SlotConfig()
		window = t.SlotWindow()
	}
	slots := appointment.DaySlotsIn(slotMinutes, window)
	rows := make([]slotRow, 0, len(slots)+4)
	idx := map[string]int{}
	for _, s := range slots {
		idx[s.Value] = len(rows)
		rows = append(rows, slotRow{Slot: s.Value, Label: s.Label})
	}
	// 分桶：常驻档进对应行；区间外历史档另起行（时间升序）；脏值沉底"未定时"。
	extras := map[string]*slotRow{}
	var untimed *slotRow
	for _, a := range list {
		i, ok := dayIdx[a.Date]
		if !ok {
			continue
		}
		cols[i].Total++
		if j, ok := idx[a.Slot]; ok {
			rows[j].Cells[i] = append(rows[j].Cells[i], a)
			continue
		}
		if isHHMM(a.Slot) {
			r := extras[a.Slot]
			if r == nil {
				r = &slotRow{Label: slotRangeLabel(a.Slot, slotMinutes), Extra: true}
				extras[a.Slot] = r
			}
			r.Cells[i] = append(r.Cells[i], a)
		} else {
			if untimed == nil {
				untimed = &slotRow{Label: "未定时", Extra: true}
			}
			untimed.Cells[i] = append(untimed.Cells[i], a)
		}
	}
	extraKeys := make([]string, 0, len(extras))
	for k := range extras {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		rows = append(rows, *extras[k])
	}
	if untimed != nil {
		rows = append(rows, *untimed)
	}
	mon, _ := tz.DayStart(days[0])
	prevQ := weekQuery(mon.AddDate(0, 0, -7).Format("2006-01-02"), phone)
	nextQ := weekQuery(mon.AddDate(0, 0, 7).Format("2006-01-02"), phone)
	todayQ := weekQuery(tz.Today(), phone)
	pats, _, _ := h.pats.List(c.Request.Context(), mw.TenantID(c), "", 0, 2000)
	web.Render(c, h.e, "dental/appointments", gin.H{
		"Days": cols, "Rows": rows, "WeekStart": days[0], "WeekEnd": days[6],
		"WeekTotal": len(list), "WeekMonday": days[0],
		"PrevQ": prevQ, "NextQ": nextQ, "TodayQ": todayQ,
		"Phone":    phone,
		"Patients": pats,
		"Doctors":  h.treatUsers(c),
		"Slots":    slots,
	})
}

// treatUsers 可接诊医生列表（前台补录医生下拉用）。
func (h *Handler) treatUsers(c *gin.Context) []auth.User {
	users, _ := h.e.Auth.TreatUsers(c.Request.Context(), mw.TenantID(c))
	return users
}

func (h *Handler) createAppt(c *gin.Context) {
	patID, _ := bson.ObjectIDFromHex(c.PostForm("patient_id"))
	did, _ := bson.ObjectIDFromHex(c.PostForm("doctor_id"))
	a := &appointment.Appointment{
		PatientID: patID,
		Date:      c.PostForm("date"), Slot: c.PostForm("slot"),
		DoctorID: did,
	}
	opts := appointment.BookOpts{Minutes: appointment.DefaultSlotMinutes}
	tid := mw.TenantID(c)
	if t := mw.Tenant(c); t != nil {
		opts.Minutes, _ = t.SlotConfig()
		opts.Biz = t.SlotWindow()
		opts.Rest = t.IsRest
	}
	opts.Doctor = func(id bson.ObjectID) (string, bool) {
		u, err := h.e.Auth.UserByID(c.Request.Context(), tid, id)
		if err != nil || u.Status != "active" || !u.Treat {
			return "", false
		}
		return u.Name, true
	}
	if err := h.appts.Create(c.Request.Context(), tid, a, opts); err != nil {
		slog.Error("backoffice appt failed", "tenant", tid.Hex(),
			"patient", a.PatientID.Hex(), "doctor", a.DoctorID.Hex(),
			"date", a.Date, "slot", a.Slot, "err", err)
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		h.audit(c, audit.ActApptCreate, a.PatientName+" "+a.Date+" "+a.Slot, "")
		web.SetFlash(c, "已预约")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

// checkin 前台签到：booked → done（签到即完成）。
func (h *Handler) checkin(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	doc, err := h.appts.CheckIn(c.Request.Context(), mw.TenantID(c), id)
	if err != nil {
		web.SetFlash(c, "签到失败: "+err.Error())
	} else {
		h.audit(c, audit.ActApptCheckin, doc, "")
		msg := "签到完成"
		if doc != "" {
			msg += "，接诊医生：" + doc
		}
		web.SetFlash(c, msg)
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
