package appointment

import (
	"fmt"
	"strconv"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 预约状态机：booked → arrived(候诊) → serving(就诊中) → unpaid(待缴费) → done；
// 分支 noshow / cancel。签到分配医生+排号；叫号只前端播报；点就诊进就诊中；
// 开单（unpaid）仅就诊中可做；前台收清后翻 done。
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
	Diagnosis   string        `bson:"diagnosis,omitempty"` // 病情分析/诊断（开单时填）
	Result      string        `bson:"result,omitempty"`    // 诊疗结果（开单时填）
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

// 放号档位默认值：半小时一档，每档 1 人（租户在门诊设置改）。
const (
	DefaultSlotMinutes  = 30
	DefaultSlotCapacity = 1
)

// Slot 放号档位选项：Value 下单用（桶起点 HH:MM），Label 展示用（09:00-09:30）。
type Slot struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// DaySlots 当天放号档位（8:00 起约，20:00 为最晚起约，非法粒度回落默认）。
func DaySlots(minutes int) []Slot {
	if minutes != 15 && minutes != 30 && minutes != 60 {
		minutes = DefaultSlotMinutes
	}
	var out []Slot
	for start := 8 * 60; start <= 20*60; start += minutes {
		end := start + minutes
		out = append(out, Slot{
			Value: fmt.Sprintf("%02d:%02d", start/60, start%60),
			Label: fmt.Sprintf("%02d:%02d-%02d:%02d", start/60, start%60, end/60, end%60),
		})
	}
	return out
}

// NormSlotConfig 下单配置归一（0/非法回落默认，防脏调用）。
func NormSlotConfig(minutes, capacity int) (int, int) {
	if minutes != 15 && minutes != 30 && minutes != 60 {
		minutes = DefaultSlotMinutes
	}
	if capacity < 1 || capacity > 10 {
		capacity = DefaultSlotCapacity
	}
	return minutes, capacity
}

// statusMeta 状态元数据（State 模式的数据驱动版）：中文名与徽章色收敛到一张表，
// 加状态只改这里，模板/流转天然同步，不会出现"名对了色错了"。
type statusMeta struct{ Name, Badge string }

var statusMetas = map[string]statusMeta{
	Booked:  {"已预约", "bg-primary"},
	Arrived: {"候诊中", "bg-info text-dark"},
	Serving: {"就诊中", "bg-success"},
	Unpaid:  {"待缴费", "bg-warning text-dark"},
	Done:    {"已完成", "bg-secondary"},
	NoShow:  {"爽约", "bg-warning text-dark"},
	Cancel:  {"已取消", "bg-secondary"},
}

// StatusName 状态中文名（模板调用）。
func (a Appointment) StatusName() string {
	if m, ok := statusMetas[a.Status]; ok {
		return m.Name
	}
	return a.Status
}

// Badge Bootstrap 颜色类（模板调用）。
func (a Appointment) Badge() string {
	if m, ok := statusMetas[a.Status]; ok {
		return m.Badge
	}
	return "bg-secondary"
}

func trimNum(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
