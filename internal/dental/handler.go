package dental

import (
	"net/http"
	"strconv"

	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"
	"erp/internal/platform/env"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Handler struct {
	e   *env.Env
	svc *Service
}

func NewHandler(e *env.Env, svc *Service) *Handler {
	return &Handler{e: e, svc: svc}
}

func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("", mw.RequirePerm("appt.read"), h.today)
	g.GET("/patients", mw.RequirePerm("patient.read"), h.patients)
	g.POST("/patients", mw.RequirePerm("patient.write"), h.createPatient)
	g.GET("/patients/:id", mw.RequirePerm("patient.read"), h.patient)
	g.POST("/patients/:id/tooth", mw.RequirePerm("patient.write"), h.setTooth)
	g.GET("/appointments", mw.RequirePerm("appt.read"), h.appointments)
	g.POST("/appointments", mw.RequirePerm("appt.write"), h.createAppt)
	g.POST("/appointments/:id/arrive", mw.RequirePerm("appt.write"), h.arrive)
	g.POST("/appointments/:id/done", mw.RequirePerm("appt.write"), h.done)
	g.POST("/appointments/:id/noshow", mw.RequirePerm("appt.write"), h.noshow)
	g.POST("/appointments/:id/cancel", mw.RequirePerm("appt.write"), h.cancel)
	g.GET("/services", mw.RequirePerm("catalog.read"), h.services)
	g.POST("/services", mw.RequirePerm("catalog.write"), h.createService)
	g.POST("/services/:id", mw.RequirePerm("catalog.write"), h.updateService)
	g.POST("/services/:id/delete", mw.RequirePerm("catalog.write"), h.deleteService)
	g.GET("/staff", mw.RequirePerm("staff.read"), h.staff)
	g.POST("/staff", mw.RequirePerm("staff.write"), h.createStaff)
	g.POST("/staff/:id", mw.RequirePerm("staff.write"), h.updateStaff)
	g.POST("/staff/:id/delete", mw.RequirePerm("staff.write"), h.deleteStaff)
}

// today 今日预约工作台。
func (h *Handler) today(c *gin.Context) {
	list, _ := h.svc.TodayAppts(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "dental/index", gin.H{"Rows": list})
}

func (h *Handler) patients(c *gin.Context) {
	tenantID := mw.TenantID(c)
	skip, limit, pager := web.ParsePager(c, 30)
	q := c.Query("q")
	list, total, err := h.svc.ListPatients(c.Request.Context(), tenantID, q, skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "dental/patients", gin.H{"Rows": list, "Pager": pager, "Q": q})
}

func (h *Handler) createPatient(c *gin.Context) {
	p := &Patient{
		Name: c.PostForm("name"), Phone: c.PostForm("phone"),
		Gender: c.PostForm("gender"), Birth: c.PostForm("birth"),
		Allergy: c.PostForm("allergy"), History: c.PostForm("history"),
	}
	if err := h.svc.CreatePatient(c.Request.Context(), mw.TenantID(c), p); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "患者已建档")
	}
	c.Redirect(http.StatusFound, "/app/patients")
}

func (h *Handler) patient(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	p, err := h.svc.PatientByID(c.Request.Context(), mw.TenantID(c), id)
	if err != nil {
		c.String(http.StatusNotFound, "患者不存在")
		return
	}
	appts, _ := h.svc.ApptsOfPatient(c.Request.Context(), mw.TenantID(c), id)
	web.Render(c, h.e, "dental/patient", gin.H{
		"P": p, "Appts": appts,
		"Quadrants": quadrants(), "ToothNames": ToothStatuses,
	})
}

// quadrants FDI 四象限，按牙位图显示顺序。
func quadrants() [][]string {
	return [][]string{
		{"18", "17", "16", "15", "14", "13", "12", "11"},
		{"21", "22", "23", "24", "25", "26", "27", "28"},
		{"48", "47", "46", "45", "44", "43", "42", "41"},
		{"31", "32", "33", "34", "35", "36", "37", "38"},
	}
}

// setTooth 牙位状态更新（fetch POST）。
func (h *Handler) setTooth(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	tooth, status := c.PostForm("tooth"), c.PostForm("status")
	if len(tooth) != 2 || tooth[0] < '1' || tooth[0] > '4' || tooth[1] < '1' || tooth[1] > '8' {
		c.String(http.StatusBadRequest, "牙位无效")
		return
	}
	if status != "" {
		if _, ok := ToothStatuses[status]; !ok {
			c.String(http.StatusBadRequest, "状态无效")
			return
		}
	}
	if err := h.svc.SetTooth(c.Request.Context(), mw.TenantID(c), id, tooth, status); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) appointments(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 30)
	date := c.Query("date")
	list, total, err := h.svc.ListAppts(c.Request.Context(), mw.TenantID(c), date, skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	pats, _, _ := h.svc.ListPatients(c.Request.Context(), mw.TenantID(c), "", 0, 500)
	items, _ := h.svc.ListServiceItems(c.Request.Context(), mw.TenantID(c), true)
	doctors, _ := h.svc.ListDoctors(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "dental/appointments", gin.H{
		"Rows": list, "Pager": pager, "Date": date,
		"Patients": pats, "Services": items, "Doctors": doctors,
	})
}

// parseApptItems 解析明细表单：service_id[] 多选 + qty_<hex> 数量。
func parseApptItems(c *gin.Context) []ApptItem {
	ids := c.PostFormArray("service_id")
	qtys := c.PostFormArray("qty")
	var out []ApptItem
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
		out = append(out, ApptItem{ServiceID: oid, Qty: qty})
	}
	return out
}

func (h *Handler) createAppt(c *gin.Context) {
	patID, _ := bson.ObjectIDFromHex(c.PostForm("patient_id"))
	docID, _ := bson.ObjectIDFromHex(c.PostForm("doctor_id"))
	a := &Appointment{
		PatientID: patID, DoctorID: docID,
		Chair: c.PostForm("chair"),
		Date:  c.PostForm("date"), Slot: c.PostForm("slot"),
		Item:  c.PostForm("item"),
		Items: parseApptItems(c),
	}
	if err := h.svc.CreateAppt(c.Request.Context(), mw.TenantID(c), a); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "已预约")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) arrive(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Arrive(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) done(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	charge, _ := strconv.ParseFloat(c.PostForm("charge"), 64)
	items := parseApptItems(c)
	if err := h.svc.Complete(c.Request.Context(), mw.TenantID(c), id, items, charge, mw.User(c).Username); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	} else {
		web.SetFlash(c, "已完成就诊")
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) noshow(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.NoShow(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

func (h *Handler) cancel(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Cancel(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/appointments")
}

// ---------- 价目表 ----------

func (h *Handler) services(c *gin.Context) {
	list, err := h.svc.ListServiceItems(c.Request.Context(), mw.TenantID(c), false)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	web.Render(c, h.e, "dental/services", gin.H{"Rows": list})
}

func (h *Handler) createService(c *gin.Context) {
	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)
	it := &ServiceItem{
		Name: c.PostForm("name"),
		Category: c.PostForm("category"), Unit: c.PostForm("unit"),
		Price: price, Status: "active",
	}
	if err := h.svc.CreateServiceItem(c.Request.Context(), mw.TenantID(c), it); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "价目已创建: "+it.Name)
	}
	c.Redirect(http.StatusFound, "/app/services")
}

func (h *Handler) updateService(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)
	set := bson.M{
		"name": c.PostForm("name"), "category": c.PostForm("category"),
		"unit": c.PostForm("unit"), "price": price, "status": c.PostForm("status"),
	}
	if set["status"] != "active" && set["status"] != "disabled" {
		set["status"] = "active"
	}
	if err := h.svc.UpdateServiceItem(c.Request.Context(), mw.TenantID(c), id, set); err != nil {
		web.SetFlash(c, "更新失败: "+err.Error())
	} else {
		web.SetFlash(c, "价目已更新")
	}
	c.Redirect(http.StatusFound, "/app/services")
}

func (h *Handler) deleteService(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.DeleteServiceItem(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "删除失败: "+err.Error())
	} else {
		web.SetFlash(c, "价目已删除")
	}
	c.Redirect(http.StatusFound, "/app/services")
}

// ---------- 员工 ----------

func (h *Handler) staff(c *gin.Context) {
	list, err := h.svc.ListStaff(c.Request.Context(), mw.TenantID(c), false)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	web.Render(c, h.e, "dental/staff", gin.H{"Rows": list, "Roles": StaffRoles})
}

func (h *Handler) createStaff(c *gin.Context) {
	st := &Staff{
		Name: c.PostForm("name"), Role: c.PostForm("role"),
		Phone: c.PostForm("phone"), Status: "active",
	}
	if err := h.svc.CreateStaff(c.Request.Context(), mw.TenantID(c), st); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "员工已创建: "+st.Name)
	}
	c.Redirect(http.StatusFound, "/app/staff")
}

func (h *Handler) updateStaff(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	set := bson.M{
		"name": c.PostForm("name"), "role": c.PostForm("role"),
		"phone": c.PostForm("phone"), "status": c.PostForm("status"),
	}
	if set["status"] != "active" && set["status"] != "disabled" {
		set["status"] = "active"
	}
	if err := h.svc.UpdateStaff(c.Request.Context(), mw.TenantID(c), id, set); err != nil {
		web.SetFlash(c, "更新失败: "+err.Error())
	} else {
		web.SetFlash(c, "员工已更新")
	}
	c.Redirect(http.StatusFound, "/app/staff")
}

func (h *Handler) deleteStaff(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.DeleteStaff(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "删除失败: "+err.Error())
	} else {
		web.SetFlash(c, "员工已删除")
	}
	c.Redirect(http.StatusFound, "/app/staff")
}
