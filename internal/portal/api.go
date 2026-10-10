package portal

import (
	"net/http"
	"strings"

	"erp/internal/dental/appointment"
	"erp/internal/dental/catalog"
	"erp/internal/dental/patient"
	"erp/internal/platform/auth"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/session"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// API 患者端 JSON 接口（H5 与以后小程序共用）：统一包信封 {code,msg,data}。
type API struct {
	e     *env.Env
	guard *Guard
	pats  *patient.Service
	appts *appointment.Service
	items *catalog.Service
}

func NewAPI(e *env.Env, guard *Guard, pats *patient.Service, appts *appointment.Service,
	items *catalog.Service) *API {
	return &API{e: e, guard: guard, pats: pats, appts: appts, items: items}
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": data})
}

func fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"code": 1, "msg": msg})
}

func (h *API) Register(g *gin.RouterGroup) {
	g.POST("/login", h.guard.RateLimit(20), h.login)
	g.POST("/login-by-code", h.guard.RateLimit(20), h.loginByCode)
	g.POST("/register", h.guard.RateLimit(20), h.register)
	g.POST("/logout", h.logout)
	g.GET("/clinic", h.clinic)
	g.GET("/team", h.team)
	g.GET("/doctors", h.doctors)
	g.GET("/services", h.services)
	g.GET("/slots", h.slots)
	g.GET("/appointments", h.requirePatient, h.myAppointments)
	g.POST("/appointments", h.requirePatient, h.createAppointment)
	g.POST("/appointments/:id/cancel", h.requirePatient, h.cancelAppointment)
}

func (h *API) requirePatient(c *gin.Context) {
	if Patient(c) == nil {
		fail(c, http.StatusUnauthorized, "请先登录")
		c.Abort()
		return
	}
	c.Next()
}

type loginReq struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
	Code     string `json:"code"` // 小程序 wx.login 的 code（可选，登录成功顺手绑 openId）
}

func (h *API) login(c *gin.Context) {
	var in loginReq
	if err := c.ShouldBindJSON(&in); err != nil || in.Phone == "" || in.Password == "" {
		fail(c, http.StatusBadRequest, "手机号和密码必填")
		return
	}
	p, err := h.pats.Login(c.Request.Context(), mw.TenantID(c), in.Phone, in.Password)
	if err != nil {
		fail(c, http.StatusUnauthorized, "手机号或密码错误")
		return
	}
	// 密码登录成功即绑 openId（best-effort：绑定失败不影响本次登录，下次带 code 再试）。
	if in.Code != "" {
		if openid, err := ExchangeOpenID(h.e.Cfg.Wechat.AppID, h.e.Cfg.Wechat.Secret, in.Code); err == nil {
			_ = h.pats.BindOpenID(c.Request.Context(), mw.TenantID(c), p.ID, openid)
		}
	}
	s, err := h.e.Sessions.Create(c.Request.Context(), session.KindPatient, p.ID, p.TenantID)
	if err != nil {
		fail(c, http.StatusInternalServerError, "会话创建失败")
		return
	}
	c.SetCookie(PatCookie, s.ID, int(h.e.Cfg.Session.TTLHours)*3600, "/", "", h.e.Cfg.Session.Secure, true)
	ok(c, gin.H{"token": s.ID, "name": p.Name, "phone": p.Phone})
}

type codeReq struct {
	Code string `json:"code"`
}

// loginByCode 小程序免密登录：code 换 openId，命中已绑档案直接下发会话。
// 未绑一律软响应 200+bound:false（冷启动每次都调，别刷 4xx）；
// 前端只看 data 里有没有 token。
func (h *API) loginByCode(c *gin.Context) {
	var in codeReq
	if err := c.ShouldBindJSON(&in); err != nil || in.Code == "" {
		ok(c, gin.H{"bound": false, "reason": "缺少微信 code"})
		return
	}
	openid, err := ExchangeOpenID(h.e.Cfg.Wechat.AppID, h.e.Cfg.Wechat.Secret, in.Code)
	if err != nil {
		ok(c, gin.H{"bound": false, "reason": err.Error()})
		return
	}
	p, err := h.pats.ByOpenID(c.Request.Context(), mw.TenantID(c), openid)
	if err != nil {
		ok(c, gin.H{"bound": false, "reason": "未建档，请先自助建档"})
		return
	}
	s, err := h.e.Sessions.Create(c.Request.Context(), session.KindPatient, p.ID, p.TenantID)
	if err != nil {
		ok(c, gin.H{"bound": false, "reason": "会话创建失败"})
		return
	}
	c.SetCookie(PatCookie, s.ID, int(h.e.Cfg.Session.TTLHours)*3600, "/", "", h.e.Cfg.Session.Secure, true)
	ok(c, gin.H{"token": s.ID, "name": p.Name, "phone": p.Phone})
}

type registerReq struct {
	Phone string `json:"phone"`
	Name  string `json:"name"`
	Code  string `json:"code"` // 小程序 wx.login 的 code（可选，有则绑 openId）
}

// register 小程序自助建档：姓名+手机号。未配密码，只能走 openId 免密登录；
// 同电话已建档时认领式登录：带 code 能换出 openId 且老档案未绑微信 → 绑定并直接
// 登录；老档案已绑其他微信 → 拒绝到前台处理；无 code（H5）→ 提示走密码登录。
func (h *API) register(c *gin.Context) {
	var in registerReq
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || in.Phone == "" {
		fail(c, http.StatusBadRequest, "姓名和手机号必填")
		return
	}
	if !auth.ValidPhone(in.Phone) {
		fail(c, http.StatusBadRequest, "手机号格式不正确")
		return
	}
	tid := mw.TenantID(c)

	// 手机号已建档：尝试认领（code 换 openId 绑老档案）。
	if exist, total, err := h.pats.List(c.Request.Context(), tid, in.Phone, 0, 1); err == nil && total > 0 {
		old := exist[0]
		if in.Code == "" {
			fail(c, http.StatusConflict, "该手机号已建过档案，请直接登录")
			return
		}
		openid, err := ExchangeOpenID(h.e.Cfg.Wechat.AppID, h.e.Cfg.Wechat.Secret, in.Code)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		if old.OpenID != "" && old.OpenID != openid {
			fail(c, http.StatusConflict, "该手机号已绑定其他微信，请到门诊前台处理")
			return
		}
		if old.OpenID == "" {
			if err := h.pats.BindOpenID(c.Request.Context(), tid, old.ID, openid); err != nil {
				fail(c, http.StatusInternalServerError, "微信绑定失败")
				return
			}
		}
		s, err := h.e.Sessions.Create(c.Request.Context(), session.KindPatient, old.ID, old.TenantID)
		if err != nil {
			fail(c, http.StatusInternalServerError, "会话创建失败")
			return
		}
		c.SetCookie(PatCookie, s.ID, int(h.e.Cfg.Session.TTLHours)*3600, "/", "", h.e.Cfg.Session.Secure, true)
		ok(c, gin.H{"token": s.ID, "name": old.Name, "bound": true, "claimed": true})
		return
	}

	p := &patient.Patient{Name: in.Name, Phone: in.Phone}
	if err := h.pats.Create(c.Request.Context(), tid, p); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	bound := false
	if in.Code != "" {
		if openid, err := ExchangeOpenID(h.e.Cfg.Wechat.AppID, h.e.Cfg.Wechat.Secret, in.Code); err == nil {
			if err := h.pats.BindOpenID(c.Request.Context(), tid, p.ID, openid); err == nil {
				bound = true
			}
		}
	}
	s, err := h.e.Sessions.Create(c.Request.Context(), session.KindPatient, p.ID, p.TenantID)
	if err != nil {
		fail(c, http.StatusInternalServerError, "会话创建失败")
		return
	}
	c.SetCookie(PatCookie, s.ID, int(h.e.Cfg.Session.TTLHours)*3600, "/", "", h.e.Cfg.Session.Secure, true)
	ok(c, gin.H{"token": s.ID, "name": p.Name, "bound": bound})
}

func (h *API) logout(c *gin.Context) {
	token, _ := c.Cookie(PatCookie)
	if token == "" {
		if hstr := c.GetHeader("Authorization"); len(hstr) > 7 {
			token = hstr[7:]
		}
	}
	if token != "" {
		h.e.Sessions.Destroy(c.Request.Context(), token)
	}
	c.SetCookie(PatCookie, "", -1, "/", "", h.e.Cfg.Session.Secure, true)
	ok(c, nil)
}

func (h *API) services(c *gin.Context) {
	list, err := h.items.List(c.Request.Context(), mw.TenantID(c), true)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, it := range list {
		out = append(out, gin.H{
			"id": it.ID.Hex(), "name": it.Name, "category": it.Category,
			"price": it.Price, "unit": it.Unit,
		})
	}
	ok(c, out)
}

// slots 放号档位（小程序排班用）：粒度+每档人数+当天档位。
func (h *API) slots(c *gin.Context) {
	minutes, capacity := appointment.DefaultSlotMinutes, appointment.DefaultSlotCapacity
	if t := mw.Tenant(c); t != nil {
		minutes, capacity = t.SlotConfig()
	}
	ok(c, gin.H{
		"slot_minutes": minutes, "slot_capacity": capacity,
		"slots": appointment.DaySlots(minutes),
	})
}

// clinic 诊所首页信息（公开）：名称/介绍/地址/电话/营业时间/公告/挂号费。
func (h *API) clinic(c *gin.Context) {
	t := mw.Tenant(c)
	if t == nil {
		fail(c, http.StatusNotFound, "门诊不存在")
		return
	}
	ok(c, gin.H{
		"name": t.Name, "intro": t.Intro, "address": t.Address,
		"phone": t.Phone, "hours": t.Hours, "notice": t.Notice,
		"gallery": t.Gallery,
	})
}

// doctors 可接诊医生列表（公开，挂号选医生用）：只吐 id+name，不露手机号。
func (h *API) doctors(c *gin.Context) {
	list, err := h.e.Auth.ListPractitioners(c.Request.Context(), mw.TenantID(c))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, u := range list {
		out = append(out, gin.H{"id": u.ID.Hex(), "name": u.Name})
	}
	ok(c, out)
}

// team 首页团队（公开）：与 SSR 首页同名单（在职/有角色/未隐藏），头像只回文件 id，
// 图片走 /p/:tid/gallery/:id 公开白名单接口取。
func (h *API) team(c *gin.Context) {
	tid := mw.TenantID(c)
	list := teamMembers(c.Request.Context(), h.e, tid)
	out := make([]gin.H, 0, len(list))
	for _, m := range list {
		if m.Avatar != "" {
			out = append(out, gin.H{"name": m.Name, "roles": m.Roles, "bio": m.Bio, "avatar": m.Avatar})
		} else {
			out = append(out, gin.H{"name": m.Name, "roles": m.Roles, "bio": m.Bio})
		}
	}
	ok(c, out)
}

func (h *API) myAppointments(c *gin.Context) {
	p := Patient(c)
	list, err := h.appts.OfPatient(c.Request.Context(), p.TenantID, p.ID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	list = visibleAppts(list)
	out := make([]gin.H, 0, len(list))
	for _, a := range list {
		out = append(out, apptJSON(a, h.appts.Position(c.Request.Context(), p.TenantID, a.ID)))
	}
	ok(c, out)
}

func apptJSON(a appointment.Appointment, pos int) gin.H {
	items := make([]gin.H, 0, len(a.Items))
	for _, it := range a.Items {
		items = append(items, gin.H{"name": it.Name, "qty": it.Qty, "price": it.Price, "amount": it.Amount})
	}
	return gin.H{
		"id": a.ID.Hex(), "date": a.Date, "slot": a.Slot,
		"doctor": a.Doctor, "item": a.DisplayItem(), "items": items,
		"status": a.Status, "status_name": a.StatusName(),
		"queue_no": a.QueueNo, "queue_pos": pos,
	}
}

type bookReq struct {
	DoctorID string `json:"doctor_id"`
	Date     string `json:"date"`
	Slot     string `json:"slot"`
	Item     string `json:"item"`
	Lines    []struct {
		ServiceID string  `json:"service_id"`
		Qty       float64 `json:"qty"`
	} `json:"lines"`
}

func (h *API) createAppointment(c *gin.Context) {
	p := Patient(c)
	var in bookReq
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	docID, _ := bson.ObjectIDFromHex(in.DoctorID)
	a := &appointment.Appointment{
		PatientID: p.ID, DoctorID: docID,
		Date: in.Date, Slot: in.Slot, Item: in.Item,
	}
	slotMinutes, slotCapacity := appointment.DefaultSlotMinutes, appointment.DefaultSlotCapacity
	if t := mw.Tenant(c); t != nil {
		slotMinutes, slotCapacity = t.SlotConfig()
	}
	for _, l := range in.Lines {
		sid, err := bson.ObjectIDFromHex(l.ServiceID)
		if err != nil {
			continue
		}
		a.Items = append(a.Items, appointment.ApptItem{ServiceID: sid, Qty: l.Qty})
	}
	if err := h.appts.Create(c.Request.Context(), p.TenantID, a, slotMinutes, slotCapacity); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"id": a.ID.Hex()})
}

func (h *API) cancelAppointment(c *gin.Context) {
	p := Patient(c)
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	a, err := h.appts.ByID(c.Request.Context(), p.TenantID, id)
	if err != nil || a.PatientID != p.ID {
		fail(c, http.StatusNotFound, "预约不存在")
		return
	}
	if err := h.appts.Cancel(c.Request.Context(), p.TenantID, id); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, nil)
}
