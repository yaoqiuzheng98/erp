package handler

import (
	"net/http"

	"erp/internal/dental/staff"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (h *Handler) registerStaff(g *gin.RouterGroup) {
	g.GET("/staff", mw.RequirePerm("staff.read"), h.staffPage)
	g.GET("/staff/roles", mw.RequirePerm("staff.read"), h.staffRolesPage)
	g.POST("/staff", mw.RequirePerm("staff.write"), h.createStaff)
	g.POST("/staff/:id", mw.RequirePerm("staff.write"), h.updateStaff)
	g.POST("/staff/:id/delete", mw.RequirePerm("staff.write"), h.deleteStaff)
	g.POST("/staff/roles", mw.RequirePerm("staff.write"), h.createRole)
	g.POST("/staff/roles/:id", mw.RequirePerm("staff.write"), h.updateRole)
	g.POST("/staff/roles/:id/delete", mw.RequirePerm("staff.write"), h.deleteRole)
}

func (h *Handler) staffPage(c *gin.Context) {
	tid := mw.TenantID(c)
	list, err := h.staff.List(c.Request.Context(), tid, false)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	roles, _ := h.staff.ListRoles(c.Request.Context(), tid)
	web.Render(c, h.e, "dental/staff", gin.H{"Rows": list, "Roles": roles})
}

func (h *Handler) staffRolesPage(c *gin.Context) {
	roles, err := h.staff.ListRoles(c.Request.Context(), mw.TenantID(c))
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	web.Render(c, h.e, "dental/staff_roles", gin.H{"Roles": roles})
}

func (h *Handler) createRole(c *gin.Context) {
	err := h.staff.CreateRole(c.Request.Context(), mw.TenantID(c),
		c.PostForm("name"), c.PostForm("can_practice") == "on")
	if err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "角色已创建")
	}
	c.Redirect(http.StatusFound, "/app/staff/roles")
}

func (h *Handler) updateRole(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	err := h.staff.SetRole(c.Request.Context(), mw.TenantID(c), id,
		c.PostForm("status"), c.PostForm("can_practice") == "on")
	if err != nil {
		web.SetFlash(c, "更新失败: "+err.Error())
	} else {
		web.SetFlash(c, "角色已更新")
	}
	c.Redirect(http.StatusFound, "/app/staff/roles")
}

func (h *Handler) deleteRole(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.staff.DeleteRole(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "删除失败: "+err.Error())
	} else {
		web.SetFlash(c, "角色已删除")
	}
	c.Redirect(http.StatusFound, "/app/staff/roles")
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
