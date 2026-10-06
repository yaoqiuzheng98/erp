package billing

import (
	"time"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	BillOpen = "open"
	BillPaid = "paid"
	BillVoid = "void" // 作废：收不回来的单，不再计入未收
)

// BillLine 应收明细（价目快照）。
type BillLine struct {
	Name   string  `bson:"name"`
	Qty    float64 `bson:"qty"`
	Price  float64 `bson:"price"`
	Amount float64 `bson:"amount"`
}

// Bill 应收单：只收患者欠费（无应付）。DocNo 租户内唯一，防重单。
type Bill struct {
	model.Doc   `bson:",inline"`
	PatientID   bson.ObjectID `bson:"patient_id"`
	PatientName string        `bson:"patient_name"`
	DocNo       string        `bson:"doc_no"`
	Amount      float64       `bson:"amount"`
	Lines       []BillLine    `bson:"lines,omitempty"`
	PaidAmount  float64       `bson:"paid_amount"`
	Status      string        `bson:"status"` // open / paid / void
	RefID       string        `bson:"ref_id"` // 来源预约 hex
}

// billStatusNames 单据状态中文名（与预约状态表同构，加状态只改这里）。
var billStatusNames = map[string]string{
	BillPaid: "已结清",
	BillVoid: "已作废",
}

// StatusName 状态中文名（模板调用），默认待收款。
func (b Bill) StatusName() string {
	if n, ok := billStatusNames[b.Status]; ok {
		return n
	}
	return "待收款"
}

// Payment 收款单（核销应收）。
type Payment struct {
	model.Doc `bson:",inline"`
	DocNo     string        `bson:"doc_no"`
	BillID    bson.ObjectID `bson:"bill_id"`
	BillDocNo string        `bson:"bill_doc_no"`
	Amount    float64       `bson:"amount"`
	Method    string        `bson:"method"` // cash / bank / other
	PaidAt    time.Time     `bson:"paid_at"`
}

// AR 创建应收入参。
type AR struct {
	PatientID   bson.ObjectID
	PatientName string
	DocNo       string
	Amount      float64
	Lines       []BillLine
	RefID       string
	By          string
}
