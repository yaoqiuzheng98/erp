package contract

import (
	"context"
	"errors"

	"erp/internal/platform/env"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 主数据引用（跨插件只读视图，字段精简避免耦合文档结构）。
type ProductRef struct {
	ID       bson.ObjectID `json:"id"`
	Code     string        `json:"code"`
	Name     string        `json:"name"`
	Unit     string        `json:"unit"`
	Price    float64       `json:"price"`
	MinStock float64       `json:"min_stock"`
}

type WarehouseRef struct {
	ID   bson.ObjectID `json:"id"`
	Name string        `json:"name"`
}

type PartnerRef struct {
	ID    bson.ObjectID `json:"id"`
	Code  string        `json:"code"`
	Name  string        `json:"name"`
	Phone string        `json:"phone"`
}

// CustomerUpsert 创建客户的入参（跨插件写主数据的唯一通道）。
type CustomerUpsert struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Contact string `json:"contact"`
	Phone   string `json:"phone"`
}

// MasterDataAPI 由 basedata 插件实现，依赖它的插件经 env.Service 获取。
type MasterDataAPI interface {
	Product(ctx context.Context, tenantID, id bson.ObjectID) (*ProductRef, error)
	Products(ctx context.Context, tenantID bson.ObjectID) ([]ProductRef, error)
	Warehouses(ctx context.Context, tenantID bson.ObjectID) ([]WarehouseRef, error)
	Customer(ctx context.Context, tenantID, id bson.ObjectID) (*PartnerRef, error)
	Customers(ctx context.Context, tenantID bson.ObjectID) ([]PartnerRef, error)
	Suppliers(ctx context.Context, tenantID bson.ObjectID) ([]PartnerRef, error)
	CreateCustomer(ctx context.Context, tenantID bson.ObjectID, in CustomerUpsert) (*PartnerRef, error)
}

const MasterDataService = "basedata"

var ErrNoMasterData = errors.New("基础资料插件未启用或未注册服务")

// Master 从 Env 服务定位器取主数据 API。
func Master(e *env.Env) (MasterDataAPI, error) {
	v := e.Service(MasterDataService)
	if v == nil {
		return nil, ErrNoMasterData
	}
	api, ok := v.(MasterDataAPI)
	if !ok {
		return nil, ErrNoMasterData
	}
	return api, nil
}
