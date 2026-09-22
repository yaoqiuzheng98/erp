package service

import (
	"context"
	"errors"
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
}

func New(e *env.Env) *Service {
	return &Service{
		e:     e,
		pats:  repo.NewTenantRepo[model.Patient](e.DB.Database, "plg_dental_patient"),
		appts: repo.NewTenantRepo[model.Appointment](e.DB.Database, "plg_dental_appt"),
	}
}

func (s *Service) ListPatients(ctx context.Context, tenantID bson.ObjectID, q string, skip, limit int64) ([]model.Patient, int64, error) {
	f := bson.M{}
	if q != "" {
		f["$or"] = []bson.M{
			{"name": bson.M{"$regex": q}},
			{"code": bson.M{"$regex": q}},
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

func (s *Service) PatientByID(ctx context.Context, tenantID, id bson.ObjectID) (*model.Patient, error) {
	return s.pats.FindByID(ctx, tenantID, id)
}

func (s *Service) CreatePatient(ctx context.Context, tenantID bson.ObjectID, p *model.Patient) error {
	no, err := s.e.Seq.Next(ctx, tenantID, "PT")
	if err != nil {
		return err
	}
	p.Code = no
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

func (s *Service) CreateAppt(ctx context.Context, tenantID bson.ObjectID, a *model.Appointment) error {
	p, err := s.PatientByID(ctx, tenantID, a.PatientID)
	if err != nil {
		return errors.New("患者不存在")
	}
	a.PatientName = p.Name
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

// Complete 完成就诊并收费：发 billing.charge 事件，财务启用时生成应收。
func (s *Service) Complete(ctx context.Context, tenantID, id bson.ObjectID, charge float64, by string) error {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if a.Status != model.ApptBooked && a.Status != model.ApptArrived {
		return ErrBadStatus
	}
	if charge < 0 {
		return errors.New("收费额不能为负")
	}
	set := bson.M{"status": model.ApptDone, "charge": charge}
	if charge > 0 {
		no, err := s.e.Seq.Next(ctx, tenantID, "CH")
		if err != nil {
			return err
		}
		set["charge_no"] = no
		s.e.Events.Publish(ctx, event.Event{
			Topic:    contract.TopicCharge,
			TenantID: tenantID,
			Payload: contract.Charge{
				DocNo: no, PartyName: a.PatientName, Total: charge, By: by,
				RefType: "dental_appt", RefID: id.Hex(),
			},
		})
	}
	return s.appts.Update(ctx, tenantID, id, set)
}
