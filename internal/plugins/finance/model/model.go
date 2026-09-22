package model

import (
	"time"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	BillAR = "ar" // 应收
	BillAP = "ap" // 应付

	BillOpen = "open"
	BillPaid = "paid"

	PayReceipt = "receipt" // 收款（核销应收）
	PayOut     = "payment" // 付款（核销应付）
)

// Bill 应收/应付单，订单审核事件自动生成。
type Bill struct {
	model.Doc   `bson:",inline"`
	Type        string        `bson:"type"`
	PartnerID   bson.ObjectID `bson:"partner_id"`
	PartnerName string        `bson:"partner_name"`
	OrderID     string        `bson:"order_id"`
	DocNo       string        `bson:"doc_no"`
	Amount      float64       `bson:"amount"`
	PaidAmount  float64       `bson:"paid_amount"`
	Status      string        `bson:"status"`
}

// Payment 收付款单（核销 Bill）。
type Payment struct {
	model.Doc `bson:",inline"`
	DocNo     string        `bson:"doc_no"`
	Type      string        `bson:"type"`
	BillID    bson.ObjectID `bson:"bill_id"`
	BillDocNo string        `bson:"bill_doc_no"`
	Amount    float64       `bson:"amount"`
	Method    string        `bson:"method"` // cash / bank / other
	PaidAt    time.Time     `bson:"paid_at"`
}

// Expense 费用单。
type Expense struct {
	model.Doc `bson:",inline"`
	Title     string    `bson:"title"`
	Amount    float64   `bson:"amount"`
	Category  string    `bson:"category"`
	At        time.Time `bson:"at"`
}
