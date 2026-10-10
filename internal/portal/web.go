package portal

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"erp/internal/dental/appointment"
	"erp/internal/dental/patient"
	"erp/internal/platform/auth"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/session"
	"erp/internal/platform/tz"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Web 患者端 H5 页面（服务端渲染，手机优先）。
type Web struct {
	e     *env.Env
	pats  *patient.Service
	appts *appointment.Service
}

func NewWeb(e *env.Env, pats *patient.Service, appts *appointment.Service) *Web {
	return &Web{e: e, pats: pats, appts: appts}
}

func (h *Web) Register(g *gin.RouterGroup) {
	g.GET("/login", h.loginPage)
	g.POST("/login", h.login)
	g.POST("/logout", h.logout)
	g.GET("", h.requirePatient, h.home)
	g.GET("/", h.requirePatient, h.home)
	g.GET("/gallery/:id", h.gallery)
	g.GET("/book", h.requirePatient, h.bookPage)
	g.POST("/book", h.requirePatient, h.book)
	g.GET("/my", h.requirePatient, h.my)
	g.GET("/appointments", h.requirePatient, h.apptsPage)
	g.POST("/appointments/:id/cancel", h.requirePatient, h.cancel)
}

func (h *Web) requirePatient(c *gin.Context) {
	if Patient(c) == nil {
		c.Redirect(http.StatusFound, "/p/"+c.Param("tid")+"/login")
		c.Abort()
		return
	}
	c.Next()
}

func (h *Web) tenant(c *gin.Context) string { return c.Param("tid") }

func (h *Web) clinic(c *gin.Context) string {
	if t := mw.Tenant(c); t != nil {
		return t.Name
	}
	return ""
}

func (h *Web) loginPage(c *gin.Context) {
	if Patient(c) != nil {
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/")
		return
	}
	web.Render(c, h.e, "portal/login", gin.H{"Tid": h.tenant(c), "Clinic": h.clinic(c)})
}

func (h *Web) login(c *gin.Context) {
	p, err := h.pats.Login(c.Request.Context(), mw.TenantID(c), c.PostForm("phone"), c.PostForm("password"))
	if err != nil {
		web.Render(c, h.e, "portal/login", gin.H{"Tid": h.tenant(c), "Clinic": h.clinic(c), "Err": "手机号或密码错误"})
		return
	}
	s, err := h.e.Sessions.Create(c.Request.Context(), session.KindPatient, p.ID, p.TenantID)
	if err != nil {
		web.Render(c, h.e, "portal/login", gin.H{"Tid": h.tenant(c), "Clinic": h.clinic(c), "Err": "会话创建失败"})
		return
	}
	c.SetCookie(PatCookie, s.ID, int(h.e.Cfg.Session.TTLHours)*3600, "/", "", h.e.Cfg.Session.Secure, true)
	c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/")
}

func (h *Web) logout(c *gin.Context) {
	if token, err := c.Cookie(PatCookie); err == nil {
		h.e.Sessions.Destroy(c.Request.Context(), token)
	}
	c.SetCookie(PatCookie, "", -1, "/", "", h.e.Cfg.Session.Secure, true)
	c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/login")
}

// teamMember 首页团队成员（与 home 模板字段对应）。
type teamMember struct {
	Name   string
	Roles  string
	Bio    string
	Avatar string
	Order  int
}

// teamMembers 首页可见团队：全部在职、有角色、未隐藏的成员，按排序号展示。
// gallery 公开图接口复用同一名单做头像白名单。SSR 模板与 JSON API 共用。
func teamMembers(ctx context.Context, e *env.Env, tid bson.ObjectID) []teamMember {
	users, _ := e.Auth.List(ctx, tid)
	roles, _ := e.RBAC.List(ctx, tid)
	roleName := map[string]string{}
	for _, r := range roles {
		roleName[r.ID.Hex()] = r.Name
	}
	var team []teamMember
	for _, u := range users {
		// 无角色的纯管理账号不上首页
		if u.Status != "active" || u.HideHome || len(u.RoleIDs) == 0 {
			continue
		}
		var rn []string
		for _, rid := range u.RoleIDs {
			if n := roleName[rid.Hex()]; n != "" {
				rn = append(rn, n)
			}
		}
		team = append(team, teamMember{Name: u.Name, Roles: strings.Join(rn, "·"), Bio: u.Bio, Avatar: u.Avatar, Order: u.HomeOrder})
	}
	auth.SortByWeight(team,
		func(m teamMember) int { return m.Order },
		func(m teamMember) string { return m.Name })
	return team
}

func (h *Web) home(c *gin.Context) {
	p := Patient(c)
	data := gin.H{"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "home", "Name": p.Name}
	if t := mw.Tenant(c); t != nil {
		data["Intro"], data["Address"] = t.Intro, t.Address
		data["Phone"], data["Hours"] = t.Phone, t.Hours
		data["Notice"], data["Gallery"] = t.Notice, t.Gallery
	}
	// 首页团队：全部在职、有角色、未隐藏的成员，按排序号展示
	data["Team"] = teamMembers(c.Request.Context(), h.e, p.TenantID)
	web.Render(c, h.e, "portal/home", data)
}

// gallery 首页公开图片（无需患者登录）：门诊图库 + 首页可见成员的大头照。
// 白名单制：不在名单里的 ID 一律 404，防止用 ID 猜解患者影像。
func (h *Web) gallery(c *gin.Context) {
	t := mw.Tenant(c)
	fid := c.Param("id")
	allowed := false
	if t != nil {
		for _, g := range t.Gallery {
			if g == fid {
				allowed = true
				break
			}
		}
		if !allowed {
			for _, m := range teamMembers(c.Request.Context(), h.e, t.ID) {
				if m.Avatar != "" && m.Avatar == fid {
					allowed = true
					break
				}
			}
		}
	}
	if !allowed {
		c.Status(http.StatusNotFound)
		return
	}
	id, err := bson.ObjectIDFromHex(fid)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	a, stream, err := h.e.Attach.Open(c.Request.Context(), t.ID, id)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer stream.Close()
	mime := a.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Header("Content-Disposition", "inline; filename=\""+a.Filename+"\"")
	c.DataFromReader(http.StatusOK, a.Size, mime, stream, nil)
}

func (h *Web) apptsPage(c *gin.Context) {
	p := Patient(c)
	ctx := c.Request.Context()
	appts, _ := h.appts.OfPatient(ctx, p.TenantID, p.ID)
	appts = visibleAppts(appts)
	web.Render(c, h.e, "portal/appts", gin.H{
		"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "home", "Name": p.Name,
		"Appts": appts,
	})
}

func (h *Web) bookPage(c *gin.Context) {
	doctors, _ := h.e.Auth.ListPractitioners(c.Request.Context(), mw.TenantID(c))
	slotMinutes := appointment.DefaultSlotMinutes
	if t := mw.Tenant(c); t != nil {
		slotMinutes, _ = t.SlotConfig()
	}
	web.Render(c, h.e, "portal/book", gin.H{
		"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "book", "Doctors": doctors,
		"Today": tz.Today(), "Slots": appointment.DaySlots(slotMinutes),
	})
}

func (h *Web) book(c *gin.Context) {
	p := Patient(c)
	// 患者自助约诊不收过去日期（后台补录走门诊端，那边不限）
	today, _ := tz.DayStart(tz.Today())
	if d, ok := tz.DayStart(c.PostForm("date")); !ok || d.Before(today) {
		web.SetFlash(c, "预约日期不能早于今天")
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/book")
		return
	}
	docID, _ := bson.ObjectIDFromHex(c.PostForm("doctor_id"))
	a := &appointment.Appointment{
		PatientID: p.ID, DoctorID: docID,
		Date: c.PostForm("date"), Slot: c.PostForm("slot"), Item: c.PostForm("item"),
	}
	slotMinutes, slotCapacity := appointment.DefaultSlotMinutes, appointment.DefaultSlotCapacity
	if t := mw.Tenant(c); t != nil {
		slotMinutes, slotCapacity = t.SlotConfig()
	}
	if err := h.appts.Create(c.Request.Context(), p.TenantID, a, slotMinutes, slotCapacity); err != nil {
		slog.Error("portal book failed", "tenant", p.TenantID.Hex(), "patient", p.ID.Hex(),
			"doctor", a.DoctorID.Hex(), "date", a.Date, "slot", a.Slot, "err", err)
		web.SetFlash(c, "预约失败: "+err.Error())
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/book")
		return
	}
	web.SetFlash(c, "预约成功，请按时到诊")
	c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/appointments")
}

func (h *Web) cancel(c *gin.Context) {
	p := Patient(c)
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	a, err := h.appts.ByID(c.Request.Context(), p.TenantID, id)
	if err != nil || a.PatientID != p.ID {
		web.SetFlash(c, "预约不存在")
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/appointments")
		return
	}
	if err := h.appts.Cancel(c.Request.Context(), p.TenantID, id); err != nil {
		web.SetFlash(c, "取消失败: "+err.Error())
	} else {
		web.SetFlash(c, "预约已取消")
	}
	c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/appointments")
}

func (h *Web) my(c *gin.Context) {
	p := Patient(c)
	web.Render(c, h.e, "portal/my", gin.H{
		"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "my", "Name": p.Name, "Phone": p.Phone,
	})
}
