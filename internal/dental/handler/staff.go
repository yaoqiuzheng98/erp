package handler

import (
	"net/http"

	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"
	"erp/internal/dental/staff"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (h *Handler) registerStaff(g *gin.RouterGroup) {
	g.GET("/staff", mw.RequirePerm("staff.read"), h.staffPage)
	g.POST("/staff", mw.RequirePerm("staff.write"), h.createStaff)
	g.POST("/staff/:id", mw.RequirePerm("staff.write"), h.updateStaff)
	g.POST("/staff/:id/delete", mw.RequirePerm("staff.write"), h.deleteStaff)
}

func (h *Handler) staffPage(c *gin.Context) {
	list, err := h.staff.List(c.Request.Context(), mw.TenantID(c), false)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	web.Render(c, h.e, "dental/staff", gin.H{"Rows": list, "Roles": staff.Roles})
}

func (h *Handler) createStaff(c *gin.Context) {
	st := &staff.Staff{
		Name: c.PostForm("name"), Role: c.PostForm("role"),
		Phone: c.PostForm("phone"), Status: "active",
	}
	if err := h.staff.Create(c.Request.Context(), mw.TenantID(c), st); err != nil {
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
	if err := h.staff.Update(c.Request.Context(), mw.TenantID(c), id, set); err != nil {
		web.SetFlash(c, "更新失败: "+err.Error())
	} else {
		web.SetFlash(c, "员工已更新")
	}
	c.Redirect(http.StatusFound, "/app/staff")
}

func (h *Handler) deleteStaff(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.staff.Delete(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "删除失败: "+err.Error())
	} else {
		web.SetFlash(c, "员工已删除")
	}
	c.Redirect(http.StatusFound, "/app/staff")
}
