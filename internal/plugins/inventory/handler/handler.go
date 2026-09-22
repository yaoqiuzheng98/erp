package handler

import (
	"net/http"
	"strconv"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"
	"erp/internal/plugins/inventory/model"
	"erp/internal/plugins/inventory/service"

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
	g.GET("/docs", mw.RequirePerm("inventory.doc.read"), h.docs)
	g.GET("/docs/new", mw.RequirePerm("inventory.doc.write"), h.newDoc)
	g.POST("/docs", mw.RequirePerm("inventory.doc.write"), h.createDoc)
	g.GET("/docs/:id", mw.RequirePerm("inventory.doc.read"), h.docDetail)
	g.POST("/docs/:id/confirm", mw.RequirePerm("inventory.doc.write"), h.confirmDoc)
	g.POST("/docs/:id/cancel", mw.RequirePerm("inventory.doc.write"), h.cancelDoc)
	g.GET("/balances", mw.RequirePerm("inventory.balance.read"), h.balances)
}

func (h *Handler) docs(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	list, total, err := h.svc.ListDocs(c.Request.Context(), mw.TenantID(c), c.Query("type"), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "inventory/docs", gin.H{"Docs": list, "Pager": pager, "Type": c.Query("type")})
}

func (h *Handler) newDoc(c *gin.Context) {
	master, err := contract.Master(h.e)
	if err != nil {
		web.SetFlash(c, "基础资料插件未启用")
		c.Redirect(http.StatusFound, "/app/inventory/docs")
		return
	}
	products, _ := master.Products(c.Request.Context(), mw.TenantID(c))
	warehouses, _ := master.Warehouses(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "inventory/doc_new", gin.H{
		"Products": products, "Warehouses": warehouses, "Type": c.Query("type"),
	})
}

func (h *Handler) createDoc(c *gin.Context) {
	d := &model.StockDoc{
		Type:   c.PostForm("type"),
		Remark: c.PostForm("remark"),
	}
	d.WarehouseID, _ = bson.ObjectIDFromHex(c.PostForm("warehouse_id"))
	productIDs := c.PostFormArray("product_id")
	qtys := c.PostFormArray("qty")
	for i, pid := range productIDs {
		if pid == "" {
			continue
		}
		oid, err := bson.ObjectIDFromHex(pid)
		if err != nil {
			continue
		}
		var qty float64
		if i < len(qtys) {
			qty, _ = strconv.ParseFloat(qtys[i], 64)
		}
		if qty <= 0 {
			continue
		}
		d.Lines = append(d.Lines, model.Line{ProductID: oid, Qty: qty})
	}
	// 回填商品编码/名称
	if master, err := contract.Master(h.e); err == nil {
		for i, l := range d.Lines {
			if p, err := master.Product(c.Request.Context(), mw.TenantID(c), l.ProductID); err == nil {
				d.Lines[i].ProductCode = p.Code
				d.Lines[i].ProductName = p.Name
			}
		}
	}
	if err := h.svc.CreateDoc(c.Request.Context(), mw.TenantID(c), d, mw.User(c).Username); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
		c.Redirect(http.StatusFound, "/app/inventory/docs/new?type="+d.Type)
		return
	}
	web.SetFlash(c, "单据已创建")
	c.Redirect(http.StatusFound, "/app/inventory/docs")
}

func (h *Handler) docDetail(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	d, err := h.svc.DocByID(c.Request.Context(), mw.TenantID(c), id)
	if err != nil {
		web.SetFlash(c, "单据不存在")
		c.Redirect(http.StatusFound, "/app/inventory/docs")
		return
	}
	web.Render(c, h.e, "inventory/doc_detail", gin.H{"Doc": d})
}

func (h *Handler) confirmDoc(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Confirm(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "确认失败: "+err.Error())
	} else {
		web.SetFlash(c, "已确认入账")
	}
	c.Redirect(http.StatusFound, "/app/inventory/docs/"+c.Param("id"))
}

func (h *Handler) cancelDoc(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Cancel(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "作废失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/inventory/docs")
}

func (h *Handler) balances(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 50)
	list, total, err := h.svc.Balances(c.Request.Context(), mw.TenantID(c), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	// 附带商品库存下限用于预警高亮
	lowMap := map[string]bool{}
	if master, err := contract.Master(h.e); err == nil {
		if products, err := master.Products(c.Request.Context(), mw.TenantID(c)); err == nil {
			minOf := map[string]float64{}
			for _, p := range products {
				minOf[p.ID.Hex()] = p.MinStock
			}
			for _, b := range list {
				if min := minOf[b.ProductID.Hex()]; min > 0 && b.Qty < min {
					lowMap[b.ID.Hex()] = true
				}
			}
		}
	}
	web.Render(c, h.e, "inventory/balances", gin.H{
		"Rows": list, "Pager": pager, "Low": lowMap,
	})
}
