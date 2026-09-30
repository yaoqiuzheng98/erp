package appointment

import (
	"context"
	"errors"
	"strings"
	"time"

	"erp/internal/billing"
	"erp/internal/dental/catalog"
	"erp/internal/dental/patient"
	"erp/internal/dental/staff"
	"erp/internal/platform/repo"
	"erp/internal/platform/seqno"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var ErrBadStatus = errors.New("状态不允许该操作")

type Service struct {
	db      *mongo.Database
	seq     *seqno.Generator
	billing *billing.Service
	appts   *repo.TenantRepo[Appointment]
	pats    *patient.Service
	items   *catalog.Service
	staff   *staff.Service
}

func New(db *mongo.Database, seq *seqno.Generator, b *billing.Service,
	pats *patient.Service, items *catalog.Service, staff *staff.Service) *Service {
	return &Service{
		db: db, seq: seq, billing: b, appts: repo.NewTenantRepo[Appointment](db, "appointments"),
		pats: pats, items: items, staff: staff,
	}
}

// fillItems 按价目表回填明细快照（防前端改价），并汇总显示串与合计。
func (s *Service) fillItems(ctx context.Context, tenantID bson.ObjectID, in []ApptItem) ([]ApptItem, string, float64, error) {
	out := make([]ApptItem, 0, len(in))
	var total float64
	names := []string{}
	for _, l := range in {
		if l.Qty <= 0 {
			continue
		}
		si, err := s.items.ByID(ctx, tenantID, l.ServiceID)
		if err != nil {
			return nil, "", 0, errors.New("价目项目不存在")
		}
		if si.Status != "active" {
			return nil, "", 0, errors.New("项目已停用：" + si.Name)
		}
		amt := l.Qty * si.Price
		out = append(out, ApptItem{
			ServiceID: si.ID, Name: si.Name,
			Qty: l.Qty, Price: si.Price, Amount: amt,
		})
		total += amt
		names = append(names, si.Name)
	}
	return out, strings.Join(names, "、"), total, nil
}

func (s *Service) doctorName(ctx context.Context, tenantID, doctorID bson.ObjectID) (string, error) {
	d, err := s.staff.ByID(ctx, tenantID, doctorID)
	if err != nil {
		return "", errors.New("医生不存在")
	}
	if d.Status != "active" || !s.staff.CanPractice(ctx, tenantID, d.Role) {
		return "", errors.New("医生不在职或不可接诊")
	}
	return d.Name, nil
}

func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, a *Appointment) error {
	p, err := s.pats.ByID(ctx, tenantID, a.PatientID)
	if err != nil {
		return errors.New("患者不存在")
	}
	a.PatientName = p.Name
	name, err := s.doctorName(ctx, tenantID, a.DoctorID)
	if err != nil {
		return err
	}
	a.Doctor = name
	// 同医生同时段防重（自助约诊必需；前台走同一入口同样受检）。
	if n, _ := s.appts.Count(ctx, tenantID, bson.M{
		"doctor_id": a.DoctorID, "date": a.Date, "slot": a.Slot,
		"status": bson.M{"$in": []string{Booked, Arrived}},
	}); n > 0 {
		return errors.New("该医生该时段已约满，换个时间试试")
	}
	if len(a.Items) > 0 {
		items, summary, _, err := s.fillItems(ctx, tenantID, a.Items)
		if err != nil {
			return err
		}
		a.Items, a.Item = items, summary
	}
	a.TenantID, a.Status, a.CreatedAt = tenantID, Booked, time.Now()
	a.ID, _ = s.appts.Insert(ctx, tenantID, a)
	_, err = s.appts.FindByID(ctx, tenantID, a.ID)
	return err
}

func (s *Service) ByID(ctx context.Context, tenantID, id bson.ObjectID) (*Appointment, error) {
	return s.appts.FindByID(ctx, tenantID, id)
}

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, date string, doctorID bson.ObjectID, skip, limit int64) ([]Appointment, int64, error) {
	f := bson.M{}
	if date != "" {
		f["date"] = date
	}
	if !doctorID.IsZero() {
		f["doctor_id"] = doctorID
	}
	total, err := s.appts.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.appts.FindMany(ctx, tenantID, f)
	return list, total, err
}

func (s *Service) OfPatient(ctx context.Context, tenantID, patID bson.ObjectID) ([]Appointment, error) {
	return s.appts.FindMany(ctx, tenantID, bson.M{"patient_id": patID})
}

func (s *Service) Today(ctx context.Context, tenantID bson.ObjectID) ([]Appointment, error) {
	return s.appts.FindMany(ctx, tenantID, bson.M{"date": time.Now().Format("2006-01-02")})
}

func (s *Service) setStatus(ctx context.Context, tenantID, id bson.ObjectID, from []string, to string) (*Appointment, error) {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	ok := false
	for _, f := range from {
		if a.Status == f {
			ok = true
		}
	}
	if !ok {
		return nil, ErrBadStatus
	}
	return a, s.appts.Update(ctx, tenantID, id, bson.M{"status": to})
}

func (s *Service) Arrive(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Booked}, Arrived)
	return err
}

func (s *Service) NoShow(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Booked}, NoShow)
	return err
}

func (s *Service) Cancel(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Booked}, Cancel)
	return err
}

// Complete 完成就诊并收费：优先按明细结算，否则沿用预约存量明细，
// 再否则用手工 charge。直接生成财务应收（同库，无事件中转）。
func (s *Service) Complete(ctx context.Context, tenantID, id bson.ObjectID, items []ApptItem, charge float64, by string) error {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if a.Status != Booked && a.Status != Arrived {
		return ErrBadStatus
	}
	finalItems := a.Items
	summary := a.Item
	total := charge
	if len(items) > 0 {
		filled, sum, t, err := s.fillItems(ctx, tenantID, items)
		if err != nil {
			return err
		}
		finalItems, summary, total = filled, sum, t
	} else if len(a.Items) > 0 {
		var t float64
		for _, it := range a.Items {
			t += it.Amount
		}
		total = t
	}
	if total < 0 {
		return errors.New("收费额不能为负")
	}
	set := bson.M{"status": Done, "charge": total, "items": finalItems, "item": summary}
	if total > 0 {
		no, err := s.seq.Next(ctx, tenantID, "CH")
		if err != nil {
			return err
		}
		set["charge_no"] = no
		lines := make([]billing.BillLine, 0, len(finalItems))
		for _, it := range finalItems {
			lines = append(lines, billing.BillLine{
				Name: it.Name, Qty: it.Qty, Price: it.Price, Amount: it.Amount,
			})
		}
		if err := s.billing.CreateAR(ctx, tenantID, billing.AR{
			PatientID: a.PatientID, PatientName: a.PatientName,
			DocNo: no, Amount: total, Lines: lines, RefID: id.Hex(), By: by,
		}); err != nil {
			return err
		}
	}
	return s.appts.Update(ctx, tenantID, id, set)
}
