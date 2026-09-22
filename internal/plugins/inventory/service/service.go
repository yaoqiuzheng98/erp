package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	"erp/internal/platform/event"
	"erp/internal/platform/notify"
	"erp/internal/plugins/inventory/model"
	"erp/internal/plugins/inventory/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	ErrBadStatus  = errors.New("单据状态不允许该操作")
	ErrNoLines    = errors.New("单据至少一行")
	ErrOutOfStock = errors.New("库存不足")
)

// Service 库存服务；e 用于发号器与事件总线。
type Service struct {
	e    *env.Env
	docs *repo.DocRepo
	bal  *repo.BalanceRepo
}

func New(e *env.Env) *Service {
	return &Service{
		e:    e,
		docs: repo.NewDocRepo(e.DB.Database),
		bal:  repo.NewBalanceRepo(e.DB.Database),
	}
}

func (s *Service) ListDocs(ctx context.Context, tenantID bson.ObjectID, typ string, skip, limit int64) ([]model.StockDoc, int64, error) {
	return s.docs.List(ctx, tenantID, typ, skip, limit)
}

func (s *Service) DocByID(ctx context.Context, tenantID, id bson.ObjectID) (*model.StockDoc, error) {
	return s.docs.FindByID(ctx, tenantID, id)
}

func (s *Service) Balances(ctx context.Context, tenantID bson.ObjectID, skip, limit int64) ([]model.Balance, int64, error) {
	return s.bal.List(ctx, tenantID, skip, limit)
}

var ruleOf = map[string]string{
	model.DocIn: "IN", model.DocOut: "OUT", model.DocCheck: "CHK",
}

// CreateDoc 创建草稿单，按类型发号。
func (s *Service) CreateDoc(ctx context.Context, tenantID bson.ObjectID, d *model.StockDoc, by string) error {
	if len(d.Lines) == 0 {
		return ErrNoLines
	}
	rule := ruleOf[d.Type]
	if rule == "" {
		rule = "IO"
	}
	no, err := s.e.Seq.Next(ctx, tenantID, rule)
	if err != nil {
		return err
	}
	d.DocNo = no
	d.TenantID = tenantID
	d.Status = model.StatusDraft
	d.CreatedAt = time.Now()
	d.CreatedBy = by
	d.ID, err = s.docs.Insert(ctx, tenantID, d)
	return err
}

// Confirm 状态机 draft→confirmed，应用库存变动（先全量校验再落账）。
func (s *Service) Confirm(ctx context.Context, tenantID, id bson.ObjectID) error {
	d, err := s.docs.FindByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if d.Status != model.StatusDraft {
		return ErrBadStatus
	}
	if err := s.apply(ctx, tenantID, d); err != nil {
		return err
	}
	return s.docs.Update(ctx, tenantID, id, bson.M{
		"status": model.StatusConfirmed, "confirmed_at": time.Now(),
	})
}

// apply 校验并落库存：in +qty / out -qty（先校验）/ check 盘点设为实盘数。
func (s *Service) apply(ctx context.Context, tenantID bson.ObjectID, d *model.StockDoc) error {
	// 第一阶段：出库校验库存是否足够
	if d.Type == model.DocOut {
		for _, l := range d.Lines {
			b, err := s.bal.Get(ctx, tenantID, l.ProductID, d.WarehouseID)
			cur := 0.0
			if err == nil {
				cur = b.Qty
			}
			if cur < l.Qty {
				return fmt.Errorf("%w: %s 当前 %.2f 需 %.2f", ErrOutOfStock, l.ProductName, cur, l.Qty)
			}
		}
	}
	for _, l := range d.Lines {
		var delta float64
		switch d.Type {
		case model.DocIn:
			delta = l.Qty
		case model.DocOut:
			delta = -l.Qty
		case model.DocCheck:
			b, err := s.bal.Get(ctx, tenantID, l.ProductID, d.WarehouseID)
			cur := 0.0
			if err == nil {
				cur = b.Qty
			}
			delta = l.Qty - cur
		}
		if err := s.bal.AddQty(ctx, tenantID, l.ProductID, d.WarehouseID,
			l.ProductCode, l.ProductName, delta); err != nil {
			return err
		}
		s.e.Events.Publish(ctx, event.Event{
			Topic:    contract.TopicStockChanged,
			TenantID: tenantID,
			Payload: contract.StockChanged{
				ProductID: l.ProductID, WarehouseID: d.WarehouseID,
				Delta: delta, DocNo: d.DocNo,
			},
		})
	}
	return nil
}

func (s *Service) Cancel(ctx context.Context, tenantID, id bson.ObjectID) error {
	d, err := s.docs.FindByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if d.Status != model.StatusDraft {
		return ErrBadStatus
	}
	return s.docs.Update(ctx, tenantID, id, bson.M{"status": model.StatusCancelled})
}

// ---------- 事件驱动的自动单据 ----------

// AutoOutbound 销售订单审核 → 自动生成并确认出库单；库存不足则留草稿并通知管理员。
func (s *Service) AutoOutbound(ctx context.Context, tenantID bson.ObjectID, o contract.OrderApproved) error {
	d := &model.StockDoc{
		Type: model.DocOut, RefNo: o.DocNo, Remark: "销售订单自动生成",
	}
	for _, l := range o.Lines {
		d.WarehouseID = l.WarehouseID
		d.Lines = append(d.Lines, model.Line{
			ProductID: l.ProductID, ProductCode: l.ProductCode,
			ProductName: l.ProductName, Qty: l.Qty,
		})
	}
	if err := s.CreateDoc(ctx, tenantID, d, "system"); err != nil {
		return err
	}
	if err := s.Confirm(ctx, tenantID, d.ID); err != nil {
		s.notifyAdmins(ctx, tenantID, fmt.Sprintf("出库单 %s 确认失败: %v", d.DocNo, err), "/app/inventory/docs")
	}
	return nil
}

// AutoInbound 采购订单审核 → 自动生成并确认入库单。
func (s *Service) AutoInbound(ctx context.Context, tenantID bson.ObjectID, o contract.OrderApproved) error {
	d := &model.StockDoc{
		Type: model.DocIn, RefNo: o.DocNo, Remark: "采购订单自动生成",
	}
	for _, l := range o.Lines {
		d.WarehouseID = l.WarehouseID
		d.Lines = append(d.Lines, model.Line{
			ProductID: l.ProductID, ProductCode: l.ProductCode,
			ProductName: l.ProductName, Qty: l.Qty,
		})
	}
	if err := s.CreateDoc(ctx, tenantID, d, "system"); err != nil {
		return err
	}
	return s.Confirm(ctx, tenantID, d.ID)
}

// ---------- 库存预警任务 ----------

// CheckLowStock 周期任务：余额低于商品库存下限 → 通知租户管理员。
func (s *Service) CheckLowStock(ctx context.Context) error {
	master, err := contract.Master(s.e)
	if err != nil {
		return nil // basedata 未注册则跳过
	}
	// 找出启用了 inventory 的租户
	cur, err := s.e.DB.C("tenant_plugins").Find(ctx,
		bson.M{"plugin_id": "inventory", "enabled": true})
	if err != nil {
		return err
	}
	var tps []struct {
		TenantID bson.ObjectID `bson:"tenant_id"`
	}
	if err := cur.All(ctx, &tps); err != nil {
		return err
	}
	for _, tp := range tps {
		products, err := master.Products(ctx, tp.TenantID)
		if err != nil {
			continue
		}
		minOf := map[bson.ObjectID]float64{}
		for _, p := range products {
			minOf[p.ID] = p.MinStock
		}
		balances, err := s.bal.All(ctx, tp.TenantID)
		if err != nil {
			continue
		}
		for _, b := range balances {
			if min := minOf[b.ProductID]; min > 0 && b.Qty < min {
				s.notifyAdmins(ctx, tp.TenantID,
					fmt.Sprintf("库存预警：%s 余量 %.2f 低于下限 %.2f", b.ProductName, b.Qty, min),
					"/app/inventory/balances")
			}
		}
	}
	return nil
}

func (s *Service) notifyAdmins(ctx context.Context, tenantID bson.ObjectID, title, link string) {
	cur, err := s.e.DB.C("users").Find(ctx,
		bson.M{"tenant_id": tenantID, "is_tenant_admin": true, "status": "active"})
	if err != nil {
		return
	}
	var admins []struct {
		ID bson.ObjectID `bson:"_id"`
	}
	if err := cur.All(ctx, &admins); err != nil {
		return
	}
	for _, a := range admins {
		s.e.Notify.Send(ctx, &notify.Message{
			TenantID: tenantID, UserID: a.ID, Type: "alert", Title: title, Link: link,
		})
	}
}

// EnsureIndexes OnInstall 钩子。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	_, err := s.bal.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "tenant_id", Value: 1},
			{Key: "product_id", Value: 1},
			{Key: "warehouse_id", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	})
	return err
}
