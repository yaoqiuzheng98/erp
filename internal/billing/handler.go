package billing

import (
	"net/http"
	"strconv"
	"time"

	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func parseDate(s string) time.Time {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return time.Now()
}

type Handler struct {
	e   *env.Env
	svc *Service
}

func NewHandler(e *env.Env, svc *Service) *Handler {
	return &Handler{e: e, svc: svc}
}

func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("", mw.RequirePerm("billing.read"), h.summary)
	g.GET("/receivables", mw.RequirePerm("billing.read"), h.receivables)
	g.POST("/bills/:id/pay", mw.RequirePerm("billing.write"), h.pay)
	g.GET("/payments", mw.RequirePerm("billing.read"), h.payments)
	g.GET("/expenses", mw.RequirePerm("billing.read"), h.expenses)
	g.POST("/expenses", mw.RequirePerm("billing.write"), h.createExpense)
}

func (h *Handler) summary(c *gin.Context) {
	openAR, received, expense := h.svc.Summary(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "billing/summary", gin.H{
		"OpenAR": openAR, "Received": received, "Expense": expense,
	})
}

func (h *Handler) receivables(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	list, total, err := h.svc.ListBills(c.Request.Context(), mw.TenantID(c), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "billing/bills", gin.H{"Bills": list, "Pager": pager})
}

func (h *Handler) pay(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	amount, _ := strconv.ParseFloat(c.PostForm("amount"), 64)
	if err := h.svc.Pay(c.Request.Context(), mw.TenantID(c), id, amount, c.PostForm("method"), mw.User(c).Username); err != nil {
		web.SetFlash(c, "核销失败: "+err.Error())
	} else {
		web.SetFlash(c, "已核销")
	}
	c.Redirect(http.StatusFound, "/app/billing/receivables")
}

func (h *Handler) payments(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	list, total, err := h.svc.ListPayments(c.Request.Context(), mw.TenantID(c), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "billing/payments", gin.H{"Rows": list, "Pager": pager})
}

func (h *Handler) expenses(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	list, total, err := h.svc.ListExpenses(c.Request.Context(), mw.TenantID(c), skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "billing/expenses", gin.H{"List": list, "Pager": pager})
}

func (h *Handler) createExpense(c *gin.Context) {
	amount, _ := strconv.ParseFloat(c.PostForm("amount"), 64)
	at := parseDate(c.PostForm("at"))
	ex := &Expense{
		Title: c.PostForm("title"), Amount: amount,
		Category: c.PostForm("category"), At: at,
	}
	if err := h.svc.CreateExpense(c.Request.Context(), mw.TenantID(c), ex, mw.User(c).Username); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "费用已记账")
	}
	c.Redirect(http.StatusFound, "/app/billing/expenses")
}
