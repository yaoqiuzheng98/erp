package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	"erp/internal/platform/event"
	"erp/internal/platform/repo"
	"erp/internal/plugins/dental/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var ErrBadStatus = errors.New("状态不允许该操作")

type Service struct {
	e     *env.Env
	pats  *repo.TenantRepo[model.Patient]
	appts *repo.TenantRepo[model.Appointment]
	items *repo.TenantRepo[model.ServiceItem]
}

func New(e *env.Env) *Service {
	return &Service{
		e:     e,
		pats:  repo.NewTenantRepo[model.Patient](e.DB.Database, "plg_dental_patient"),
		appts: repo.NewTenantRepo[model.Appointment](e.DB.Database, "plg_dental_appt"),
		items: repo.NewTenantRepo[model.ServiceItem](e.DB.Database, "plg_dental_service_item"),
	}
}

// master 取主数据 API（basedata 必须启用，依赖声明保证）。
func (s *Service) master() (contract.MasterDataAPI, error) {
	return contract.Master(s.e)
}

// hydrate 患者 join 客户姓名/电话。
func (s *Service) hydrate(ctx context.Context, tenantID bson.ObjectID, pats []model.Patient) []model.View {
	master, err := s.master()
	if err != nil {
		return nil
	}
	custs, err := master.Customers(ctx, tenantID)
	if err != nil {
		return nil
	}
	names := map[bson.ObjectID]contract.PartnerRef{}
	for _, c := range custs {
		names[c.ID] = c
	}
	out := make([]model.View, 0, len(pats))
	for _, p := range pats {
		v := model.View{Patient: p}
		if c, ok := names[p.CustomerID]; ok {
			v.Name, v.Phone = c.Name, c.Phone
		}
		out = append(out, v)
	}
	return out
}

func (s *Service) ListPatients(ctx context.Context, tenantID bson.ObjectID, q string, skip, limit int64) ([]model.View, int64, error) {
	f := bson.M{}
	if q != "" {
		// 姓名/电话/编码经客户表匹配出 customer_id 集合；病历号直接查
		var ids []bson.ObjectID
		if master, err := s.master(); err == nil {
			custs, _ := master.Customers(ctx, tenantID)
			for _, c := range custs {
				if strings.Contains(c.Name, q) || strings.Contains(c.Code, q) || strings.Contains(c.Phone, q) {
					ids = append(ids, c.ID)
				}
			}
		}
		f["$or"] = []bson.M{
			{"code": bson.M{"$regex": q}},
			{"customer_id": bson.M{"$in": ids}},
		}
	}
	total, err := s.pats.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.pats.FindMany(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	return s.hydrate(ctx, tenantID, list), total, nil
}

func (s *Service) PatientByID(ctx context.Context, tenantID, id bson.ObjectID) (*model.Patient, error) {
	return s.pats.FindByID(ctx, tenantID, id)
}

// PatientView 详情（含客户姓名/电话）。
func (s *Service) PatientView(ctx context.Context, tenantID, id bson.ObjectID) (*model.View, error) {
	p, err := s.PatientByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	v := &model.View{Patient: *p}
	if master, err := s.master(); err == nil {
		if c, err := master.Customer(ctx, tenantID, p.CustomerID); err == nil {
			v.Name, v.Phone = c.Name, c.Phone
		}
	}
	return v, nil
}

// CreatePatient 建档。customerID 非零 = 挂接已有客户（不新建）；否则以
// name/phone 建 basedata 客户。病历号同时作为新建客户的编码。
func (s *Service) CreatePatient(ctx context.Context, tenantID bson.ObjectID, p *model.Patient, name, phone string, customerID bson.ObjectID) error {
	master, err := s.master()
	if err != nil {
		return err
	}
	if customerID.IsZero() {
		if name == "" {
			return errors.New("姓名必填")
		}
		no, err := s.e.Seq.Next(ctx, tenantID, "PT")
		if err != nil {
			return err
		}
		cust, err := master.CreateCustomer(ctx, tenantID, contract.CustomerUpsert{
			Code: no, Name: name, Phone: phone,
		})
		if err != nil {
			return err
		}
		p.Code, p.CustomerID = no, cust.ID
	} else {
		cust, err := master.Customer(ctx, tenantID, customerID)
		if err != nil {
			return errors.New("客户不存在")
		}
		// 一客户一档案
		if n, _ := s.pats.Count(ctx, tenantID, bson.M{"customer_id": cust.ID}); n > 0 {
			return errors.New("该客户已有患者档案")
		}
		no, err := s.e.Seq.Next(ctx, tenantID, "PT")
		if err != nil {
			return err
		}
		p.Code, p.CustomerID = no, cust.ID
	}
	p.TenantID, p.CreatedAt = tenantID, time.Now()
	_, err = s.pats.Insert(ctx, tenantID, p)
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

func (s *Service) ListAppts(ctx context.Context, tenantID bson.ObjectID, date string, skip, limit int64) ([]model.Appointment, int64, error) {
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

// ApptsOfPatient 患者就诊记录。
func (s *Service) ApptsOfPatient(ctx context.Context, tenantID, patID bson.ObjectID) ([]model.Appointment, error) {
	return s.appts.FindMany(ctx, tenantID, bson.M{"patient_id": patID})
}

// TodayAppts 今日预约。
func (s *Service) TodayAppts(ctx context.Context, tenantID bson.ObjectID) ([]model.Appointment, error) {
	return s.appts.FindMany(ctx, tenantID, bson.M{"date": time.Now().Format("2006-01-02")})
}

// ---------- 价目表 ----------

func (s *Service) ListServiceItems(ctx context.Context, tenantID bson.ObjectID, activeOnly bool) ([]model.ServiceItem, error) {
	f := bson.M{}
	if activeOnly {
		f["status"] = "active"
	}
	return s.items.FindMany(ctx, tenantID, f)
}

func (s *Service) ServiceItemByID(ctx context.Context, tenantID, id bson.ObjectID) (*model.ServiceItem, error) {
	return s.items.FindByID(ctx, tenantID, id)
}

func (s *Service) CreateServiceItem(ctx context.Context, tenantID bson.ObjectID, it *model.ServiceItem) error {
	if it.Name == "" {
		return errors.New("项目名称必填")
	}
	if it.Code == "" {
		no, err := s.e.Seq.Next(ctx, tenantID, "SV")
		if err != nil {
			return err
		}
		it.Code = no
	} else if n, _ := s.items.Count(ctx, tenantID, bson.M{"code": it.Code}); n > 0 {
		return errors.New("编码已存在")
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
	if v, ok := set["price"]; ok {
		var price float64
		switch n := v.(type) {
		case float64:
			price = n
		case int:
			price = float64(n)
		}
		if price < 0 {
			return errors.New("单价不能为负")
		}
	}
	return s.items.Update(ctx, tenantID, id, set)
}

func (s *Service) DeleteServiceItem(ctx context.Context, tenantID, id bson.ObjectID) error {
	return s.items.Delete(ctx, tenantID, id)
}

// fillItems 按价目表回填明细快照（防前端改价），并汇总 Item 显示串与合计。
func (s *Service) fillItems(ctx context.Context, tenantID bson.ObjectID, in []model.ApptItem) ([]model.ApptItem, string, float64, error) {
	out := make([]model.ApptItem, 0, len(in))
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
		out = append(out, model.ApptItem{
			ServiceID: si.ID, Code: si.Code, Name: si.Name,
			Qty: l.Qty, Price: si.Price, Amount: amt,
		})
		total += amt
		if l.Qty > 1 {
			names = append(names, si.Name)
		} else {
			names = append(names, si.Name)
		}
	}
	summary := strings.Join(names, "、")
	return out, summary, total, nil
}

func (s *Service) CreateAppt(ctx context.Context, tenantID bson.ObjectID, a *model.Appointment) error {
	p, err := s.PatientByID(ctx, tenantID, a.PatientID)
	if err != nil {
		return errors.New("患者不存在")
	}
	// 快照患者姓名到预约单（列表显示免 join）
	if master, err := s.master(); err == nil {
		if c, err := master.Customer(ctx, tenantID, p.CustomerID); err == nil {
			a.PatientName = c.Name
		}
	}
	// 明细回填：有明细以价目表为准重算，兼容老单自由文本
	if len(a.Items) > 0 {
		items, summary, _, err := s.fillItems(ctx, tenantID, a.Items)
		if err != nil {
			return err
		}
		a.Items, a.Item = items, summary
	}
	a.TenantID, a.Status, a.CreatedAt = tenantID, model.ApptBooked, time.Now()
	_, err = s.appts.Insert(ctx, tenantID, a)
	return err
}

func (s *Service) setStatus(ctx context.Context, tenantID, id bson.ObjectID, from []string, to string) (*model.Appointment, error) {
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
	_, err := s.setStatus(ctx, tenantID, id, []string{model.ApptBooked}, model.ApptArrived)
	return err
}

func (s *Service) NoShow(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{model.ApptBooked}, model.ApptNoShow)
	return err
}

func (s *Service) Cancel(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{model.ApptBooked}, model.ApptCancel)
	return err
}

func (s *Service) ApptByID(ctx context.Context, tenantID, id bson.ObjectID) (*model.Appointment, error) {
	return s.appts.FindByID(ctx, tenantID, id)
}

// Complete 完成就诊并收费：优先按明细结算（items 非空则以价目表重算），
// 否则沿用预约单存量明细，再否则用手工 charge（兼容老单）。发 billing.charge 事件。
func (s *Service) Complete(ctx context.Context, tenantID, id bson.ObjectID, items []model.ApptItem, charge float64, by string) error {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if a.Status != model.ApptBooked && a.Status != model.ApptArrived {
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
	set := bson.M{"status": model.ApptDone, "charge": total, "items": finalItems, "item": summary}
	if total > 0 {
		no, err := s.e.Seq.Next(ctx, tenantID, "CH")
		if err != nil {
			return err
		}
		set["charge_no"] = no
		var partyID bson.ObjectID
		if p, err := s.PatientByID(ctx, tenantID, a.PatientID); err == nil {
			partyID = p.CustomerID
		}
		lines := make([]contract.ChargeLine, 0, len(finalItems))
		for _, it := range finalItems {
			lines = append(lines, contract.ChargeLine{
				Code: it.Code, Name: it.Name, Qty: it.Qty, Price: it.Price, Amount: it.Amount,
			})
		}
		s.e.Events.Publish(ctx, event.Event{
			Topic:    contract.TopicCharge,
			TenantID: tenantID,
			Payload: contract.Charge{
				DocNo: no, PartyID: partyID, PartyName: a.PatientName, Total: total, Lines: lines, By: by,
				RefType: "dental_appt", RefID: id.Hex(),
			},
		})
	}
	return s.appts.Update(ctx, tenantID, id, set)
}
