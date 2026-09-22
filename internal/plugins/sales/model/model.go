package model

import (
	"erp/internal/platform/contract"
	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 状态机：draft → submitted(审批中) → approved → done；分支 rejected / cancelled
const (
	StatusDraft     = "draft"
	StatusSubmitted = "submitted"
	StatusApproved  = "approved"
	StatusDone      = "done"
	StatusRejected  = "rejected"
	StatusCancelled = "cancelled"
)

// Order 销售订单。
type Order struct {
	model.Doc    `bson:",inline"`
	DocNo        string              `bson:"doc_no"`
	CustomerID   bson.ObjectID       `bson:"customer_id"`
	CustomerName string              `bson:"customer_name"`
	WarehouseID  bson.ObjectID       `bson:"warehouse_id"`
	Lines        []contract.OrderLine `bson:"lines"`
	Total        float64             `bson:"total"`
	Status       string              `bson:"status"`
	Remark       string              `bson:"remark"`
}
