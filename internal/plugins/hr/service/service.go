package service

import (
	"context"
	"errors"
	"time"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	"erp/internal/platform/event"
	"erp/internal/platform/repo"
	"erp/internal/plugins/hr/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var ErrBadStatus = errors.New("状态不允许该操作")

type Service struct {
	e      *env.Env
	emps   *repo.TenantRepo[model.Employee]
	attend *repo.TenantRepo[model.Attend]
	leaves *repo.TenantRepo[model.Leave]
}

func New(e *env.Env) *Service {
	return &Service{
		e:      e,
		emps:   repo.NewTenantRepo[model.Employee](e.DB.Database, "plg_hr_employee"),
		attend: repo.NewTenantRepo[model.Attend](e.DB.Database, "plg_hr_attend"),
		leaves: repo.NewTenantRepo[model.Leave](e.DB.Database, "plg_hr_leave"),
	}
}

func (s *Service) ListEmployees(ctx context.Context, tenantID bson.ObjectID) ([]model.Employee, error) {
	return s.emps.FindMany(ctx, tenantID, bson.M{})
}

func (s *Service) CreateEmployee(ctx context.Context, tenantID bson.ObjectID, em *model.Employee) error {
	em.TenantID, em.CreatedAt, em.Status = tenantID, time.Now(), "active"
	_, err := s.emps.Insert(ctx, tenantID, em)
	return err
}

func (s *Service) ListAttends(ctx context.Context, tenantID bson.ObjectID, skip, limit int64) ([]model.Attend, int64, error) {
	total, err := s.attend.Count(ctx, tenantID, bson.M{})
	if err != nil {
		return nil, 0, err
	}
	list, err := s.attend.FindMany(ctx, tenantID, bson.M{})
	return list, total, err
}

func (s *Service) CreateAttend(ctx context.Context, tenantID bson.ObjectID, a *model.Attend) error {
	a.TenantID, a.CreatedAt = tenantID, time.Now()
	_, err := s.attend.Insert(ctx, tenantID, a)
	return err
}

func (s *Service) ListLeaves(ctx context.Context, tenantID bson.ObjectID, skip, limit int64) ([]model.Leave, int64, error) {
	total, err := s.leaves.Count(ctx, tenantID, bson.M{})
	if err != nil {
		return nil, 0, err
	}
	list, err := s.leaves.FindMany(ctx, tenantID, bson.M{})
	return list, total, err
}

func (s *Service) LeaveByID(ctx context.Context, tenantID, id bson.ObjectID) (*model.Leave, error) {
	return s.leaves.FindByID(ctx, tenantID, id)
}

func (s *Service) CreateLeave(ctx context.Context, tenantID bson.ObjectID, l *model.Leave, by string) error {
	no, err := s.e.Seq.Next(ctx, tenantID, "LV")
	if err != nil {
		return err
	}
	l.DocNo = no
	l.TenantID, l.Status = tenantID, model.LeaveDraft
	l.CreatedAt, l.CreatedBy = time.Now(), by
	_, err = s.leaves.Insert(ctx, tenantID, l)
	return err
}

// Submit 请假提交：flow 启用走审批，否则直接通过。
func (s *Service) SubmitLeave(ctx context.Context, tenantID, id bson.ObjectID, by string) error {
	l, err := s.LeaveByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if l.Status != model.LeaveDraft && l.Status != model.LeaveRejected {
		return ErrBadStatus
	}
	if s.e.Gate.IsEnabled(ctx, tenantID, "flow") {
		if err := s.leaves.Update(ctx, tenantID, id, bson.M{"status": model.LeaveSubmitted}); err != nil {
			return err
		}
		s.e.Events.Publish(ctx, event.Event{
			Topic:    contract.TopicDocSubmit,
			TenantID: tenantID,
			Payload: contract.DocSubmit{
				TargetType: "hr_leave", TargetID: id.Hex(),
				Title: "请假单 " + l.DocNo + "（" + l.EmpName + "）", By: by,
			},
		})
		return nil
	}
	return s.leaves.Update(ctx, tenantID, id, bson.M{"status": model.LeaveApproved})
}

func (s *Service) OnFlowResult(ctx context.Context, tenantID bson.ObjectID, r contract.FlowResult, approved bool) error {
	id, err := bson.ObjectIDFromHex(r.TargetID)
	if err != nil {
		return err
	}
	status := model.LeaveApproved
	if !approved {
		status = model.LeaveRejected
	}
	return s.leaves.Update(ctx, tenantID, id, bson.M{"status": status})
}
