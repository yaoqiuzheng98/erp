package dental

import (
	"strconv"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 预约状态机：booked → arrived → done；分支 noshow / cancel
const (
	ApptBooked  = "booked"
	ApptArrived = "arrived"
	ApptDone    = "done"
	ApptNoShow  = "noshow"
	ApptCancel  = "cancel"
)

// 牙位状态（FDI 编号）；缺省视为健康。
const (
	ToothCaries  = "caries"
	ToothMissing = "missing"
	ToothTreated = "treated"
	ToothImplant = "implant"
	ToothCrown   = "crown"
)

// ToothStatuses 状态 → 显示名（牙位图图例）。
var ToothStatuses = map[string]string{
	ToothCaries:  "龋坏",
	ToothMissing: "缺失",
	ToothTreated: "已治",
	ToothImplant: "种植",
	ToothCrown:   "修复",
}

// 员工角色。
const (
	StaffDoctor  = "doctor"
	StaffNurse   = "nurse"
	StaffFront   = "front"
	StaffAssistant = "assistant"
)

// StaffRoles 角色 → 显示名。
var StaffRoles = map[string]string{
	StaffDoctor:    "医生",
	StaffNurse:     "护士",
	StaffFront:     "前台",
	StaffAssistant: "助理",
}

// Patient 患者档案：姓名+电话直接标识，无客户中间层。
type Patient struct {
	model.Doc  `bson:",inline"`
	Name       string            `bson:"name"`
	Phone      string            `bson:"phone"`
	Gender     string            `bson:"gender"`
	Birth      string            `bson:"birth"`
	Allergy    string            `bson:"allergy"`
	History    string            `bson:"history"`
	Note       string            `bson:"note"`
	Teeth      map[string]string `bson:"teeth"`
}

// ServiceItem 诊疗价目：门诊主数据。药品少量直接建 Category=药品 的条目，
// 按次收费不走库存。以名称标识，租户内唯一。
type ServiceItem struct {
	model.Doc `bson:",inline"`
	Name      string  `bson:"name"`
	Category  string  `bson:"category"`
	Price     float64 `bson:"price"`
	Unit      string  `bson:"unit"`
	Status    string  `bson:"status"` // active / disabled
}

// Staff 医护花名册：医生/护士/前台/助理。预约选医生时只列在职医生。
type Staff struct {
	model.Doc `bson:",inline"`
	Name      string `bson:"name"`   // 租户内唯一
	Role      string `bson:"role"`   // doctor / nurse / front / assistant
	Phone     string `bson:"phone"`
	Status    string `bson:"status"` // active / disabled
}

// ApptItem 预约/结算明细行：下单时快照 Name/Price，防价目改价影响历史单。
type ApptItem struct {
	ServiceID bson.ObjectID `bson:"service_id"`
	Name      string        `bson:"name"`
	Qty       float64       `bson:"qty"`
	Price     float64       `bson:"price"`
	Amount    float64       `bson:"amount"` // Qty*Price
}

// Appointment 预约（按椅位/医生/时段排）。
type Appointment struct {
	model.Doc   `bson:",inline"`
	PatientID   bson.ObjectID `bson:"patient_id"`
	PatientName string        `bson:"patient_name"` // 快照，免 join
	DoctorID    bson.ObjectID `bson:"doctor_id"`
	Doctor      string        `bson:"doctor"` // 接诊医生快照
	Chair       string        `bson:"chair"`
	Date        string        `bson:"date"` // YYYY-MM-DD
	Slot        string        `bson:"slot"` // HH:MM
	Item        string        `bson:"item"` // 自由文本兜底（无价目时填）
	Items       []ApptItem    `bson:"items,omitempty"`
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
	case ApptBooked:
		return "已预约"
	case ApptArrived:
		return "已到诊"
	case ApptDone:
		return "已完成"
	case ApptNoShow:
		return "爽约"
	case ApptCancel:
		return "已取消"
	}
	return a.Status
}

// Badge Bootstrap 颜色类（模板调用）。
func (a Appointment) Badge() string {
	switch a.Status {
	case ApptBooked:
		return "bg-primary"
	case ApptArrived:
		return "bg-info text-dark"
	case ApptDone:
		return "bg-success"
	case ApptNoShow:
		return "bg-warning text-dark"
	case ApptCancel:
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
