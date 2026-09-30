package handler

import (
	"net/http"
	"strconv"

	"erp/internal/dental/catalog"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (h *Handler) registerCatalog(g *gin.RouterGroup) {
	g.GET("/services", mw.RequirePerm("catalog.read"), h.services)
	g.POST("/services", mw.RequirePerm("catalog.write"), h.createService)
	g.POST("/services/:id", mw.RequirePerm("catalog.write"), h.updateService)
	g.POST("/services/:id/delete", mw.RequirePerm("catalog.write"), h.deleteService)
}

func (h *Handler) services(c *gin.Context) {
	list, err := h.items.List(c.Request.Context(), mw.TenantID(c), false)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	web.Render(c, h.e, "dental/services", gin.H{
		"Rows":  list,
		"Units": catalog.UnitOptions, "Categories": catalog.CategoryOptions,
	})
}

func (h *Handler) createService(c *gin.Context) {
	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)
	it := &catalog.ServiceItem{
		Name:     c.PostForm("name"),
		Category: c.PostForm("category"), Unit: c.PostForm("unit"),
		Price: price, Status: "active",
	}
	if err := h.items.Create(c.Request.Context(), mw.TenantID(c), it); err != nil {
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
	if err := h.items.Update(c.Request.Context(), mw.TenantID(c), id, set); err != nil {
		web.SetFlash(c, "更新失败: "+err.Error())
	} else {
		web.SetFlash(c, "价目已更新")
	}
	c.Redirect(http.StatusFound, "/app/services")
}

func (h *Handler) deleteService(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.items.Delete(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "删除失败: "+err.Error())
	} else {
		web.SetFlash(c, "价目已删除")
	}
	c.Redirect(http.StatusFound, "/app/services")
}
