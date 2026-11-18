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
	Slot   string                       // 档起点 HH:MM（快捷新建预填用）；区间外/未定时为空
	Label  string                       // 展示区间（如 08:30-09:00）
	Extra  bool                         // 区间外历史档/未定时：只展示，不给快捷新建
	Cells  [7][]appointment.Appointment // 起始档卡片（全部状态）
	Covers [7][]appointment.Appointment // 被长单覆盖的后续档（仅 booked）
	Used   [7]int                       // 该档已占用的 booked 单数（判断还能否加号）
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
	// 放号档：一行一档，时间列展示区间（与门诊配置的放号时间范围一致）。
	slotMinutes := appointment.DefaultSlotMinutes
	var window []model.HourSeg
	if t := mw.Tenant(c); t != nil {
		slotMinutes, _ = t.SlotConfig()
		window = t.SlotWindow()
	}
	// 老单没存结束时间：按当前粒度补一个内存值，模板统一按 [slot,end_slot) 展示。
	for i := range list {
		list[i].NormalizeEnd(slotMinutes)
	}
	// 卡片展示患者性别/年龄：按本周预约的患者 ID 批量取当前档案。
	patProfiles := make(map[string]string, len(list))
	patIDs := make([]bson.ObjectID, 0, len(list))
	seenPat := make(map[bson.ObjectID]struct{}, len(list))
	for _, a := range list {
		if a.PatientID.IsZero() {
			continue
		}
		if _, ok := seenPat[a.PatientID]; ok {
			continue
		}
		seenPat[a.PatientID] = struct{}{}
		patIDs = append(patIDs, a.PatientID)
	}
	if len(patIDs) > 0 {
		ps, err := h.pats.ByIDs(c.Request.Context(), mw.TenantID(c), patIDs)
		if err != nil {
			slog.Error("appointment patient profiles", "err", err)
		} else {
			for _, p := range ps {
				parts := make([]string, 0, 2)
				if p.Gender != "" {
					parts = append(parts, p.Gender)
				}
				if p.AgeText() != "" {
					parts = append(parts, p.AgeText()+"岁")
				}
				patProfiles[p.ID.Hex()] = strings.Join(parts, " · ")
			}
		}
	}
	today := tz.Today()
	dayIdx := map[string]int{}
	cols := make([]dayCol, 7)
	for i, d := range days {
		dayIdx[d] = i
		cols[i] = dayCol{Date: d, Weekday: weekdays[i], IsToday: d == today}
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
		j, onGrid := idx[a.Slot]
		if onGrid {
			rows[j].Cells[i] = append(rows[j].Cells[i], a)
		} else if isHHMM(a.Slot) {
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
		// 长单占后续档：只标 booked，覆盖格给前台看，避免误加号。
		// 区间外历史档也按重叠标到周视图，避免患者端显示已占、前台却看不到。
		if a.Status != appointment.Booked {
			continue
		}
		start := appointment.SlotStart(a.Slot)
		if start < 0 {
			continue
		}
		end := a.EndMinutes(slotMinutes)
		for k := range rows {
			ks := appointment.SlotStart(rows[k].Slot)
			if ks < 0 {
				continue
			}
			if ks < end && ks+slotMinutes > start {
				rows[k].Used[i]++
				if !onGrid || k != j {
					rows[k].Covers[i] = append(rows[k].Covers[i], a)
				}
			}
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
	docs := h.treatUsers(c)
	web.Render(c, h.e, "dental/appointments", gin.H{
		"Days": cols, "Rows": rows, "WeekStart": days[0], "WeekEnd": days[6],
		"WeekTotal": len(list), "WeekMonday": days[0],
		"PrevQ": prevQ, "NextQ": nextQ, "TodayQ": todayQ,
		"Phone":    phone,
		"Patients": pats,
		"Doctors":  docs,
		// 可接诊医生数：一个档最多这么多单，格子没约满就还能"+新建"。
		"DoctorCount": len(docs),
		"Slots":       slots,
		"SlotMinutes": slotMinutes,
		"SlotSegs":    window,
		"ApptColors":  apptColors(list, docs),
		"PatProfiles": patProfiles,
	})
}

// doctorPalette 医生卡片底色（按可接诊医生顺序轮转，保证同屏医生颜色不同）。
var doctorPalette = []string{
	"doc-bg-0", "doc-bg-1", "doc-bg-2", "doc-bg-3",
	"doc-bg-4", "doc-bg-5", "doc-bg-6", "doc-bg-7",
}

// apptColors 预约 → 医生底色 class。同医生同色；不在册/无医生的用中性色。
func apptColors(list []appointment.Appointment, docs []auth.User) map[string]string {
	byID := make(map[string]string, len(docs))
	byName := make(map[string]string, len(docs))
	for i, u := range docs {
		cls := doctorPalette[i%len(doctorPalette)]
		byID[u.ID.Hex()] = cls
		byName[u.Name] = cls
	}
	out := make(map[string]string, len(list))
	for _, a := range list {
		cls := ""
		if !a.DoctorID.IsZero() {
			cls = byID[a.DoctorID.Hex()]
		}
		if cls == "" {
			cls = byName[a.Doctor]
		}
		if cls == "" {
			cls = "doc-bg-unknown"
		}
		out[a.ID.Hex()] = cls
	}
	return out
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
		EndSlot:  c.PostForm("end_slot"),
		DoctorID: did,
	}
	// 后台可建长时段（手术等占多个档）；患者端入口不传 end_slot，仍是一档一单。
	opts := appointment.BookOpts{Minutes: appointment.DefaultSlotMinutes, AllowSpan: true}
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
			"date", a.Date, "slot", a.Slot, "end_slot", a.EndSlot, "err", err)
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		h.audit(c, audit.ActApptCreate, a.PatientName+" "+a.Date+" "+a.Slot+"-"+a.EndSlot, "")
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
