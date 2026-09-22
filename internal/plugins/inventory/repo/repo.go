package repo

import (
	"context"
	"time"

	"erp/internal/platform/repo"
	"erp/internal/plugins/inventory/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type DocRepo struct {
	*repo.TenantRepo[model.StockDoc]
}

type BalanceRepo struct {
	*repo.TenantRepo[model.Balance]
}

func NewDocRepo(db *mongo.Database) *DocRepo {
	return &DocRepo{repo.NewTenantRepo[model.StockDoc](db, "plg_inventory_doc")}
}

func NewBalanceRepo(db *mongo.Database) *BalanceRepo {
	return &BalanceRepo{repo.NewTenantRepo[model.Balance](db, "plg_inventory_balance")}
}

func (r *DocRepo) List(ctx context.Context, tenantID bson.ObjectID, typ string, skip, limit int64) ([]model.StockDoc, int64, error) {
	f := bson.M{}
	if typ != "" {
		f["type"] = typ
	}
	total, err := r.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := r.FindMany(ctx, tenantID, f,
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetSkip(skip).SetLimit(limit))
	return list, total, err
}

func (r *BalanceRepo) List(ctx context.Context, tenantID bson.ObjectID, skip, limit int64) ([]model.Balance, int64, error) {
	total, err := r.Count(ctx, tenantID, bson.M{})
	if err != nil {
		return nil, 0, err
	}
	list, err := r.FindMany(ctx, tenantID, bson.M{},
		options.Find().SetSort(bson.D{{Key: "product_code", Value: 1}}).SetSkip(skip).SetLimit(limit))
	return list, total, err
}

// Get 取某商品在某仓库的余额，不存在返回 nil。
func (r *BalanceRepo) Get(ctx context.Context, tenantID, productID, warehouseID bson.ObjectID) (*model.Balance, error) {
	b, err := r.FindOne(ctx, tenantID, bson.M{"product_id": productID, "warehouse_id": warehouseID})
	if err != nil {
		return nil, err
	}
	return b, nil
}

// AddQty 原子增减余额，upsert。
func (r *BalanceRepo) AddQty(ctx context.Context, tenantID, productID, warehouseID bson.ObjectID, code, name string, delta float64) error {
	_, err := r.Col.UpdateOne(ctx,
		bson.M{"tenant_id": tenantID, "product_id": productID, "warehouse_id": warehouseID},
		bson.M{
			"$inc": bson.M{"qty": delta},
			"$set": bson.M{"product_code": code, "product_name": name, "updated_at": time.Now()},
			"$setOnInsert": bson.M{
				"tenant_id": tenantID, "product_id": productID,
				"warehouse_id": warehouseID, "created_at": time.Now(),
			},
		},
		options.UpdateOne().SetUpsert(true))
	return err
}

func (r *BalanceRepo) All(ctx context.Context, tenantID bson.ObjectID) ([]model.Balance, error) {
	return r.FindMany(ctx, tenantID, bson.M{})
}
