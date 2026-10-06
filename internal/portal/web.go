package portal

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"erp/internal/billing"
	"erp/internal/dental/appointment"
	"erp/internal/dental/catalog"
	"erp/internal/dental/patient"
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
	items *catalog.Service
	bill  *billing.Service
}

func NewWeb(e *env.Env, pats *patient.Service, appts *appointment.Service,
	items *catalog.Service, bill *billing.Service) *Web {
	return &Web{e: e, pats: pats, appts: appts, items: items, bill: bill}
}

func (h *Web) Register(g *gin.RouterGroup) {
	g.GET("/login", h.loginPage)
	g.POST("/login", h.login)
	g.POST("/logout", h.logout)
	g.GET("", h.requirePatient, h.home)
	g.GET("/", h.requirePatient, h.home)
	g.GET("/services", h.services)
	g.GET("/gallery/:id", h.gallery)
	g.GET("/book", h.requirePatient, h.bookPage)
	g.POST("/book", h.requirePatient, h.book)
	g.GET("/pay/:id", h.requirePatient, h.payPage)
	g.POST("/pay/:id", h.requirePatient, h.pay)
	g.GET("/my", h.requirePatient, h.my)
	g.GET("/appointments", h.requirePatient, h.apptsPage)
	g.GET("/bills", h.requirePatient, h.billsPage)
	g.POST("/appointments/:id/cancel", h.requirePatient, h.cancel)
	g.GET("/bills/:id", h.requirePatient, h.billPage)
	g.POST("/bills/:id/pay", h.requirePatient, h.payBill)
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

func (h *Web) home(c *gin.Context) {
	p := Patient(c)
	data := gin.H{"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "home", "Name": p.Name}
	if t := mw.Tenant(c); t != nil {
		data["Intro"], data["Address"] = t.Intro, t.Address
		data["Phone"], data["Hours"] = t.Phone, t.Hours
		data["Notice"], data["Gallery"] = t.Notice, t.Gallery
	}
	// 首页团队：全部在职、有角色、未隐藏的成员，按排序号展示
	data["Team"] = h.teamMembers(c.Request.Context(), p.TenantID)
	web.Render(c, h.e, "portal/home", data)
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
// gallery 公开图接口复用同一名单做头像白名单。
func (h *Web) teamMembers(ctx context.Context, tid bson.ObjectID) []teamMember {
	users, _ := h.e.Auth.List(ctx, tid)
	roles, _ := h.e.RBAC.List(ctx, tid)
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
	sort.Slice(team, func(i, j int) bool {
		if team[i].Order != team[j].Order {
			return team[i].Order > team[j].Order
		}
		return team[i].Name < team[j].Name
	})
	return team
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
			for _, m := range h.teamMembers(c.Request.Context(), t.ID) {
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
	pos := map[string]int{}
	for _, a := range appts {
		pos[a.ID.Hex()] = h.appts.Position(ctx, p.TenantID, a.ID)
	}
	web.Render(c, h.e, "portal/appts", gin.H{
		"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "home", "Name": p.Name,
		"Appts": appts, "Pos": pos,
	})
}

func (h *Web) billsPage(c *gin.Context) {
	p := Patient(c)
	bills, _ := h.bill.Mine(c.Request.Context(), p.TenantID, p.ID)
	var unpaid float64
	for _, b := range bills {
		unpaid += b.Amount - b.PaidAmount
	}
	web.Render(c, h.e, "portal/bills", gin.H{
		"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "home", "Name": p.Name,
		"Bills": bills, "Unpaid": unpaid,
	})
}

func (h *Web) services(c *gin.Context) {
	list, _ := h.items.List(c.Request.Context(), mw.TenantID(c), true)
	web.Render(c, h.e, "portal/services", gin.H{"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "services", "Rows": list})
}

func (h *Web) bookPage(c *gin.Context) {
	doctors, _ := h.e.Auth.ListPractitioners(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "portal/book", gin.H{
		"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "book", "Doctors": doctors,
		"Today": tz.Today(),
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
		Items: parseItems(c),
	}
	if t := mw.Tenant(c); t != nil {
		a.RegFee = t.RegFee
	}
	if err := h.appts.Create(c.Request.Context(), p.TenantID, a); err != nil {
		slog.Error("portal book failed", "tenant", p.TenantID.Hex(), "patient", p.ID.Hex(),
			"doctor", a.DoctorID.Hex(), "date", a.Date, "slot", a.Slot,
			"items", len(a.Items), "err", err)
		web.SetFlash(c, "预约失败: "+err.Error())
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/book")
		return
	}
	if a.RegFee > 0 {
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/pay/"+a.ID.Hex())
		return
	}
	web.SetFlash(c, "预约成功，请按时到诊")
	c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/appointments")
}

func (h *Web) payPage(c *gin.Context) {
	p := Patient(c)
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	a, err := h.appts.ByID(c.Request.Context(), p.TenantID, id)
	if err != nil || a.PatientID != p.ID {
		c.String(http.StatusNotFound, "单据不存在")
		return
	}
	web.Render(c, h.e, "portal/pay", gin.H{"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "", "A": a})
}

func (h *Web) pay(c *gin.Context) {
	p := Patient(c)
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	a, err := h.appts.ByID(c.Request.Context(), p.TenantID, id)
	if err != nil || a.PatientID != p.ID {
		web.SetFlash(c, "单据不存在")
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/appointments")
		return
	}
	if err := h.appts.PayReg(c.Request.Context(), p.TenantID, id, p.Name); err != nil {
		web.SetFlash(c, "支付失败: "+err.Error())
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/pay/"+id.Hex())
		return
	}
	web.SetFlash(c, "支付成功，请按时到诊")
	c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/appointments")
}

// parseItems 解析 service_id[] + qty_<hex>（与门诊后台同款）。
func parseItems(c *gin.Context) []appointment.ApptItem {
	ids := c.PostFormArray("service_id")
	var out []appointment.ApptItem
	for _, sid := range ids {
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
		}
		out = append(out, appointment.ApptItem{ServiceID: oid, Qty: qty})
	}
	return out
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

func (h *Web) billPage(c *gin.Context) {
	p := Patient(c)
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	list, _ := h.bill.Mine(c.Request.Context(), p.TenantID, p.ID)
	for _, b := range list {
		if b.ID == id {
			var appt *appointment.Appointment
			if b.RefID != "" {
				if aid, err := bson.ObjectIDFromHex(b.RefID); err == nil {
					appt, _ = h.appts.ByID(c.Request.Context(), p.TenantID, aid)
				}
			}
			web.Render(c, h.e, "portal/bill", gin.H{"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "home", "B": b, "A": appt})
			return
		}
	}
	c.String(http.StatusNotFound, "诊疗单不存在")
}

func (h *Web) payBill(c *gin.Context) {
	p := Patient(c)
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	mine, _ := h.bill.Mine(c.Request.Context(), p.TenantID, p.ID)
	found := false
	for _, b := range mine {
		if b.ID == id {
			found = true
		}
	}
	if !found {
		web.SetFlash(c, "诊疗单不存在")
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/bills")
		return
	}
	if err := h.bill.PayMock(c.Request.Context(), p.TenantID, id, p.Name); err != nil {
		web.SetFlash(c, "支付失败: "+err.Error())
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/bills/"+id.Hex())
		return
	}
	web.SetFlash(c, "支付成功")
	c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/bills")
}

func (h *Web) my(c *gin.Context) {
	p := Patient(c)
	web.Render(c, h.e, "portal/my", gin.H{
		"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "my", "Name": p.Name, "Phone": p.Phone,
	})
}
