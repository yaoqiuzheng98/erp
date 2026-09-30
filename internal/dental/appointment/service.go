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
	"go.mongodb.org/mongo-driver/v2/mongo/options"
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

// EnsureIndexes 排号部分唯一：只有已取号（queue_no>0）的单据参与唯一约束。
// 注意不能用 sparse——复合稀疏索引会把缺字段当 null，同医生同天第二单就撞键。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	// 删掉历史错误版本（同名不同选项会报 IndexOptionsConflict）。
	_ = s.appts.Col.Indexes().DropOne(ctx, "tenant_id_1_doctor_id_1_date_1_queue_no_1")
	_, err := s.appts.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "doctor_id", Value: 1},
			{Key: "date", Value: 1}, {Key: "queue_no", Value: 1}},
		Options: options.Index().SetUnique(true).
			SetPartialFilterExpression(bson.M{"queue_no": bson.M{"$gt": 0}}),
	})
	return err
}

func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, a *Appointment) error {
	p, err := s.pats.ByID(ctx, tenantID, a.PatientID)
	if err != nil {
		return errors.New("患者不存在")
	}
	a.PatientName = p.Name
	// 医生可选：不选则签到时分配；选了则校验并快照
	if !a.DoctorID.IsZero() {
		name, err := s.doctorName(ctx, tenantID, a.DoctorID)
		if err != nil {
			return err
		}
		a.Doctor = name
		// 同医生同时段防重（自助约诊必需；前台走同一入口同样受检）。
		if n, _ := s.appts.Count(ctx, tenantID, bson.M{
			"doctor_id": a.DoctorID, "date": a.Date, "slot": a.Slot,
			"status": bson.M{"$in": []string{Booked, Arrived, Serving}},
		}); n > 0 {
			return errors.New("该医生该时段已约满，换个时间试试")
		}
	}
	if len(a.Items) > 0 {
		items, summary, _, err := s.fillItems(ctx, tenantID, a.Items)
		if err != nil {
			return err
		}
		a.Items, a.Item = items, summary
	}
	a.TenantID, a.Status, a.CreatedAt = tenantID, Booked, time.Now()
	id, err := s.appts.Insert(ctx, tenantID, a)
	if err != nil {
		return errors.New("建单失败: " + err.Error())
	}
	a.ID = id
	return nil
}

func (s *Service) ByID(ctx context.Context, tenantID, id bson.ObjectID) (*Appointment, error) {
	return s.appts.FindByID(ctx, tenantID, id)
}

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, date string, doctorID bson.ObjectID, phone string, skip, limit int64) ([]Appointment, int64, error) {
	f := bson.M{}
	if date != "" {
		f["date"] = date
	}
	if !doctorID.IsZero() {
		f["doctor_id"] = doctorID
	}
	if phone != "" {
		// 按患者电话筛：先找出匹配患者
		pats, _, _ := s.pats.List(ctx, tenantID, phone, 0, 100)
		ids := make([]bson.ObjectID, 0, len(pats))
		for _, p := range pats {
			ids = append(ids, p.ID)
		}
		f["patient_id"] = bson.M{"$in": ids}
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

// activeLoad 当天某医生手上未完结的单数（候诊+就诊中）。
func (s *Service) activeLoad(ctx context.Context, tenantID, doctorID bson.ObjectID, date string) int64 {
	n, _ := s.appts.Count(ctx, tenantID, bson.M{
		"doctor_id": doctorID, "date": date,
		"status": bson.M{"$in": []string{Arrived, Serving}},
	})
	return n
}

// pickDoctor 签到分配：有空闲医生（当天手上没活）用第一个；
// 都没有则取当天负载最小的医生。无可接诊医生时报错。
func (s *Service) pickDoctor(ctx context.Context, tenantID bson.ObjectID, date string) (bson.ObjectID, string, error) {
	docs, err := s.staff.ListDoctors(ctx, tenantID)
	if err != nil || len(docs) == 0 {
		return bson.NilObjectID, "", errors.New("暂无可接诊医生")
	}
	for _, d := range docs {
		if s.activeLoad(ctx, tenantID, d.ID, date) == 0 {
			return d.ID, d.Name, nil
		}
	}
	best, bestLoad := docs[0], s.activeLoad(ctx, tenantID, docs[0].ID, date)
	for _, d := range docs[1:] {
		if l := s.activeLoad(ctx, tenantID, d.ID, date); l < bestLoad {
			best, bestLoad = d, l
		}
	}
	return best.ID, best.Name, nil
}

// nextQueueNo 当天当医生下一个排号（已用号+1）。
func (s *Service) nextQueueNo(ctx context.Context, tenantID, doctorID bson.ObjectID, date string) int {
	n, _ := s.appts.Count(ctx, tenantID, bson.M{
		"doctor_id": doctorID, "date": date,
		"status": bson.M{"$in": []string{Arrived, Serving, Done}},
		"queue_no": bson.M{"$gt": 0},
	})
	return int(n) + 1
}

// CheckIn 签到：booked → arrived。已指定医生则进该医生队列（医生失效则自动改派）；
// 未指定医生则按空闲优先/最短队分配。返回最终医生名与排号。
func (s *Service) CheckIn(ctx context.Context, tenantID, id bson.ObjectID) (string, int, error) {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return "", 0, err
	}
	if a.Status != Booked {
		return "", 0, ErrBadStatus
	}
	docID, docName := a.DoctorID, a.Doctor
	if docID.IsZero() {
		var err error
		docID, docName, err = s.pickDoctor(ctx, tenantID, a.Date)
		if err != nil {
			return "", 0, err
		}
	} else if name, err := s.doctorName(ctx, tenantID, docID); err != nil {
		docID, docName, err = s.pickDoctor(ctx, tenantID, a.Date)
		if err != nil {
			return "", 0, err
		}
	} else {
		docName = name
	}
	set := bson.M{"status": Arrived, "doctor_id": docID, "doctor": docName}
	// 排号带一次重试（防并发签到同号，唯一索引兜底）。
	var lastErr error
	for i := 0; i < 3; i++ {
		set["queue_no"] = s.nextQueueNo(ctx, tenantID, docID, a.Date)
		if err := s.appts.Update(ctx, tenantID, id, set); err == nil {
			return docName, set["queue_no"].(int), nil
		} else {
			lastErr = err
		}
	}
	return "", 0, lastErr
}

// CallNow 手动叫号：arrived → serving（自动叫号的补充）。
func (s *Service) CallNow(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Arrived}, Serving)
	return err
}

// nextInLine 同医生当天排号最小的候诊单（自动叫号用）。
func (s *Service) nextInLine(ctx context.Context, tenantID, doctorID bson.ObjectID, date string) (*Appointment, error) {
	list, err := s.appts.FindMany(ctx, tenantID,
		bson.M{"doctor_id": doctorID, "date": date, "status": Arrived})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	best := list[0]
	for _, a := range list[1:] {
		if a.QueueNo < best.QueueNo {
			best = a
		}
	}
	return &best, nil
}

// Position 排位：serving=0（正在就诊），候诊=前面人数+1，其他状态=-1。
func (s *Service) Position(ctx context.Context, tenantID, id bson.ObjectID) int {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return -1
	}
	switch a.Status {
	case Serving:
		return 0
	case Arrived:
		n, _ := s.appts.Count(ctx, tenantID, bson.M{
			"doctor_id": a.DoctorID, "date": a.Date, "status": Arrived,
			"queue_no": bson.M{"$lt": a.QueueNo},
		})
		return int(n) + 1
	default:
		return -1
	}
}

func (s *Service) NoShow(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Booked}, NoShow)
	return err
}

func (s *Service) Cancel(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Booked, Arrived}, Cancel)
	return err
}

// Complete 完成就诊并收费：优先按明细结算，否则沿用预约存量明细，
// 再否则用手工 charge。直接生成财务应收（同库，无事件中转）。
// 完成后自动叫同医生下一位，返回其姓名（无则空）。
func (s *Service) Complete(ctx context.Context, tenantID, id bson.ObjectID, items []ApptItem, charge float64, by string) (string, error) {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return "", err
	}
	if a.Status != Booked && a.Status != Arrived && a.Status != Serving {
		return "", ErrBadStatus
	}
	finalItems := a.Items
	summary := a.Item
	total := charge
	if len(items) > 0 {
		filled, sum, t, err := s.fillItems(ctx, tenantID, items)
		if err != nil {
			return "", err
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
		return "", errors.New("收费额不能为负")
	}
	set := bson.M{"status": Done, "charge": total, "items": finalItems, "item": summary}
	if total > 0 {
		no, err := s.seq.Next(ctx, tenantID, "CH")
		if err != nil {
			return "", err
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
			return "", err
		}
	}
	if err := s.appts.Update(ctx, tenantID, id, set); err != nil {
		return "", err
	}
	// 自动叫号：同医生当天排号最小的候诊单转就诊中
	next, err := s.nextInLine(ctx, tenantID, a.DoctorID, a.Date)
	if err != nil || next == nil {
		return "", nil
	}
	if _, err := s.setStatus(ctx, tenantID, next.ID, []string{Arrived}, Serving); err != nil {
		return "", nil
	}
	return next.PatientName, nil
}
