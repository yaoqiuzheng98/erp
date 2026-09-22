package handler

import (
	"net/http"
	"strconv"

	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"
	"erp/internal/plugins/basedata/model"
	"erp/internal/plugins/basedata/service"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Handler struct {
	e   *env.Env
	svc *service.Service
}

func New(e *env.Env) *Handler {
	return &Handler{e: e, svc: service.New(e.DB.Database)}
}

func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("/products", mw.RequirePerm("basedata.product.read"), h.products)
	g.GET("/products/table", mw.RequirePerm("basedata.product.read"), h.productTable)
	g.POST("/products", mw.RequirePerm("basedata.product.write"), h.createProduct)
	g.GET("/products/:id/edit", mw.RequirePerm("basedata.product.write"), h.editProduct)
	g.POST("/products/:id", mw.RequirePerm("basedata.product.write"), h.updateProduct)

	g.GET("/warehouses", mw.RequirePerm("basedata.master.read"), h.warehouses)
	g.POST("/warehouses", mw.RequirePerm("basedata.master.write"), h.createWarehouse)
	g.GET("/customers", mw.RequirePerm("basedata.master.read"), h.customers)
	g.POST("/customers", mw.RequirePerm("basedata.master.write"), h.createCustomer)
	g.GET("/suppliers", mw.RequirePerm("basedata.master.read"), h.suppliers)
	g.POST("/suppliers", mw.RequirePerm("basedata.master.write"), h.createSupplier)
}

// ---------- 商品 ----------

func (h *Handler) products(c *gin.Context) {
	web.Render(c, h.e, "basedata/products", gin.H{})
}

func (h *Handler) productTable(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	list, total, err := h.svc.ListProducts(c.Request.Context(), mw.TenantID(c), c.Query("q"), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.RenderFrag(c, h.e, "basedata/product_rows", gin.H{"Rows": list, "Pager": pager})
}

func productForm(c *gin.Context, p *model.Product) {
	p.Code = c.PostForm("code")
	p.Name = c.PostForm("name")
	p.Unit = c.PostForm("unit")
	p.Category = c.PostForm("category")
	p.Spec = c.PostForm("spec")
	p.Price, _ = strconv.ParseFloat(c.PostForm("price"), 64)
	p.MinStock, _ = strconv.ParseFloat(c.PostForm("min_stock"), 64)
}

func (h *Handler) createProduct(c *gin.Context) {
	var p model.Product
	productForm(c, &p)
	if err := h.svc.CreateProduct(c.Request.Context(), mw.TenantID(c), &p, mw.User(c).Username); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "商品已创建")
	}
	c.Redirect(http.StatusFound, "/app/basedata/products")
}

func (h *Handler) editProduct(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	p, err := h.svc.ProductByID(c.Request.Context(), mw.TenantID(c), id)
	if err != nil {
		web.SetFlash(c, "商品不存在")
		c.Redirect(http.StatusFound, "/app/basedata/products")
		return
	}
	web.Render(c, h.e, "basedata/product_edit", gin.H{"P": p})
}

func (h *Handler) updateProduct(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	var p model.Product
	productForm(c, &p)
	err := h.svc.UpdateProduct(c.Request.Context(), mw.TenantID(c), id, bson.M{
		"name": p.Name, "unit": p.Unit, "category": p.Category,
		"spec": p.Spec, "price": p.Price, "min_stock": p.MinStock,
	})
	if err != nil {
		web.SetFlash(c, "更新失败: "+err.Error())
	} else {
		web.SetFlash(c, "商品已更新")
	}
	c.Redirect(http.StatusFound, "/app/basedata/products")
}

// ---------- 仓库/客户/供应商 ----------

func (h *Handler) warehouses(c *gin.Context) {
	list, _ := h.svc.ListWarehouses(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "basedata/warehouses", gin.H{"Rows": list})
}

func (h *Handler) createWarehouse(c *gin.Context) {
	w := &model.Warehouse{
		Code: c.PostForm("code"), Name: c.PostForm("name"), Address: c.PostForm("address"),
	}
	if err := h.svc.CreateWarehouse(c.Request.Context(), mw.TenantID(c), w); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "仓库已创建")
	}
	c.Redirect(http.StatusFound, "/app/basedata/warehouses")
}

func (h *Handler) customers(c *gin.Context) {
	list, _ := h.svc.ListCustomers(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "basedata/partners", gin.H{
		"Title": "客户", "Rows": list, "Path": "/app/basedata/customers",
	})
}

func (h *Handler) createCustomer(c *gin.Context) {
	cu := &model.Customer{
		Code: c.PostForm("code"), Name: c.PostForm("name"),
		Contact: c.PostForm("contact"), Phone: c.PostForm("phone"),
	}
	if err := h.svc.CreateCustomer(c.Request.Context(), mw.TenantID(c), cu); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "客户已创建")
	}
	c.Redirect(http.StatusFound, "/app/basedata/customers")
}

func (h *Handler) suppliers(c *gin.Context) {
	list, _ := h.svc.ListSuppliers(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "basedata/partners", gin.H{
		"Title": "供应商", "Rows": list, "Path": "/app/basedata/suppliers",
	})
}

func (h *Handler) createSupplier(c *gin.Context) {
	su := &model.Supplier{
		Code: c.PostForm("code"), Name: c.PostForm("name"),
		Contact: c.PostForm("contact"), Phone: c.PostForm("phone"),
	}
	if err := h.svc.CreateSupplier(c.Request.Context(), mw.TenantID(c), su); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "供应商已创建")
	}
	c.Redirect(http.StatusFound, "/app/basedata/suppliers")
}
