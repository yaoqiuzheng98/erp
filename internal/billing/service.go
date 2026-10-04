package billing

import (
	"context"
	"errors"
	"time"

	"erp/internal/platform/repo"
	"erp/internal/platform/seqno"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var ErrOverPay = errors.New("核销金额超过未付余额")

type Service struct {
	db       *mongo.Database
	seq      *seqno.Generator
	bills    *repo.TenantRepo[Bill]
	payments *repo.TenantRepo[Payment]
	expenses *repo.TenantRepo[Expense]
}

func New(db *mongo.Database, seq *seqno.Generator) *Service {
	return &Service{
		db: db, seq: seq,
		bills:    repo.NewTenantRepo[Bill](db, "bills"),
		payments: repo.NewTenantRepo[Payment](db, "payments"),
		expenses: repo.NewTenantRepo[Expense](db, "expenses"),
	}
}

// EnsureIndexes doc_no 租户内唯一（防重复收费）。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	_, err := s.bills.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "doc_no", Value: 1}},
	})
	return err
}

// CreateAR 生成应收；同 DocNo 已存在则幂等返回 nil。
func (s *Service) CreateAR(ctx context.Context, tenantID bson.ObjectID, ar AR) error {
	if ar.Amount < 0 {
		return errors.New("金额不能为负")
	}
	if ar.DocNo != "" {
		if n, _ := s.bills.Count(ctx, tenantID, bson.M{"doc_no": ar.DocNo}); n > 0 {
			return nil
		}
	}
	b := &Bill{
		PatientID: ar.PatientID, PatientName: ar.PatientName,
		DocNo: ar.DocNo, Amount: ar.Amount, Lines: ar.Lines,
		Status: BillOpen, RefID: ar.RefID,
	}
	b.TenantID, b.CreatedAt, b.CreatedBy = tenantID, time.Now(), ar.By
	_, err := s.bills.Insert(ctx, tenantID, b)
	return err
}

// BillByRef 按来源预约查应收（诊疗单打印用），无单返回 nil, nil。
func (s *Service) BillByRef(ctx context.Context, tenantID bson.ObjectID, refID string) (*Bill, error) {
	if refID == "" {
		return nil, nil
	}
	b, err := s.bills.FindOne(ctx, tenantID, bson.M{"ref_id": refID})
	if err != nil {
		return nil, nil
	}
	return b, nil
}

// ByDocNo 按流水号查单。
func (s *Service) ByDocNo(ctx context.Context, tenantID bson.ObjectID, docNo string) (*Bill, error) {
	return s.bills.FindOne(ctx, tenantID, bson.M{"doc_no": docNo})
}

// PayMock 模拟全额代收（患者端/演示用，真支付接入时换这一处）。
func (s *Service) PayMock(ctx context.Context, tenantID, billID bson.ObjectID, by string) error {
	b, err := s.bills.FindByID(ctx, tenantID, billID)
	if err != nil {
		return err
	}
	if b.Status == BillPaid {
		return nil
	}
	return s.Pay(ctx, tenantID, billID, b.Amount-b.PaidAmount, "mock", by)
}

func (s *Service) ListBills(ctx context.Context, tenantID bson.ObjectID, skip, limit int64) ([]Bill, int64, error) {
	total, err := s.bills.Count(ctx, tenantID, bson.M{})
	if err != nil {
		return nil, 0, err
	}
	list, err := s.bills.FindMany(ctx, tenantID, bson.M{})
	return list, total, err
}

// Mine 某患者的全部账单（患者端）。
func (s *Service) Mine(ctx context.Context, tenantID, patientID bson.ObjectID) ([]Bill, error) {
	return s.bills.FindMany(ctx, tenantID, bson.M{"patient_id": patientID})
}

// Pay 核销：生成收款单并累加已核销额，足额标记 paid。
func (s *Service) Pay(ctx context.Context, tenantID, billID bson.ObjectID, amount float64, method string, by string) error {
	b, err := s.bills.FindByID(ctx, tenantID, billID)
	if err != nil {
		return err
	}
	if b.Status == BillPaid {
		return errors.New("单据已核销")
	}
	if amount <= 0 || b.PaidAmount+amount > b.Amount+1e-9 {
		return ErrOverPay
	}
	no, err := s.seq.Next(ctx, tenantID, "RC")
	if err != nil {
		return err
	}
	p := &Payment{
		DocNo: no, BillID: billID, BillDocNo: b.DocNo,
		Amount: amount, Method: method, PaidAt: time.Now(),
	}
	p.TenantID, p.CreatedAt, p.CreatedBy = tenantID, time.Now(), by
	if _, err := s.payments.Insert(ctx, tenantID, p); err != nil {
		return err
	}
	paid := b.PaidAmount + amount
	set := bson.M{"paid_amount": paid}
	fullyPaid := paid >= b.Amount-1e-9
	if fullyPaid {
		set["status"] = BillPaid
	}
	if err := s.bills.Update(ctx, tenantID, billID, set); err != nil {
		return err
	}
	if fullyPaid && b.RefID != "" {
		// 收清联动：对应预约从待缴费翻已完成（只翻待缴费态，幂等）
		if apptID, err := bson.ObjectIDFromHex(b.RefID); err == nil {
			_, _ = s.db.Collection("appointments").UpdateOne(ctx,
				bson.M{"tenant_id": tenantID, "_id": apptID, "status": "unpaid"},
				bson.M{"$set": bson.M{"status": "done", "updated_at": time.Now()}})
		}
	}
	return nil
}

func (s *Service) ListPayments(ctx context.Context, tenantID bson.ObjectID, skip, limit int64) ([]Payment, int64, error) {
	total, err := s.payments.Count(ctx, tenantID, bson.M{})
	if err != nil {
		return nil, 0, err
	}
	list, err := s.payments.FindMany(ctx, tenantID, bson.M{})
	return list, total, err
}

func (s *Service) ListExpenses(ctx context.Context, tenantID bson.ObjectID, skip, limit int64) ([]Expense, int64, error) {
	total, err := s.expenses.Count(ctx, tenantID, bson.M{})
	if err != nil {
		return nil, 0, err
	}
	list, err := s.expenses.FindMany(ctx, tenantID, bson.M{})
	return list, total, err
}

func (s *Service) CreateExpense(ctx context.Context, tenantID bson.ObjectID, ex *Expense, by string) error {
	ex.TenantID, ex.CreatedAt, ex.CreatedBy = tenantID, time.Now(), by
	_, err := s.expenses.Insert(ctx, tenantID, ex)
	return err
}

// Summary 账簿汇总：未收应收、累计收款、累计费用。
func (s *Service) Summary(ctx context.Context, tenantID bson.ObjectID) (openAR, received, expense float64) {
	sum := func(col *mongo.Collection, f bson.M, field string) float64 {
		pipe := mongo.Pipeline{
			{{Key: "$match", Value: f}},
			{{Key: "$group", Value: bson.M{"_id": nil, "s": bson.M{"$sum": "$" + field}}}},
		}
		cur, err := col.Aggregate(ctx, pipe)
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
	f := bson.M{"tenant_id": tenantID}
	openAR = sum(s.bills.Col, bson.M{"tenant_id": tenantID, "status": BillOpen}, "amount")
	received = sum(s.payments.Col, f, "amount")
	expense = sum(s.expenses.Col, f, "amount")
	return
}
