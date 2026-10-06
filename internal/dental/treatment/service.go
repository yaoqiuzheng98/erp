package treatment

import (
	"context"
	"errors"

	"erp/internal/platform/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ErrExists 同预约已开过诊疗单（一张预约只开一张）。
var ErrExists = errors.New("该预约已开过诊疗单，请刷新")

type Service struct {
	db    *mongo.Database
	items *repo.TenantRepo[Treatment]
}

func New(db *mongo.Database) *Service {
	return &Service{db: db, items: repo.NewTenantRepo[Treatment](db, "treatments")}
}

// EnsureIndexes 一张预约最多一张诊疗单（并发开单第二人直接撞唯一索引）。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	_, err := s.items.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenant_id", Value: 1}, {Key: "appt_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	return err
}

// Insert 建诊疗单（幂等：同预约已存在返回 ErrExists，调用方走修复/提示）。
func (s *Service) Insert(ctx context.Context, tenantID bson.ObjectID, t *Treatment) error {
	t.TenantID = tenantID
	id, err := s.items.Insert(ctx, tenantID, t)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrExists
		}
		return err
	}
	t.ID = id
	return nil
}

// ByAppt 按预约取诊疗单，无则返回 nil, nil。
func (s *Service) ByAppt(ctx context.Context, tenantID, apptID bson.ObjectID) (*Treatment, error) {
	t, err := s.items.FindOne(ctx, tenantID, bson.M{"appt_id": apptID})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return t, nil
}

// ByID 按 ID 取诊疗单。
func (s *Service) ByID(ctx context.Context, tenantID, id bson.ObjectID) (*Treatment, error) {
	return s.items.FindByID(ctx, tenantID, id)
}

// ListByPatient 某患者的诊疗单（就诊记录用；作废的不展示）。
func (s *Service) ListByPatient(ctx context.Context, tenantID, patientID bson.ObjectID) ([]Treatment, error) {
	return s.items.FindMany(ctx, tenantID,
		bson.M{"patient_id": patientID, "status": bson.M{"$ne": Void}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
}

// EnsureMigration 老数据迁移（幂等）：把已有诊疗内容的预约拆出诊疗单。
// 只处理 unpaid/done（已开过单的）；serving 等未开单的走新流程，不动。
// 有内容的标准：有明细 / 有单号 / 有病情或结果 / 有收费额。
// 跑完后通常是空操作。诊疗单状态跟费用单走（paid/void），找不到单按 billed。
func (s *Service) EnsureMigration(ctx context.Context) error {
	cur, err := s.db.Collection("appointments").Find(ctx, bson.M{
		"status": bson.M{"$in": []string{"unpaid", "done"}},
		"$or": []bson.M{
			{"items.0": bson.M{"$exists": true}},
			{"charge_no": bson.M{"$gt": ""}},
			{"diagnosis": bson.M{"$gt": ""}},
			{"result": bson.M{"$gt": ""}},
			{"charge": bson.M{"$gt": 0}},
		},
	})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	type oldAppt struct {
		ID          bson.ObjectID `bson:"_id"`
		TenantID    bson.ObjectID `bson:"tenant_id"`
		PatientID   bson.ObjectID `bson:"patient_id"`
		PatientName string        `bson:"patient_name"`
		DoctorID    bson.ObjectID `bson:"doctor_id"`
		Doctor      string        `bson:"doctor"`
		Date        string        `bson:"date"`
		Slot        string        `bson:"slot"`
		Item        string        `bson:"item"`
		Items       []Item        `bson:"items"`
		Diagnosis   string        `bson:"diagnosis"`
		Result      string        `bson:"result"`
		Charge      float64       `bson:"charge"`
		ChargeNo    string        `bson:"charge_no"`
	}
	for cur.Next(ctx) {
		var a oldAppt
		if err := cur.Decode(&a); err != nil {
			continue
		}
		if n, _ := s.items.Col.CountDocuments(ctx,
			bson.M{"tenant_id": a.TenantID, "appt_id": a.ID}); n > 0 {
			continue
		}
		// 找对应的诊疗费单（CH-），定状态、回填关联
		var bill struct {
			ID     bson.ObjectID `bson:"_id"`
			Status string        `bson:"status"`
		}
		status := Billed
		var billID bson.ObjectID
		if err := s.db.Collection("bills").FindOne(ctx, bson.M{
			"tenant_id": a.TenantID, "ref_id": a.ID.Hex(),
			"doc_no": bson.M{"$regex": "^CH-"},
		}).Decode(&bill); err == nil {
			billID = bill.ID
			switch bill.Status {
			case "paid":
				status = Paid
			case "void":
				status = Void
			}
		}
		t := &Treatment{
			ApptID: a.ID, PatientID: a.PatientID, PatientName: a.PatientName,
			DoctorID: a.DoctorID, Doctor: a.Doctor, Date: a.Date, Slot: a.Slot,
			Item: a.Item, Items: a.Items, Diagnosis: a.Diagnosis, Result: a.Result,
			Total: a.Charge, BillNo: a.ChargeNo, Status: status,
		}
		t.TenantID = a.TenantID
		id, err := s.items.Insert(ctx, a.TenantID, t)
		if err != nil {
			continue
		}
		if !billID.IsZero() {
			_, _ = s.db.Collection("bills").UpdateOne(ctx,
				bson.M{"_id": billID},
				bson.M{"$set": bson.M{"treatment_id": id}})
		}
	}
	return cur.Err()
}
func (s *Service) DeleteByAppt(ctx context.Context, tenantID, apptID bson.ObjectID) {
	_, _ = s.items.Col.DeleteOne(ctx,
		bson.M{"tenant_id": tenantID, "appt_id": apptID})
}

// MarkPaid 收清联动：已开单→已收（条件更新，幂等）。
func (s *Service) MarkPaid(ctx context.Context, tenantID, apptID bson.ObjectID) {
	_, _ = s.items.Col.UpdateOne(ctx,
		bson.M{"tenant_id": tenantID, "appt_id": apptID, "status": Billed},
		bson.M{"$set": bson.M{"status": Paid}})
}

// MarkVoid 作废联动：已开单→作废（条件更新，幂等）。
func (s *Service) MarkVoid(ctx context.Context, tenantID, apptID bson.ObjectID) {
	_, _ = s.items.Col.UpdateOne(ctx,
		bson.M{"tenant_id": tenantID, "appt_id": apptID, "status": Billed},
		bson.M{"$set": bson.M{"status": Void}})
}
