package handler

import (
	"net/http"
	"strconv"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"
	"erp/internal/plugins/sales/model"
	"erp/internal/plugins/sales/service"

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
	g.GET("/orders", mw.RequirePerm("sales.order.read"), h.orders)
	g.GET("/orders/new", mw.RequirePerm("sales.order.write"), h.newOrder)
	g.POST("/orders", mw.RequirePerm("sales.order.write"), h.create)
	g.GET("/orders/:id", mw.RequirePerm("sales.order.read"), h.detail)
	g.POST("/orders/:id/submit", mw.RequirePerm("sales.order.write"), h.submit)
	g.POST("/orders/:id/approve", mw.RequirePerm("sales.order.approve"), h.approve)
	g.POST("/orders/:id/done", mw.RequirePerm("sales.order.write"), h.done)
	g.POST("/orders/:id/cancel", mw.RequirePerm("sales.order.write"), h.cancel)
}

func (h *Handler) orders(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	list, total, err := h.svc.List(c.Request.Context(), mw.TenantID(c), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "sales/orders", gin.H{"Orders": list, "Pager": pager})
}

func (h *Handler) newOrder(c *gin.Context) {
	master, err := contract.Master(h.e)
	if err != nil {
		web.SetFlash(c, "基础资料插件未启用")
		c.Redirect(http.StatusFound, "/app/sales/orders")
		return
	}
	tid := mw.TenantID(c)
	customers, _ := master.Customers(c.Request.Context(), tid)
	products, _ := master.Products(c.Request.Context(), tid)
	warehouses, _ := master.Warehouses(c.Request.Context(), tid)
	web.Render(c, h.e, "sales/order_new", gin.H{
		"Customers": customers, "Products": products, "Warehouses": warehouses,
	})
}

func (h *Handler) create(c *gin.Context) {
	tid := mw.TenantID(c)
	o := &model.Order{Remark: c.PostForm("remark")}
	o.CustomerID, _ = bson.ObjectIDFromHex(c.PostForm("customer_id"))
	o.WarehouseID, _ = bson.ObjectIDFromHex(c.PostForm("warehouse_id"))
	productIDs := c.PostFormArray("product_id")
	qtys := c.PostFormArray("qty")
	prices := c.PostFormArray("price")
	for i, pid := range productIDs {
		oid, err := bson.ObjectIDFromHex(pid)
		if err != nil {
			continue
		}
		var qty, price float64
		if i < len(qtys) {
			qty, _ = strconv.ParseFloat(qtys[i], 64)
		}
		if i < len(prices) {
			price, _ = strconv.ParseFloat(prices[i], 64)
		}
		if qty <= 0 {
			continue
		}
		o.Lines = append(o.Lines, contract.OrderLine{
			ProductID: oid, WarehouseID: o.WarehouseID, Qty: qty, Price: price,
		})
	}
	// 回填商品与客户名称
	if master, err := contract.Master(h.e); err == nil {
		for i, l := range o.Lines {
			if p, err := master.Product(c.Request.Context(), tid, l.ProductID); err == nil {
				o.Lines[i].ProductCode = p.Code
				o.Lines[i].ProductName = p.Name
				if o.Lines[i].Price == 0 {
					o.Lines[i].Price = p.Price
				}
			}
		}
		if cus, err := master.Customers(c.Request.Context(), tid); err == nil {
			for _, cu := range cus {
				if cu.ID == o.CustomerID {
					o.CustomerName = cu.Name
				}
			}
		}
	}
	if err := h.svc.Create(c.Request.Context(), tid, o, mw.User(c).Username); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
		c.Redirect(http.StatusFound, "/app/sales/orders/new")
		return
	}
	c.Redirect(http.StatusFound, "/app/sales/orders/"+o.ID.Hex())
}

func (h *Handler) detail(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	o, err := h.svc.ByID(c.Request.Context(), mw.TenantID(c), id)
	if err != nil {
		web.SetFlash(c, "订单不存在")
		c.Redirect(http.StatusFound, "/app/sales/orders")
		return
	}
	web.Render(c, h.e, "sales/order_detail", gin.H{
		"Order": o, "FlowEnabled": h.e.Gate.IsEnabled(c.Request.Context(), mw.TenantID(c), "flow"),
	})
}

func (h *Handler) submit(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Submit(c.Request.Context(), mw.TenantID(c), id, mw.User(c).Username); err != nil {
		web.SetFlash(c, "提交失败: "+err.Error())
	} else {
		web.SetFlash(c, "已提交")
	}
	c.Redirect(http.StatusFound, "/app/sales/orders/"+c.Param("id"))
}

func (h *Handler) approve(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Approve(c.Request.Context(), mw.TenantID(c), id, mw.User(c).Username); err != nil {
		web.SetFlash(c, "审核失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/sales/orders/"+c.Param("id"))
}

func (h *Handler) done(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Done(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "操作失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/sales/orders/"+c.Param("id"))
}

func (h *Handler) cancel(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.svc.Cancel(c.Request.Context(), mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "作废失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/app/sales/orders/"+c.Param("id"))
}
