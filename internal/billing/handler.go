package billing

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"erp/internal/platform/audit"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/tz"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Handler struct {
	e   *env.Env
	svc *Service
}

func NewHandler(e *env.Env, svc *Service) *Handler {
	return &Handler{e: e, svc: svc}
}

// audit 记财务操作审计（核销/作废必留痕）。
func (h *Handler) audit(c *gin.Context, action, target, detail string) {
	mw.Audit(c, h.e, action, target, detail)
}

func (h *Handler) Register(g *gin.RouterGroup) {
	// /app/billing 直接进应收（汇总与费用已下线）。
	g.GET("", mw.RequirePerm("billing.read"), func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/app/billing/receivables")
	})
	g.GET("/receivables", mw.RequirePerm("billing.read"), h.receivables)
	g.POST("/bills/:id/pay", mw.RequirePerm("billing.write"), h.pay)
	g.POST("/bills/:id/void", mw.RequirePerm("billing.write"), h.void)
	g.GET("/payments", mw.RequirePerm("billing.read"), h.payments)
}

func (h *Handler) receivables(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	from, to := c.Query("from"), c.Query("to")
	list, total, err := h.svc.ListBills(c.Request.Context(), mw.TenantID(c), from, to, skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	pager.Query = dateQuery(from, to)
	web.Render(c, h.e, "billing/bills", gin.H{"Bills": list, "Pager": pager, "From": from, "To": to, "Today": tz.Today()})
}

func (h *Handler) pay(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	b, err := h.svc.BillByID(ctx, tid, id)
	if err != nil || b.Status != BillOpen {
		web.SetFlash(c, "单据已结清或已作废")
		c.Redirect(http.StatusFound, "/app/billing/receivables")
		return
	}
	// 分次收款：金额默认剩余全额，可改小分多笔多方式收，凑满为止
	amount := b.Amount - b.PaidAmount
	if v := c.PostForm("amount"); v != "" {
		if a, err := strconv.ParseFloat(v, 64); err == nil {
			amount = a
		}
	}
	fully, err := h.svc.Pay(ctx, tid, id, amount, c.PostForm("method"), mw.User(c).Name)
	if err != nil {
		web.SetFlash(c, "核销失败: "+err.Error())
	} else if fully {
		h.audit(c, audit.ActBillingPay, b.DocNo, fmt.Sprintf("%.2f", amount))
		web.SetFlash(c, "已结清")
	} else {
		h.audit(c, audit.ActBillingPay, b.DocNo, fmt.Sprintf("%.2f", amount))
		web.SetFlash(c, "已收部分，还差结清")
	}
	c.Redirect(http.StatusFound, "/app/billing/receivables")
}

// void 作废应收（免单/坏账），移出未收。
func (h *Handler) void(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	docNo := id.Hex()
	if b, err := h.svc.BillByID(ctx, tid, id); err == nil {
		docNo = b.DocNo
	}
	if err := h.svc.VoidBill(ctx, tid, id); err != nil {
		web.SetFlash(c, "作废失败: "+err.Error())
	} else {
		h.audit(c, audit.ActBillingVoid, docNo, "")
		web.SetFlash(c, "单据已作废")
	}
	c.Redirect(http.StatusFound, "/app/billing/receivables")
}

func (h *Handler) payments(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 20)
	from, to := c.Query("from"), c.Query("to")
	list, total, err := h.svc.ListPayments(c.Request.Context(), mw.TenantID(c), from, to, skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	pager.Query = dateQuery(from, to)
	web.Render(c, h.e, "billing/payments", gin.H{"Rows": list, "Pager": pager, "From": from, "To": to, "Today": tz.Today()})
}

// dateQuery 翻页保留的日期筛选参数（空=全部；只收 DayStart 能解析的严格日期防注入）。
func dateQuery(from, to string) template.URL {
	q := ""
	if _, ok := tz.DayStart(from); from != "" && ok {
		q += "from=" + from + "&"
	}
	if _, ok := tz.DayStart(to); to != "" && ok {
		q += "to=" + to + "&"
	}
	return template.URL(q)
}
