package service

import (
	"context"
	"errors"
	"time"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	"erp/internal/platform/repo"
	"erp/internal/plugins/finance/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var ErrOverPay = errors.New("核销金额超过未付余额")

type Service struct {
	e        *env.Env
	bills    *repo.TenantRepo[model.Bill]
	payments *repo.TenantRepo[model.Payment]
	expenses *repo.TenantRepo[model.Expense]
}

func New(e *env.Env) *Service {
	return &Service{
		e:        e,
		bills:    repo.NewTenantRepo[model.Bill](e.DB.Database, "plg_finance_bill"),
		payments: repo.NewTenantRepo[model.Payment](e.DB.Database, "plg_finance_payment"),
		expenses: repo.NewTenantRepo[model.Expense](e.DB.Database, "plg_finance_expense"),
	}
}

// OnOrderApproved 订单审核事件 → 生成应收/应付。
func (s *Service) OnOrderApproved(ctx context.Context, tenantID bson.ObjectID, o contract.OrderApproved, typ, partnerName string) error {
	b := &model.Bill{
		Type: typ, PartnerID: o.PartnerID, PartnerName: partnerName,
		OrderID: o.OrderID, DocNo: o.DocNo,
		Amount: o.Total, Status: model.BillOpen,
	}
	b.TenantID, b.CreatedAt = tenantID, time.Now()
	_, err := s.bills.Insert(ctx, tenantID, b)
	return err
}

// OnCharge 业务收费事件（门诊/服务类）→ 生成应收。PartyID 直接挂客户档案，
// 按 DocNo 幂等去重（事件重复投递不产生重复应收）。
func (s *Service) OnCharge(ctx context.Context, tenantID bson.ObjectID, ch contract.Charge) error {
	if ch.DocNo != "" {
		if n, _ := s.bills.Count(ctx, tenantID, bson.M{"doc_no": ch.DocNo}); n > 0 {
			return nil
		}
	}
	b := &model.Bill{
		Type: model.BillAR, PartnerID: ch.PartyID, PartnerName: ch.PartyName,
		OrderID: ch.RefID, DocNo: ch.DocNo,
		Amount: ch.Total, Status: model.BillOpen,
	}
	b.TenantID, b.CreatedAt = tenantID, time.Now()
	_, err := s.bills.Insert(ctx, tenantID, b)
	return err
}

func (s *Service) ListBills(ctx context.Context, tenantID bson.ObjectID, typ string, skip, limit int64) ([]model.Bill, int64, error) {
	total, err := s.bills.Count(ctx, tenantID, bson.M{"type": typ})
	if err != nil {
		return nil, 0, err
	}
	list, err := s.bills.FindMany(ctx, tenantID, bson.M{"type": typ})
	return list, total, err
}

// Pay 对账单核销：生成收付款单并累加已核销额，足额标记 paid。
func (s *Service) Pay(ctx context.Context, tenantID, billID bson.ObjectID, amount float64, method string, by string) error {
	b, err := s.bills.FindByID(ctx, tenantID, billID)
	if err != nil {
		return err
	}
	if b.Status == model.BillPaid {
		return errors.New("单据已核销")
	}
	if amount <= 0 || b.PaidAmount+amount > b.Amount+1e-9 {
		return ErrOverPay
	}
	payType := model.PayReceipt
	rule := "RC"
	if b.Type == model.BillAP {
		payType = model.PayOut
		rule = "PY"
	}
	no, err := s.e.Seq.Next(ctx, tenantID, rule)
	if err != nil {
		return err
	}
	p := &model.Payment{
		DocNo: no, Type: payType, BillID: billID, BillDocNo: b.DocNo,
		Amount: amount, Method: method, PaidAt: time.Now(),
	}
	p.TenantID, p.CreatedAt, p.CreatedBy = tenantID, time.Now(), by
	if _, err := s.payments.Insert(ctx, tenantID, p); err != nil {
		return err
	}
	paid := b.PaidAmount + amount
	set := bson.M{"paid_amount": paid}
	if paid >= b.Amount-1e-9 {
		set["status"] = model.BillPaid
	}
	return s.bills.Update(ctx, tenantID, billID, set)
}

func (s *Service) ListPayments(ctx context.Context, tenantID bson.ObjectID, skip, limit int64) ([]model.Payment, int64, error) {
	total, err := s.payments.Count(ctx, tenantID, bson.M{})
	if err != nil {
		return nil, 0, err
	}
	list, err := s.payments.FindMany(ctx, tenantID, bson.M{})
	return list, total, err
}

func (s *Service) ListExpenses(ctx context.Context, tenantID bson.ObjectID, skip, limit int64) ([]model.Expense, int64, error) {
	total, err := s.expenses.Count(ctx, tenantID, bson.M{})
	if err != nil {
		return nil, 0, err
	}
	list, err := s.expenses.FindMany(ctx, tenantID, bson.M{})
	return list, total, err
}

func (s *Service) CreateExpense(ctx context.Context, tenantID bson.ObjectID, ex *model.Expense, by string) error {
	ex.TenantID, ex.CreatedAt, ex.CreatedBy = tenantID, time.Now(), by
	_, err := s.expenses.Insert(ctx, tenantID, ex)
	return err
}

// Summary 简易账簿汇总。
func (s *Service) Summary(ctx context.Context, tenantID bson.ObjectID) (openAR, openAP, received, paid, expense float64) {
	sum := func(col *repo.TenantRepo[model.Bill], f bson.M) float64 {
		pipe := mongo.Pipeline{
			{{Key: "$match", Value: f}},
			{{Key: "$group", Value: bson.M{"_id": nil, "s": bson.M{"$sum": "$amount"}}}},
		}
		cur, err := col.Col.Aggregate(ctx, pipe)
		if err != nil {
			return 0
		}
		var rows []struct {
			S float64 `bson:"s"`
		}
		_ = cur.All(ctx, &rows)
		if len(rows) > 0 {
			return rows[0].S
		}
		return 0
	}
	sumP := func(f bson.M) float64 {
		pipe := mongo.Pipeline{
			{{Key: "$match", Value: f}},
			{{Key: "$group", Value: bson.M{"_id": nil, "s": bson.M{"$sum": "$amount"}}}},
		}
		cur, err := s.payments.Col.Aggregate(ctx, pipe)
		if err != nil {
			return 0
		}
		var rows []struct {
			S float64 `bson:"s"`
		}
		_ = cur.All(ctx, &rows)
		if len(rows) > 0 {
			return rows[0].S
		}
		return 0
	}
	sumE := func() float64 {
		pipe := mongo.Pipeline{
			{{Key: "$match", Value: bson.M{"tenant_id": tenantID}}},
			{{Key: "$group", Value: bson.M{"_id": nil, "s": bson.M{"$sum": "$amount"}}}},
		}
		cur, err := s.expenses.Col.Aggregate(ctx, pipe)
		if err != nil {
			return 0
		}
		var rows []struct {
			S float64 `bson:"s"`
		}
		_ = cur.All(ctx, &rows)
		if len(rows) > 0 {
			return rows[0].S
		}
		return 0
	}
	openAR = sum(s.bills, bson.M{"tenant_id": tenantID, "type": model.BillAR, "status": model.BillOpen})
	openAP = sum(s.bills, bson.M{"tenant_id": tenantID, "type": model.BillAP, "status": model.BillOpen})
	received = sumP(bson.M{"tenant_id": tenantID, "type": model.PayReceipt})
	paid = sumP(bson.M{"tenant_id": tenantID, "type": model.PayOut})
	expense = sumE()
	return
}
