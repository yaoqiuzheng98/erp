package dental

import (
	"context"
	"errors"
	"strings"
	"time"

	"erp/internal/billing"
	"erp/internal/platform/repo"
	"erp/internal/platform/seqno"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var ErrBadStatus = errors.New("状态不允许该操作")

// Service 门诊业务（患者/预约/价目/员工），结算经 billing 直连。
type Service struct {
	db      *mongo.Database
	seq     *seqno.Generator
	billing *billing.Service
	pats    *repo.TenantRepo[Patient]
	appts   *repo.TenantRepo[Appointment]
	items   *repo.TenantRepo[ServiceItem]
	staff   *repo.TenantRepo[Staff]
}

func New(db *mongo.Database, seq *seqno.Generator, b *billing.Service) *Service {
	return &Service{
		db: db, seq: seq, billing: b,
		pats:  repo.NewTenantRepo[Patient](db, "patients"),
		appts: repo.NewTenantRepo[Appointment](db, "appointments"),
		items: repo.NewTenantRepo[ServiceItem](db, "service_items"),
		staff: repo.NewTenantRepo[Staff](db, "staff"),
	}
}

// EnsureSeed 建唯一索引 + 空表时种子默认价目（幂等，租户创建/演示种子时调）。
func (s *Service) EnsureSeed(ctx context.Context, tenantID bson.ObjectID) error {
	for _, ix := range []struct {
		col *mongo.Collection
		key string
	}{
		{s.items.Col, "name"},
		{s.staff.Col, "name"},
	} {
		if _, err := ix.col.Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: ix.key, Value: 1}},
		}); err != nil {
			return err
		}
	}
	n, err := s.items.Count(ctx, tenantID, bson.M{})
	if err != nil || n > 0 {
		return err
	}
	defaults := []ServiceItem{
		{Name: "初诊检查", Category: "检查", Price: 50, Unit: "次", Status: "active"},
		{Name: "口腔拍片", Category: "检查", Price: 100, Unit: "次", Status: "active"},
		{Name: "超声洁治", Category: "洁治", Price: 300, Unit: "次", Status: "active"},
		{Name: "树脂补牙", Category: "充填", Price: 300, Unit: "颗", Status: "active"},
		{Name: "根管治疗", Category: "根管", Price: 1200, Unit: "颗", Status: "active"},
		{Name: "简单拔牙", Category: "拔牙", Price: 300, Unit: "颗", Status: "active"},
		{Name: "阻生智齿拔除", Category: "拔牙", Price: 1200, Unit: "颗", Status: "active"},
		{Name: "正畸复诊", Category: "正畸", Price: 200, Unit: "次", Status: "active"},
		{Name: "烤瓷冠修复", Category: "修复", Price: 1500, Unit: "颗", Status: "active"},
		{Name: "种植牙", Category: "种植", Price: 8000, Unit: "颗", Status: "active"},
	}
	now := time.Now()
	docs := make([]any, 0, len(defaults))
	for _, d := range defaults {
		d.TenantID, d.CreatedAt = tenantID, now
		docs = append(docs, d)
	}
	_, err = s.db.Collection("service_items").InsertMany(ctx, docs)
	return err
}

// ---------- 患者 ----------

// ListPatients 姓名/电话模糊搜。
func (s *Service) ListPatients(ctx context.Context, tenantID bson.ObjectID, q string, skip, limit int64) ([]Patient, int64, error) {
	f := bson.M{}
	if q != "" {
		f["$or"] = []bson.M{
			{"name": bson.M{"$regex": q}},
			{"phone": bson.M{"$regex": q}},
		}
	}
	total, err := s.pats.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.pats.FindMany(ctx, tenantID, f)
	return list, total, err
}

func (s *Service) PatientByID(ctx context.Context, tenantID, id bson.ObjectID) (*Patient, error) {
	return s.pats.FindByID(ctx, tenantID, id)
}

// CreatePatient 建档：按电话自动认领已有患者（有则报错防重），认领不到才新建。
func (s *Service) CreatePatient(ctx context.Context, tenantID bson.ObjectID, p *Patient) error {
	if p.Name == "" {
		return errors.New("姓名必填")
	}
	if p.Phone != "" {
		if n, _ := s.pats.Count(ctx, tenantID, bson.M{"phone": p.Phone}); n > 0 {
			return errors.New("该电话已建过患者档案")
		}
	}
	p.TenantID, p.CreatedAt = tenantID, time.Now()
	p.ID, _ = s.pats.Insert(ctx, tenantID, p)
	_, err := s.pats.FindByID(ctx, tenantID, p.ID)
	return err
}

// SetTooth 设置牙位状态；空状态 = 恢复健康（删除键）。
func (s *Service) SetTooth(ctx context.Context, tenantID, patID bson.ObjectID, tooth, status string) error {
	if status == "" {
		_, err := s.pats.Col.UpdateOne(ctx,
			bson.M{"_id": patID, "tenant_id": tenantID},
			bson.M{"$unset": bson.M{"teeth." + tooth: ""}})
		return err
	}
	_, err := s.pats.Col.UpdateOne(ctx,
		bson.M{"_id": patID, "tenant_id": tenantID},
		bson.M{"$set": bson.M{"teeth." + tooth: status}})
	return err
}

// ---------- 价目 ----------

func (s *Service) ListServiceItems(ctx context.Context, tenantID bson.ObjectID, activeOnly bool) ([]ServiceItem, error) {
	f := bson.M{}
	if activeOnly {
		f["status"] = "active"
	}
	return s.items.FindMany(ctx, tenantID, f)
}

func (s *Service) ServiceItemByID(ctx context.Context, tenantID, id bson.ObjectID) (*ServiceItem, error) {
	return s.items.FindByID(ctx, tenantID, id)
}

func (s *Service) CreateServiceItem(ctx context.Context, tenantID bson.ObjectID, it *ServiceItem) error {
	if it.Name == "" {
		return errors.New("项目名称必填")
	}
	if n, _ := s.items.Count(ctx, tenantID, bson.M{"name": it.Name}); n > 0 {
		return errors.New("同名项目已存在")
	}
	if it.Price < 0 {
		return errors.New("单价不能为负")
	}
	if it.Unit == "" {
		it.Unit = "次"
	}
	it.TenantID, it.CreatedAt = tenantID, time.Now()
	if it.Status == "" {
		it.Status = "active"
	}
	_, err := s.items.Insert(ctx, tenantID, it)
	return err
}

func (s *Service) UpdateServiceItem(ctx context.Context, tenantID, id bson.ObjectID, set bson.M) error {
	return s.items.Update(ctx, tenantID, id, set)
}

func (s *Service) DeleteServiceItem(ctx context.Context, tenantID, id bson.ObjectID) error {
	return s.items.Delete(ctx, tenantID, id)
}

// ---------- 员工 ----------

func (s *Service) ListStaff(ctx context.Context, tenantID bson.ObjectID, activeOnly bool) ([]Staff, error) {
	f := bson.M{}
	if activeOnly {
		f["status"] = "active"
	}
	return s.staff.FindMany(ctx, tenantID, f)
}

// ListDoctors 预约用：只列在职医生。
func (s *Service) ListDoctors(ctx context.Context, tenantID bson.ObjectID) ([]Staff, error) {
	return s.staff.FindMany(ctx, tenantID, bson.M{"status": "active", "role": StaffDoctor})
}

func (s *Service) CreateStaff(ctx context.Context, tenantID bson.ObjectID, st *Staff) error {
	if st.Name == "" {
		return errors.New("姓名必填")
	}
	if _, ok := StaffRoles[st.Role]; !ok {
		return errors.New("角色无效")
	}
	if n, _ := s.staff.Count(ctx, tenantID, bson.M{"name": st.Name}); n > 0 {
		return errors.New("同名员工已存在")
	}
	st.TenantID, st.CreatedAt = tenantID, time.Now()
	if st.Status == "" {
		st.Status = "active"
	}
	_, err := s.staff.Insert(ctx, tenantID, st)
	return err
}

func (s *Service) UpdateStaff(ctx context.Context, tenantID, id bson.ObjectID, set bson.M) error {
	if r, ok := set["role"].(string); ok && r != "" {
		if _, valid := StaffRoles[r]; !valid {
			return errors.New("角色无效")
		}
	}
	return s.staff.Update(ctx, tenantID, id, set)
}

func (s *Service) DeleteStaff(ctx context.Context, tenantID, id bson.ObjectID) error {
	return s.staff.Delete(ctx, tenantID, id)
}

// ---------- 预约 ----------

// fillItems 按价目表回填明细快照（防前端改价），并汇总显示串与合计。
func (s *Service) fillItems(ctx context.Context, tenantID bson.ObjectID, in []ApptItem) ([]ApptItem, string, float64, error) {
	out := make([]ApptItem, 0, len(in))
	var total float64
	names := []string{}
	for _, l := range in {
		if l.Qty <= 0 {
			continue
		}
		si, err := s.ServiceItemByID(ctx, tenantID, l.ServiceID)
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

func (s *Service) doctorOf(ctx context.Context, tenantID, doctorID bson.ObjectID) (string, error) {
	d, err := s.staff.FindByID(ctx, tenantID, doctorID)
	if err != nil {
		return "", errors.New("医生不存在")
	}
	if d.Status != "active" || d.Role != StaffDoctor {
		return "", errors.New("医生不在职")
	}
	return d.Name, nil
}

func (s *Service) CreateAppt(ctx context.Context, tenantID bson.ObjectID, a *Appointment) error {
	p, err := s.PatientByID(ctx, tenantID, a.PatientID)
	if err != nil {
		return errors.New("患者不存在")
	}
	a.PatientName = p.Name
	name, err := s.doctorOf(ctx, tenantID, a.DoctorID)
	if err != nil {
		return err
	}
	a.Doctor = name
	if len(a.Items) > 0 {
		items, summary, _, err := s.fillItems(ctx, tenantID, a.Items)
		if err != nil {
			return err
		}
		a.Items, a.Item = items, summary
	}
	a.TenantID, a.Status, a.CreatedAt = tenantID, ApptBooked, time.Now()
	_, err = s.appts.Insert(ctx, tenantID, a)
	return err
}

func (s *Service) ApptByID(ctx context.Context, tenantID, id bson.ObjectID) (*Appointment, error) {
	return s.appts.FindByID(ctx, tenantID, id)
}

func (s *Service) ListAppts(ctx context.Context, tenantID bson.ObjectID, date string, skip, limit int64) ([]Appointment, int64, error) {
	f := bson.M{}
	if date != "" {
		f["date"] = date
	}
	total, err := s.appts.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.appts.FindMany(ctx, tenantID, f)
	return list, total, err
}

func (s *Service) ApptsOfPatient(ctx context.Context, tenantID, patID bson.ObjectID) ([]Appointment, error) {
	return s.appts.FindMany(ctx, tenantID, bson.M{"patient_id": patID})
}

func (s *Service) TodayAppts(ctx context.Context, tenantID bson.ObjectID) ([]Appointment, error) {
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
	_, err := s.setStatus(ctx, tenantID, id, []string{ApptBooked}, ApptArrived)
	return err
}

func (s *Service) NoShow(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{ApptBooked}, ApptNoShow)
	return err
}

func (s *Service) Cancel(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{ApptBooked}, ApptCancel)
	return err
}

// Complete 完成就诊并收费：优先按明细结算，否则沿用预约存量明细，
// 再否则用手工 charge。直接生成财务应收（同库，无事件中转）。
func (s *Service) Complete(ctx context.Context, tenantID, id bson.ObjectID, items []ApptItem, charge float64, by string) error {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if a.Status != ApptBooked && a.Status != ApptArrived {
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
	set := bson.M{"status": ApptDone, "charge": total, "items": finalItems, "item": summary}
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
