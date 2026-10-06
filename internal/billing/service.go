package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"erp/internal/platform/repo"
	"erp/internal/platform/seqno"
	"erp/internal/platform/tz"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var ErrOverPay = errors.New("核销金额超过未付余额")

// epsilon 金额浮点比较容差（分以下抹零误差）。
const epsilon = 1e-9

// ValidMethod 收款方式白名单（表单手填非法值直接拒绝，不进库）。
func ValidMethod(m string) bool {
	switch m {
	case "cash", "bank", "other", "mock":
		return true
	}
	return false
}

type Service struct {
	db       *mongo.Database
	seq      *seqno.Generator
	bills    *repo.TenantRepo[Bill]
	payments *repo.TenantRepo[Payment]
}

func New(db *mongo.Database, seq *seqno.Generator) *Service {
	return &Service{
		db: db, seq: seq,
		bills:    repo.NewTenantRepo[Bill](db, "bills"),
		payments: repo.NewTenantRepo[Payment](db, "payments"),
	}
}

// EnsureIndexes doc_no 租户内唯一（防重复收费）。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	// 老版本非唯一索引先删，否则同名不同选项报 IndexOptionsConflict。
	_ = s.bills.Col.Indexes().DropOne(ctx, "tenant_id_1_doc_no_1")
	_, err := s.bills.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenant_id", Value: 1}, {Key: "doc_no", Value: 1}},
		Options: options.Index().SetUnique(true),
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
		Status: BillOpen, RefID: ar.RefID, TreatmentID: ar.TreatmentID,
	}
	b.TenantID, b.CreatedAt, b.CreatedBy = tenantID, time.Now(), ar.By
	_, err := s.bills.Insert(ctx, tenantID, b)
	return err
}

// BillByRef 按来源预约查诊疗应收（诊疗单打印用），无单返回 nil, nil。
// 注意只认 CH- 诊疗单：挂号费 RG 单也挂同一 ref_id，必须排除，
// 否则没开单也显示“已开单”（挂号流水号前缀规则见 seqno 用法）。
func (s *Service) BillByRef(ctx context.Context, tenantID bson.ObjectID, refID string) (*Bill, error) {
	if refID == "" {
		return nil, nil
	}
	b, err := s.bills.FindOne(ctx, tenantID, bson.M{
		"ref_id": refID, "doc_no": bson.M{"$regex": "^CH-"},
	})
	if err != nil {
		return nil, nil
	}
	return b, nil
}

// ByDocNo 按流水号查单。
func (s *Service) ByDocNo(ctx context.Context, tenantID bson.ObjectID, docNo string) (*Bill, error) {
	return s.bills.FindOne(ctx, tenantID, bson.M{"doc_no": docNo})
}

// BillByID 按 ID 查单。
func (s *Service) BillByID(ctx context.Context, tenantID, id bson.ObjectID) (*Bill, error) {
	return s.bills.FindByID(ctx, tenantID, id)
}

// VoidBill 作废应收：收不回来的单（免单/坏账）移出未收；
// 若关联预约还在待缴费，一并翻已完成（幂等，只翻待缴费态）。
func (s *Service) VoidBill(ctx context.Context, tenantID, billID bson.ObjectID) error {
	matched, err := s.bills.UpdateWhere(ctx, tenantID,
		bson.M{"_id": billID, "status": BillOpen}, bson.M{"status": BillVoid})
	if err != nil {
		return err
	}
	if matched == 0 {
		return errors.New("单据已结清或已作废")
	}
	b, err := s.bills.FindByID(ctx, tenantID, billID)
	if err != nil {
		return err
	}
	// 作废联动：诊疗单 billed→void；预约翻已完成只翻待缴费态（传统链路兼容，幂等）
	if !b.TreatmentID.IsZero() {
		_, _ = s.db.Collection("treatments").UpdateOne(ctx,
			bson.M{"tenant_id": tenantID, "_id": b.TreatmentID, "status": "billed"},
			bson.M{"$set": bson.M{"status": "void", "updated_at": time.Now()}})
	}
	if b.RefID != "" {
		if apptID, err := bson.ObjectIDFromHex(b.RefID); err == nil {
			if _, err := s.db.Collection("appointments").UpdateOne(ctx,
				bson.M{"tenant_id": tenantID, "_id": apptID, "status": "unpaid"},
				bson.M{"$set": bson.M{"status": "done", "updated_at": time.Now()}}); err != nil {
				return fmt.Errorf("已作废但预约状态联动失败，请联系管理员处理: %w", err)
			}
		}
	}
	return nil
}

// VoidOpenByRef 作废某预约名下所有未收单（取消待缴费预约用）。
// 只动 open 单：已结清的不动（收了的钱不退），已作废的不重复动。
// 返回被作废的单号（审计留痕用）。注意不联动预约状态，调用方负责落预约。
func (s *Service) VoidOpenByRef(ctx context.Context, tenantID bson.ObjectID, refID string) ([]string, error) {
	list, err := s.bills.FindMany(ctx, tenantID,
		bson.M{"ref_id": refID, "status": BillOpen})
	if err != nil {
		return nil, err
	}
	var voided []string
	for _, b := range list {
		matched, err := s.bills.UpdateWhere(ctx, tenantID,
			bson.M{"_id": b.ID, "status": BillOpen}, bson.M{"status": BillVoid})
		if err != nil {
			return voided, err
		}
		if matched > 0 {
			voided = append(voided, b.DocNo)
		}
		// 命中为 0 说明并发中被收掉了：只收不作废，跳过（钱已收，调用方取消预约即可）
	}
	return voided, nil
}

// ByRefPrefix 按来源预约+单号前缀查单（如 RG 挂号费），无则返回 nil,nil。
func (s *Service) ByRefPrefix(ctx context.Context, tenantID bson.ObjectID, refID, prefix string) (*Bill, error) {
	b, err := s.bills.FindOne(ctx, tenantID,
		bson.M{"ref_id": refID, "doc_no": bson.M{"$regex": "^" + prefix + "-"}})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
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
	_, err = s.Pay(ctx, tenantID, billID, b.Amount-b.PaidAmount, "mock", by)
	return err
}

func (s *Service) ListBills(ctx context.Context, tenantID bson.ObjectID, from, to string, skip, limit int64) ([]Bill, int64, error) {
	filter := dateFilter("created_at", from, to)
	total, err := s.bills.Count(ctx, tenantID, filter)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.bills.FindMany(ctx, tenantID, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetSkip(skip).SetLimit(limit))
	return list, total, err
}

// Mine 某患者的有效账单（患者端；已作废的不展示不计欠）。
func (s *Service) Mine(ctx context.Context, tenantID, patientID bson.ObjectID) ([]Bill, error) {
	return s.bills.FindMany(ctx, tenantID, bson.M{
		"patient_id": patientID, "status": bson.M{"$ne": BillVoid},
	})
}

// Pay 核销：生成收款单并累加已核销额，足额标记 paid。
// 支持部分收款（现金一部分+微信一部分，凑满为止），返回本次是否收清。
// 并发安全：先用「状态 open + 已核销额未变」条件落账，抢到的才算；
// 落账成功后记收款单，记单失败则回滚账单（最佳努力+日志），不留半提交。
func (s *Service) Pay(ctx context.Context, tenantID, billID bson.ObjectID, amount float64, method string, by string) (bool, error) {
	b, err := s.bills.FindByID(ctx, tenantID, billID)
	if err != nil {
		return false, err
	}
	if b.Status != BillOpen {
		return false, errors.New("单据已结清或已作废")
	}
	if !ValidMethod(method) {
		return false, errors.New("未知收款方式")
	}
	if amount <= 0 || b.PaidAmount+amount > b.Amount+epsilon {
		return false, ErrOverPay
	}
	no, err := s.seq.Next(ctx, tenantID, "RC")
	if err != nil {
		return false, err
	}
	paid := b.PaidAmount + amount
	fullyPaid := paid >= b.Amount-epsilon
	set := bson.M{"paid_amount": paid}
	if fullyPaid {
		set["status"] = BillPaid
	}
	matched, err := s.bills.UpdateWhere(ctx, tenantID,
		bson.M{"_id": billID, "status": BillOpen, "paid_amount": b.PaidAmount}, set)
	if err != nil {
		return false, err
	}
	if matched == 0 {
		return false, errors.New("单据状态已变化，请刷新后重试")
	}
	p := &Payment{
		DocNo: no, BillID: billID, BillDocNo: b.DocNo,
		Amount: amount, Method: method, PaidAt: time.Now(),
	}
	p.TenantID, p.CreatedAt, p.CreatedBy = tenantID, time.Now(), by
	if _, err := s.payments.Insert(ctx, tenantID, p); err != nil {
		if _, rerr := s.bills.UpdateWhere(ctx, tenantID,
			bson.M{"_id": billID, "paid_amount": paid},
			bson.M{"paid_amount": b.PaidAmount, "status": BillOpen}); rerr != nil {
			slog.Error("pay rollback failed", "bill", billID.Hex(), "err", rerr)
		}
		return false, err
	}
	if fullyPaid {
		// 收清联动：诊疗单 billed→paid；预约翻已完成（只翻待缴费态，幂等，传统链路兼容）
		if !b.TreatmentID.IsZero() {
			_, _ = s.db.Collection("treatments").UpdateOne(ctx,
				bson.M{"tenant_id": tenantID, "_id": b.TreatmentID, "status": "billed"},
				bson.M{"$set": bson.M{"status": "paid", "updated_at": time.Now()}})
		}
		if b.RefID != "" {
			if apptID, err := bson.ObjectIDFromHex(b.RefID); err == nil {
				if _, err := s.db.Collection("appointments").UpdateOne(ctx,
					bson.M{"tenant_id": tenantID, "_id": apptID, "status": "unpaid"},
					bson.M{"$set": bson.M{"status": "done", "updated_at": time.Now()}}); err != nil {
					return true, fmt.Errorf("已收款但预约状态联动失败，请联系管理员处理: %w", err)
				}
			}
		}
	}
	return fullyPaid, nil
}

func (s *Service) ListPayments(ctx context.Context, tenantID bson.ObjectID, from, to string, skip, limit int64) ([]Payment, int64, error) {
	filter := dateFilter("paid_at", from, to)
	total, err := s.payments.Count(ctx, tenantID, filter)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.payments.FindMany(ctx, tenantID, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetSkip(skip).SetLimit(limit))
	return list, total, err
}

// dateFilter 某时间字段的自然日范围过滤（东八区；起止为空=不限）。
func dateFilter(field, from, to string) bson.M {
	m := bson.M{}
	if t, ok := tz.DayStart(from); from != "" && ok {
		m["$gte"] = t
	}
	if t, ok := tz.DayStart(to); to != "" && ok {
		m["$lt"] = t.AddDate(0, 0, 1)
	}
	if len(m) == 0 {
		return bson.M{}
	}
	return bson.M{field: m}
}
