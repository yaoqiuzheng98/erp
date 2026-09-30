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
	staff *staff.Service
	bill  *billing.Service
}

func NewAPI(e *env.Env, guard *Guard, pats *patient.Service, appts *appointment.Service,
	items *catalog.Service, staff *staff.Service, bill *billing.Service) *API {
	return &API{e: e, guard: guard, pats: pats, appts: appts, items: items, staff: staff, bill: bill}
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": data})
}

func fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"code": 1, "msg": msg})
}

func (h *API) Register(g *gin.RouterGroup) {
	g.POST("/login", h.guard.RateLimit(20), h.login)
	g.POST("/logout", h.logout)
	g.GET("/services", h.services)
	g.GET("/appointments", h.requirePatient, h.myAppointments)
	g.POST("/appointments", h.requirePatient, h.createAppointment)
	g.POST("/appointments/:id/cancel", h.requirePatient, h.cancelAppointment)
	g.POST("/appointments/:id/pay", h.requirePatient, h.payAppointment)
	g.GET("/bills", h.requirePatient, h.myBills)
	g.GET("/bills/:id", h.requirePatient, h.billDetail)
	g.POST("/bills/:id/pay", h.requirePatient, h.payBill)
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
	s, err := h.e.Sessions.Create(c.Request.Context(), session.KindPatient, p.ID, p.TenantID)
	if err != nil {
		fail(c, http.StatusInternalServerError, "会话创建失败")
		return
	}
	c.SetCookie(PatCookie, s.ID, int(h.e.Cfg.Session.TTLHours)*3600, "/", "", h.e.Cfg.Session.Secure, true)
	ok(c, gin.H{"token": s.ID, "name": p.Name})
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

func (h *API) myAppointments(c *gin.Context) {
	p := Patient(c)
	list, err := h.appts.OfPatient(c.Request.Context(), p.TenantID, p.ID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
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
		"charge": a.Charge, "queue_no": a.QueueNo, "queue_pos": pos,
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
	if t := mw.Tenant(c); t != nil {
		a.RegFee = t.RegFee
	}
	for _, l := range in.Lines {
		sid, err := bson.ObjectIDFromHex(l.ServiceID)
		if err != nil {
			continue
		}
		a.Items = append(a.Items, appointment.ApptItem{ServiceID: sid, Qty: l.Qty})
	}
	if err := h.appts.Create(c.Request.Context(), p.TenantID, a); err != nil {
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

func (h *API) payAppointment(c *gin.Context) {
	p := Patient(c)
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	a, err := h.appts.ByID(c.Request.Context(), p.TenantID, id)
	if err != nil || a.PatientID != p.ID {
		fail(c, http.StatusNotFound, "预约不存在")
		return
	}
	if err := h.appts.PayReg(c.Request.Context(), p.TenantID, id, p.Name); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"reg_paid": true})
}

func (h *API) ownBill(c *gin.Context) (*billing.Bill, bool) {
	p := Patient(c)
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	list, _ := h.bill.Mine(c.Request.Context(), p.TenantID, p.ID)
	for _, b := range list {
		if b.ID == id {
			return &b, true
		}
	}
	return nil, false
}

func (h *API) billDetail(c *gin.Context) {
	b, found := h.ownBill(c)
	if !found {
		fail(c, http.StatusNotFound, "账单不存在")
		return
	}
	lines := make([]gin.H, 0, len(b.Lines))
	for _, l := range b.Lines {
		lines = append(lines, gin.H{"name": l.Name, "qty": l.Qty, "price": l.Price, "amount": l.Amount})
	}
	ok(c, gin.H{
		"doc_no": b.DocNo, "amount": b.Amount, "paid": b.PaidAmount,
		"status": b.Status, "lines": lines,
	})
}

func (h *API) payBill(c *gin.Context) {
	p := Patient(c)
	b, found := h.ownBill(c)
	if !found {
		fail(c, http.StatusNotFound, "账单不存在")
		return
	}
	if err := h.bill.PayMock(c.Request.Context(), p.TenantID, b.ID, p.Name); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"paid": true})
}

func (h *API) myBills(c *gin.Context) {
	p := Patient(c)
	list, err := h.bill.Mine(c.Request.Context(), p.TenantID, p.ID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, b := range list {
		out = append(out, gin.H{
			"doc_no": b.DocNo, "amount": b.Amount, "paid": b.PaidAmount,
			"status": b.Status, "unpaid": strconv.FormatFloat(b.Amount-b.PaidAmount, 'f', 2, 64),
		})
	}
	ok(c, out)
}
