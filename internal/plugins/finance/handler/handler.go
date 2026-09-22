package handler

import (
	"net/http"
	"strconv"
	"time"

	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"
	"erp/internal/plugins/finance/model"
	"erp/internal/plugins/finance/service"

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
	g.GET("", mw.RequirePerm("finance.read"), h.summary)
	g.GET("/receivables", mw.RequirePerm("finance.read"), h.receivables)
	g.GET("/payables", mw.RequirePerm("finance.read"), h.payables)
	g.POST("/bills/:id/pay", mw.RequirePerm("finance.write"), h.pay)
	g.GET("/payments", mw.RequirePerm("finance.read"), h.payments)
	g.GET("/expenses", mw.RequirePerm("finance.read"), h.expenses)
	g.POST("/expenses", mw.RequirePerm("finance.write"), h.createExpense)
}

func (h *Handler) summary(c *gin.Context) {
	ar, ap, rcv, py, exp := h.svc.Summary(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "finance/summary", gin.H{
		"OpenAR": ar, "OpenAP": ap, "Received": rcv, "Paid": py, "Expense": exp,
	})
}

func (h *Handler) billPage(c *gin.Context, typ, tpl string) {
	skip, limit, pager := web.ParsePager(c, 20)
	list, total, err := h.svc.ListBills(c.Request.Context(), mw.TenantID(c), typ, skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, tpl, gin.H{"Bills": list, "Pager": pager})
}

func (h *Handler) receivables(c *gin.Context) { h.billPage(c, model.BillAR, "finance/bills") }
func (h *Handler) payables(c *gin.Context)    { h.billPage(c, model.BillAP, "finance/bills") }

func (h *Handler) pay(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	amount, _ := strconv.ParseFloat(c.PostForm("amount"), 64)
	if err := h.svc.Pay(c.Request.Context(), mw.TenantID(c), id, amount,
		c.PostForm("method"), mw.User(c).Username); err != nil {
		web.SetFlash(c, "核销失败: "+err.Error())
	} else {
		web.SetFlash(c, "已核销")
	}
	c.Redirect(http.StatusFound, c.Request.Referer())
}

func (h *Handler) payments(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	list, total, err := h.svc.ListPayments(c.Request.Context(), mw.TenantID(c), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "finance/payments", gin.H{"List": list, "Pager": pager})
}

func (h *Handler) expenses(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	list, total, err := h.svc.ListExpenses(c.Request.Context(), mw.TenantID(c), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "finance/expenses", gin.H{"List": list, "Pager": pager})
}

func (h *Handler) createExpense(c *gin.Context) {
	amount, _ := strconv.ParseFloat(c.PostForm("amount"), 64)
	at, _ := time.Parse("2006-01-02", c.PostForm("at"))
	if at.IsZero() {
		at = time.Now()
	}
	ex := &model.Expense{
		Title: c.PostForm("title"), Amount: amount,
		Category: c.PostForm("category"), At: at,
	}
	if err := h.svc.CreateExpense(c.Request.Context(), mw.TenantID(c), ex, mw.User(c).Username); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "费用已记录")
	}
	c.Redirect(http.StatusFound, "/app/finance/expenses")
}
