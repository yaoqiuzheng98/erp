package model

import (
	"time"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 单据类型
const (
	DocIn    = "in"    // 入库
	DocOut   = "out"   // 出库
	DocCheck = "check" // 盘点
)

// 单据状态（状态机：draft → confirmed / cancelled）
const (
	StatusDraft     = "draft"
	StatusConfirmed = "confirmed"
	StatusCancelled = "cancelled"
)

type Line struct {
	ProductID   bson.ObjectID `bson:"product_id"`
	ProductCode string        `bson:"product_code"`
	ProductName string        `bson:"product_name"`
	Qty         float64       `bson:"qty"`
}

// StockDoc 出入库/盘点单。
type StockDoc struct {
	model.Doc   `bson:",inline"`
	DocNo       string        `bson:"doc_no"`
	Type        string        `bson:"type"`
	WarehouseID bson.ObjectID `bson:"warehouse_id"`
	Lines       []Line        `bson:"lines"`
	Status      string        `bson:"status"`
	Remark      string        `bson:"remark"`
	RefNo       string        `bson:"ref_no,omitempty"` // 来源单号（如销售订单号）
	ConfirmedAt time.Time     `bson:"confirmed_at,omitempty"`
}

// Balance 库存余额（tenant+product+warehouse 唯一）。
type Balance struct {
	model.Doc   `bson:",inline"`
	ProductID   bson.ObjectID `bson:"product_id"`
	ProductCode string        `bson:"product_code"`
	ProductName string        `bson:"product_name"`
	WarehouseID bson.ObjectID `bson:"warehouse_id"`
	Qty         float64       `bson:"qty"`
}
