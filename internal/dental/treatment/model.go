package treatment

import (
	"strconv"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 状态机：billed（已开单待收）→ paid（收清）/ void（作废）。
// 一张预约最多一张诊疗单（appt_id 唯一），复诊重新约。
const (
	Billed = "billed"
	Paid   = "paid"
	Void   = "void"
)

// Item 诊疗明细行：开单时快照（与 appointment.ApptItem 同构但分包，避免循环引用）。
type Item struct {
	ServiceID bson.ObjectID `bson:"service_id"`
	Name      string        `bson:"name"`
	Qty       float64       `bson:"qty"`
	Price     float64       `bson:"price"`
	Amount    float64       `bson:"amount"`
}

// Treatment 诊疗单：一次接诊的临床记录（项目/病情/结果/总额）+ 收费状态。
// 费用单（bills）与之 1:1，钱的明细（多次多方式收款）全在 payments 里。
// appointment 上仍保留 charge/item 等显示快照（列表/旧模板用），权威值在这里。
type Treatment struct {
	model.Doc   `bson:",inline"`
	ApptID      bson.ObjectID `bson:"appt_id"`
	PatientID   bson.ObjectID `bson:"patient_id"`
	PatientName string        `bson:"patient_name"` // 快照，免 join
	DoctorID    bson.ObjectID `bson:"doctor_id,omitempty"`
	Doctor      string        `bson:"doctor"` // 快照
	Date        string        `bson:"date"`   // YYYY-MM-DD
	Slot        string        `bson:"slot"`   // HH:MM
	Item        string        `bson:"item"`   // 明细汇总
	Items       []Item        `bson:"items,omitempty"`
	Diagnosis   string        `bson:"diagnosis,omitempty"` // 病情分析
	Result      string        `bson:"result,omitempty"`    // 诊疗结果
	Total       float64       `bson:"total"`
	BillNo      string        `bson:"bill_no,omitempty"` // 对应费用单号（零收费无单）
	Status      string        `bson:"status"`            // billed / paid / void
}

// DisplayItem 列表展示用（与 appointment 同款逻辑，就诊记录用）。
func (t Treatment) DisplayItem() string {
	if len(t.Items) > 0 {
		names := make([]string, 0, len(t.Items))
		for _, it := range t.Items {
			n := it.Name
			if it.Qty > 1 {
				n += "x" + trimNum(it.Qty)
			}
			names = append(names, n)
		}
		s := ""
		for i, n := range names {
			if i > 0 {
				s += "、"
			}
			s += n
		}
		return s
	}
	return t.Item
}

func trimNum(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
