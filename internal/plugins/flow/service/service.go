package service

import (
	"context"
	"errors"
	"time"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	"erp/internal/platform/event"
	"erp/internal/platform/notify"
	"erp/internal/platform/repo"
	"erp/internal/plugins/flow/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var ErrBadStatus = errors.New("审批已处理")

type Service struct {
	e    *env.Env
	repo *repo.TenantRepo[model.Approval]
}

func New(e *env.Env) *Service {
	return &Service{e: e, repo: repo.NewTenantRepo[model.Approval](e.DB.Database, "plg_flow_approval")}
}

// OnSubmit doc.submit 事件 → 生成审批单。
func (s *Service) OnSubmit(ctx context.Context, tenantID bson.ObjectID, d contract.DocSubmit) error {
	a := &model.Approval{
		TargetType: d.TargetType, TargetID: d.TargetID,
		Title: d.Title, Status: model.StatusPending, By: d.By,
	}
	a.TenantID, a.CreatedAt = tenantID, time.Now()
	_, err := s.repo.Insert(ctx, tenantID, a)
	return err
}

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, status string, skip, limit int64) ([]model.Approval, int64, error) {
	f := bson.M{}
	if status != "" {
		f["status"] = status
	}
	total, err := s.repo.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.repo.FindMany(ctx, tenantID, f)
	return list, total, err
}

// Decide 审批 → 更新状态 + 发布结论事件 + 通知提交人。
func (s *Service) Decide(ctx context.Context, tenantID, id bson.ObjectID, approver, comment string, approved bool) error {
	a, err := s.repo.FindByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if a.Status != model.StatusPending {
		return ErrBadStatus
	}
	status := model.StatusApproved
	topic := contract.TopicFlowApproved
	if !approved {
		status = model.StatusRejected
		topic = contract.TopicFlowRejected
	}
	if err := s.repo.Update(ctx, tenantID, id, bson.M{
		"status": status, "approver": approver,
		"comment": comment, "decided_at": time.Now(),
	}); err != nil {
		return err
	}
	s.e.Events.Publish(ctx, event.Event{
		Topic:    topic,
		TenantID: tenantID,
		Payload: contract.FlowResult{
			TargetType: a.TargetType, TargetID: a.TargetID,
			Comment: comment, By: approver,
		},
	})
	// 通知提交人
	var u struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := s.e.DB.C("users").FindOne(ctx, bson.M{
		"tenant_id": tenantID, "username": a.By,
	}).Decode(&u); err == nil {
		verb := "通过"
		if !approved {
			verb = "驳回"
		}
		s.e.Notify.Send(ctx, &notify.Message{
			TenantID: tenantID, UserID: u.ID, Type: "todo",
			Title: "审批" + verb + "：" + a.Title, Link: "/app/flow/approvals",
		})
	}
	return nil
}
