package appointment

import (
	"strconv"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 预约状态机：booked → arrived(候诊) → serving(就诊中) → unpaid(待缴费) → done；
// 分支 noshow / cancel。签到分配医生+排号；开单（unpaid）时自动叫同医生下一位；
// 前台收清后翻 done。
const (
	Booked  = "booked"
	Arrived = "arrived"
	Serving = "serving"
	Unpaid  = "unpaid"
	Done    = "done"
	NoShow  = "noshow"
	Cancel  = "cancel"
)

// ApptItem 预约/结算明细行：下单时快照 Name/Price，防价目改价影响历史单。
type ApptItem struct {
	ServiceID bson.ObjectID `bson:"service_id"`
	Name      string        `bson:"name"`
	Qty       float64       `bson:"qty"`
	Price     float64       `bson:"price"`
	Amount    float64       `bson:"amount"` // Qty*Price
}

// Appointment 预约（按医生/时段排）。
type Appointment struct {
	model.Doc   `bson:",inline"`
	PatientID   bson.ObjectID `bson:"patient_id"`
	PatientName string        `bson:"patient_name"` // 快照，免 join
	DoctorID    bson.ObjectID `bson:"doctor_id,omitempty"`
	Doctor      string        `bson:"doctor"` // 接诊医生快照（签到时最终确定）
	Date        string        `bson:"date"` // YYYY-MM-DD
	Slot        string        `bson:"slot"` // HH:MM
	Item        string        `bson:"item"` // 自由文本兜底（无价目时填）
	Items       []ApptItem    `bson:"items,omitempty"`
	QueueNo     int           `bson:"queue_no,omitempty"` // 当天当医生排号（签到分配）
	RegFee      float64       `bson:"reg_fee,omitempty"`    // 挂号费快照（预约时）
	RegPaid     bool          `bson:"reg_paid,omitempty"`   // 挂号费已缴
	Status      string        `bson:"status"`
	Charge      float64       `bson:"charge"` // 完成时收费总额
	ChargeNo    string        `bson:"charge_no"`
}

// DisplayItem 列表展示用：有明细显示明细名，否则回落 Item。
func (a Appointment) DisplayItem() string {
	if len(a.Items) > 0 {
		names := make([]string, 0, len(a.Items))
		for _, it := range a.Items {
			if it.Qty > 1 {
				names = append(names, it.Name+"x"+trimNum(it.Qty))
			} else {
				names = append(names, it.Name)
			}
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
	return a.Item
}

// Total 明细合计（无明细回落 Charge）。
func (a Appointment) Total() float64 {
	if len(a.Items) > 0 {
		var s float64
		for _, it := range a.Items {
			s += it.Amount
		}
		return s
	}
	return a.Charge
}

// StatusName 状态中文名（模板调用）。
func (a Appointment) StatusName() string {
	switch a.Status {
	case Booked:
		return "已预约"
	case Arrived:
		return "候诊中"
	case Serving:
		return "就诊中"
	case Unpaid:
		return "待缴费"
	case Done:
		return "已完成"
	case NoShow:
		return "爽约"
	case Cancel:
		return "已取消"
	}
	return a.Status
}

// Badge Bootstrap 颜色类（模板调用）。
func (a Appointment) Badge() string {
	switch a.Status {
	case Booked:
		return "bg-primary"
	case Arrived:
		return "bg-info text-dark"
	case Serving:
		return "bg-success"
	case Unpaid:
		return "bg-warning text-dark"
	case Done:
		return "bg-secondary"
	case NoShow:
		return "bg-warning text-dark"
	case Cancel:
		return "bg-secondary"
	}
	return "bg-secondary"
}

func trimNum(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
