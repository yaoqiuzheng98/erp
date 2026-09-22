package handler

import (
	"net/http"
	"time"

	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"
	"erp/internal/plugins/hr/model"
	"erp/internal/plugins/hr/service"

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
	g.GET("/employees", mw.RequirePerm("hr.read"), h.employees)
	g.POST("/employees", mw.RequirePerm("hr.write"), h.createEmployee)
	g.GET("/attends", mw.RequirePerm("hr.read"), h.attends)
	g.POST("/attends", mw.RequirePerm("hr.write"), h.createAttend)
	g.GET("/leaves", mw.RequirePerm("hr.read"), h.leaves)
	g.POST("/leaves", mw.RequirePerm("hr.write"), h.createLeave)
	g.POST("/leaves/:id/submit", mw.RequirePerm("hr.write"), h.submitLeave)
}

func (h *Handler) employees(c *gin.Context) {
	list, _ := h.svc.ListEmployees(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "hr/employees", gin.H{"Rows": list})
}

func (h *Handler) createEmployee(c *gin.Context) {
	hd, _ := time.Parse("2006-01-02", c.PostForm("hire_date"))
	em := &model.Employee{
		Code: c.PostForm("code"), Name: c.PostForm("name"),
		Dept: c.PostForm("dept"), Position: c.PostForm("position"),
		Phone: c.PostForm("phone"), HireDate: hd,
	}
	if err := h.svc.CreateEmployee(c.Request.Context(), mw.TenantID(c), em); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "员工已创建")
	}
	c.Redirect(http.StatusFound, "/app/hr/employees")
}

func (h *Handler) attends(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 30)
	list, total, err := h.svc.ListAttends(c.Request.Context(), mw.TenantID(c), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	emps, _ := h.svc.ListEmployees(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "hr/attends", gin.H{"Rows": list, "Pager": pager, "Emps": emps})
}

func (h *Handler) createAttend(c *gin.Context) {
	empID, _ := bson.ObjectIDFromHex(c.PostForm("employee_id"))
	a := &model.Attend{
		EmployeeID: empID, Date: c.PostForm("date"), Type: c.PostForm("type"),
	}
	if emps, err := h.svc.ListEmployees(c.Request.Context(), mw.TenantID(c)); err == nil {
		for _, em := range emps {
			if em.ID == empID {
				a.EmpName = em.Name
			}
		}
	}
	if err := h.svc.CreateAttend(c.Request.Context(), mw.TenantID(c), a); err != nil {
		web.SetFlash(c, "记录失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/hr/attends")
}

func (h *Handler) leaves(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 30)
	list, total, err := h.svc.ListLeaves(c.Request.Context(), mw.TenantID(c), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	emps, _ := h.svc.ListEmployees(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "hr/leaves", gin.H{
		"Rows": list, "Pager": pager, "Emps": emps,
		"FlowEnabled": h.e.Gate.IsEnabled(c.Request.Context(), mw.TenantID(c), "flow"),
	})
}

func (h *Handler) createLeave(c *gin.Context) {
	empID, _ := bson.ObjectIDFromHex(c.PostForm("employee_id"))
	l := &model.Leave{
		EmployeeID: empID, Type: c.PostForm("type"),
		From: c.PostForm("from"), To: c.PostForm("to"), Reason: c.PostForm("reason"),
	}
	if emps, err := h.svc.ListEmployees(c.Request.Context(), mw.TenantID(c)); err == nil {
		for _, em := range emps {
			if em.ID == empID {
				l.EmpName = em.Name
			}
		}
	}
	if err := h.svc.CreateLeave(c.Request.Context(), mw.TenantID(c), l, mw.User(c).Username); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "请假单已创建")
	}
	c.Redirect(http.StatusFound, "/app/hr/leaves")
}

func (h *Handler) submitLeave(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.SubmitLeave(c.Request.Context(), mw.TenantID(c), id, mw.User(c).Username); err != nil {
		web.SetFlash(c, "提交失败: "+err.Error())
	} else {
		web.SetFlash(c, "已提交")
	}
	c.Redirect(http.StatusFound, "/app/hr/leaves")
}
