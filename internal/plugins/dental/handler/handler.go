package handler

import (
	"net/http"
	"strconv"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"
	"erp/internal/plugins/dental/model"
	"erp/internal/plugins/dental/service"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Handler struct {
	e   *env.Env
	svc *service.Service
}

func New(e *env.Env) *Handler {
	return &Handler{e: e, svc: service.New(e)}
}

func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("", mw.RequirePerm("dental.read"), h.today)
	g.GET("/patients", mw.RequirePerm("dental.read"), h.patients)
	g.POST("/patients", mw.RequirePerm("dental.write"), h.createPatient)
	g.GET("/patients/:id", mw.RequirePerm("dental.read"), h.patient)
	g.POST("/patients/:id/tooth", mw.RequirePerm("dental.write"), h.setTooth)
	g.GET("/appointments", mw.RequirePerm("dental.read"), h.appointments)
	g.POST("/appointments", mw.RequirePerm("dental.write"), h.createAppt)
	g.POST("/appointments/:id/arrive", mw.RequirePerm("dental.write"), h.arrive)
	g.POST("/appointments/:id/done", mw.RequirePerm("dental.write"), h.done)
	g.POST("/appointments/:id/noshow", mw.RequirePerm("dental.write"), h.noshow)
	g.POST("/appointments/:id/cancel", mw.RequirePerm("dental.write"), h.cancel)
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
	data := gin.H{"Rows": list, "Pager": pager, "Q": q}
	// 已有客户列表供"挂接已有客户"选择
	if master, err := contract.Master(h.e); err == nil {
		if custs, err := master.Customers(c.Request.Context(), tenantID); err == nil {
			data["Customers"] = custs
		}
	}
	web.Render(c, h.e, "dental/patients", data)
}

func (h *Handler) createPatient(c *gin.Context) {
	p := &model.Patient{
		Gender: c.PostForm("gender"), Birth: c.PostForm("birth"),
		Allergy: c.PostForm("allergy"), History: c.PostForm("history"),
	}
	custID, _ := bson.ObjectIDFromHex(c.PostForm("customer_id"))
	if err := h.svc.CreatePatient(c.Request.Context(), mw.TenantID(c), p,
		c.PostForm("name"), c.PostForm("phone"), custID); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "患者已建档: "+p.Code)
	}
	c.Redirect(http.StatusFound, "/app/dental/patients")
}

func (h *Handler) patient(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	p, err := h.svc.PatientView(c.Request.Context(), mw.TenantID(c), id)
	if err != nil {
		c.String(http.StatusNotFound, "患者不存在")
		return
	}
	appts, _ := h.svc.ApptsOfPatient(c.Request.Context(), mw.TenantID(c), id)
	web.Render(c, h.e, "dental/patient", gin.H{
		"P": p, "Appts": appts,
		"Quadrants": quadrants(), "ToothNames": model.ToothStatuses,
	})
}

// quadrants FDI 四象限，按牙位图显示顺序：
// 上排 右上18..11 | 左上21..28；下排 右下48..41 | 左下31..38。
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
		if _, ok := model.ToothStatuses[status]; !ok {
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
	web.Render(c, h.e, "dental/appointments", gin.H{
		"Rows": list, "Pager": pager, "Date": date, "Patients": pats,
	})
}

func (h *Handler) createAppt(c *gin.Context) {
	patID, _ := bson.ObjectIDFromHex(c.PostForm("patient_id"))
	a := &model.Appointment{
		PatientID: patID,
		Doctor:    c.PostForm("doctor"), Chair: c.PostForm("chair"),
		Date: c.PostForm("date"), Slot: c.PostForm("slot"), Item: c.PostForm("item"),
	}
	if err := h.svc.CreateAppt(c.Request.Context(), mw.TenantID(c), a); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "已预约")
	}
	c.Redirect(http.StatusFound, "/app/dental/appointments")
}

func (h *Handler) arrive(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Arrive(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/dental/appointments")
}

func (h *Handler) done(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	charge, _ := strconv.ParseFloat(c.PostForm("charge"), 64)
	if err := h.svc.Complete(c.Request.Context(), mw.TenantID(c), id, charge, mw.User(c).Username); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	} else {
		web.SetFlash(c, "已完成就诊")
	}
	c.Redirect(http.StatusFound, "/app/dental/appointments")
}

func (h *Handler) noshow(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.NoShow(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/dental/appointments")
}

func (h *Handler) cancel(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Cancel(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/dental/appointments")
}
