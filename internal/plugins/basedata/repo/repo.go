package repo

import (
	"context"

	"erp/internal/platform/repo"
	"erp/internal/plugins/basedata/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// 集合名遵循 plg_{pluginID}_{entity} 约定。

type ProductRepo struct {
	*repo.TenantRepo[model.Product]
}

func NewProductRepo(db *mongo.Database) *ProductRepo {
	return &ProductRepo{repo.NewTenantRepo[model.Product](db, "plg_basedata_product")}
}

func (r *ProductRepo) Search(ctx context.Context, tenantID bson.ObjectID, kw string, skip, limit int64) ([]model.Product, error) {
	f := bson.M{}
	if kw != "" {
		f["$or"] = bson.A{
			bson.M{"code": bson.M{"$regex": kw}},
			bson.M{"name": bson.M{"$regex": kw}},
		}
	}
	return r.FindMany(ctx, tenantID, f,
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).
			SetSkip(skip).SetLimit(limit))
}

func NewWarehouseRepo(db *mongo.Database) *repo.TenantRepo[model.Warehouse] {
	return repo.NewTenantRepo[model.Warehouse](db, "plg_basedata_warehouse")
}

func NewCustomerRepo(db *mongo.Database) *repo.TenantRepo[model.Customer] {
	return repo.NewTenantRepo[model.Customer](db, "plg_basedata_customer")
}

func NewSupplierRepo(db *mongo.Database) *repo.TenantRepo[model.Supplier] {
	return repo.NewTenantRepo[model.Supplier](db, "plg_basedata_supplier")
}
