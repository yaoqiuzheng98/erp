package model

import (
	"erp/internal/platform/contract"
	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	StatusDraft     = "draft"
	StatusSubmitted = "submitted"
	StatusApproved  = "approved"
	StatusDone      = "done"
	StatusRejected  = "rejected"
	StatusCancelled = "cancelled"
)

// Order 采购订单。
type Order struct {
	model.Doc    `bson:",inline"`
	DocNo        string               `bson:"doc_no"`
	SupplierID   bson.ObjectID        `bson:"supplier_id"`
	SupplierName string               `bson:"supplier_name"`
	WarehouseID  bson.ObjectID        `bson:"warehouse_id"`
	Lines        []contract.OrderLine `bson:"lines"`
	Total        float64              `bson:"total"`
	Status       string               `bson:"status"`
	Remark       string               `bson:"remark"`
}
