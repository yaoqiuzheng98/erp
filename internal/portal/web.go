package portal

import (
	"net/http"
	"strconv"

	"erp/internal/billing"
	"erp/internal/dental/appointment"
	"erp/internal/dental/catalog"
	"erp/internal/dental/patient"
	"erp/internal/dental/staff"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/session"
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
	staff *staff.Service
	bill  *billing.Service
}

func NewWeb(e *env.Env, pats *patient.Service, appts *appointment.Service,
	items *catalog.Service, staff *staff.Service, bill *billing.Service) *Web {
	return &Web{e: e, pats: pats, appts: appts, items: items, staff: staff, bill: bill}
}

func (h *Web) Register(g *gin.RouterGroup) {
	g.GET("/login", h.loginPage)
	g.POST("/login", h.login)
	g.POST("/logout", h.logout)
	g.GET("", h.requirePatient, h.home)
	g.GET("/", h.requirePatient, h.home)
	g.GET("/services", h.services)
	g.GET("/book", h.requirePatient, h.bookPage)
	g.POST("/book", h.requirePatient, h.book)
	g.GET("/my", h.requirePatient, h.my)
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

func (h *Web) home(c *gin.Context) {
	p := Patient(c)
	web.Render(c, h.e, "portal/home", gin.H{"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "home", "Name": p.Name})
}

func (h *Web) services(c *gin.Context) {
	list, _ := h.items.List(c.Request.Context(), mw.TenantID(c), true)
	web.Render(c, h.e, "portal/services", gin.H{"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "services", "Rows": list})
}

func (h *Web) bookPage(c *gin.Context) {
	doctors, _ := h.staff.ListDoctors(c.Request.Context(), mw.TenantID(c))
	items, _ := h.items.List(c.Request.Context(), mw.TenantID(c), true)
	web.Render(c, h.e, "portal/book", gin.H{
		"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "book", "Doctors": doctors, "Services": items,
	})
}

func (h *Web) book(c *gin.Context) {
	p := Patient(c)
	docID, _ := bson.ObjectIDFromHex(c.PostForm("doctor_id"))
	a := &appointment.Appointment{
		PatientID: p.ID, DoctorID: docID,
		Date: c.PostForm("date"), Slot: c.PostForm("slot"), Item: c.PostForm("item"),
		Items: parseItems(c),
	}
	if err := h.appts.Create(c.Request.Context(), p.TenantID, a); err != nil {
		web.SetFlash(c, "预约失败: "+err.Error())
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/book")
		return
	}
	web.SetFlash(c, "预约成功，请按时到诊")
	c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/my")
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
		c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/my")
		return
	}
	if err := h.appts.Cancel(c.Request.Context(), p.TenantID, id); err != nil {
		web.SetFlash(c, "取消失败: "+err.Error())
	} else {
		web.SetFlash(c, "预约已取消")
	}
	c.Redirect(http.StatusFound, "/p/"+h.tenant(c)+"/my")
}

func (h *Web) my(c *gin.Context) {
	p := Patient(c)
	ctx := c.Request.Context()
	appts, _ := h.appts.OfPatient(ctx, p.TenantID, p.ID)
	bills, _ := h.bill.Mine(ctx, p.TenantID, p.ID)
	var unpaid float64
	for _, b := range bills {
		unpaid += b.Amount - b.PaidAmount
	}
	pos := map[string]int{}
	for _, a := range appts {
		pos[a.ID.Hex()] = h.appts.Position(ctx, p.TenantID, a.ID)
	}
	web.Render(c, h.e, "portal/my", gin.H{
		"Tid": h.tenant(c), "Clinic": h.clinic(c), "Tab": "my", "Name": p.Name,
		"Appts": appts, "Bills": bills, "Unpaid": unpaid, "Pos": pos,
	})
}
