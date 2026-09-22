package service

import (
	"context"
	"errors"
	"time"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	"erp/internal/platform/event"
	"erp/internal/platform/repo"
	"erp/internal/plugins/purchase/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var ErrBadStatus = errors.New("订单状态不允许该操作")

type Service struct {
	e    *env.Env
	repo *repo.TenantRepo[model.Order]
}

func New(e *env.Env) *Service {
	return &Service{e: e, repo: repo.NewTenantRepo[model.Order](e.DB.Database, "plg_purchase_order")}
}

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, skip, limit int64) ([]model.Order, int64, error) {
	total, err := s.repo.Count(ctx, tenantID, bson.M{})
	if err != nil {
		return nil, 0, err
	}
	list, err := s.repo.FindMany(ctx, tenantID, bson.M{})
	return list, total, err
}

func (s *Service) ByID(ctx context.Context, tenantID, id bson.ObjectID) (*model.Order, error) {
	return s.repo.FindByID(ctx, tenantID, id)
}

func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, o *model.Order, by string) error {
	if len(o.Lines) == 0 {
		return errors.New("订单至少一行")
	}
	no, err := s.e.Seq.Next(ctx, tenantID, "PO")
	if err != nil {
		return err
	}
	var total float64
	for _, l := range o.Lines {
		total += l.Qty * l.Price
	}
	o.DocNo, o.Total = no, total
	o.TenantID, o.Status = tenantID, model.StatusDraft
	o.CreatedAt, o.CreatedBy = time.Now(), by
	o.ID, err = s.repo.Insert(ctx, tenantID, o)
	return err
}

func (s *Service) Submit(ctx context.Context, tenantID, id bson.ObjectID, by string) error {
	o, err := s.ByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if o.Status != model.StatusDraft && o.Status != model.StatusRejected {
		return ErrBadStatus
	}
	if s.e.Gate.IsEnabled(ctx, tenantID, "flow") {
		if err := s.repo.Update(ctx, tenantID, id, bson.M{"status": model.StatusSubmitted}); err != nil {
			return err
		}
		s.e.Events.Publish(ctx, event.Event{
			Topic:    contract.TopicDocSubmit,
			TenantID: tenantID,
			Payload: contract.DocSubmit{
				TargetType: "purchase_order", TargetID: id.Hex(),
				Title: "采购订单 " + o.DocNo, By: by,
			},
		})
		return nil
	}
	return s.approve(ctx, tenantID, o, by)
}

func (s *Service) Approve(ctx context.Context, tenantID, id bson.ObjectID, by string) error {
	o, err := s.ByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	return s.approve(ctx, tenantID, o, by)
}

func (s *Service) approve(ctx context.Context, tenantID bson.ObjectID, o *model.Order, by string) error {
	if o.Status != model.StatusDraft && o.Status != model.StatusSubmitted {
		return ErrBadStatus
	}
	if err := s.repo.Update(ctx, tenantID, o.ID, bson.M{"status": model.StatusApproved}); err != nil {
		return err
	}
	s.e.Events.Publish(ctx, event.Event{
		Topic:    contract.TopicPurchaseApproved,
		TenantID: tenantID,
		Payload: contract.OrderApproved{
			OrderID: o.ID.Hex(), DocNo: o.DocNo,
			PartnerID: o.SupplierID, Total: o.Total,
			Lines: o.Lines, By: by,
		},
	})
	return nil
}

func (s *Service) OnFlowResult(ctx context.Context, tenantID bson.ObjectID, r contract.FlowResult, approved bool) error {
	id, err := bson.ObjectIDFromHex(r.TargetID)
	if err != nil {
		return err
	}
	if approved {
		return s.Approve(ctx, tenantID, id, r.By)
	}
	o, err := s.ByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if o.Status != model.StatusSubmitted {
		return nil
	}
	return s.repo.Update(ctx, tenantID, id, bson.M{"status": model.StatusRejected})
}

func (s *Service) Done(ctx context.Context, tenantID, id bson.ObjectID) error {
	o, err := s.ByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if o.Status != model.StatusApproved {
		return ErrBadStatus
	}
	return s.repo.Update(ctx, tenantID, id, bson.M{"status": model.StatusDone})
}

func (s *Service) Cancel(ctx context.Context, tenantID, id bson.ObjectID) error {
	o, err := s.ByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if o.Status == model.StatusDone || o.Status == model.StatusApproved {
		return ErrBadStatus
	}
	return s.repo.Update(ctx, tenantID, id, bson.M{"status": model.StatusCancelled})
}
