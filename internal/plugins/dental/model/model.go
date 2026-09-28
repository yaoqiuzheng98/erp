package model

import (
	"strconv"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	ApptBooked  = "booked"  // 已预约
	ApptArrived = "arrived" // 已到诊
	ApptDone    = "done"    // 已完成
	ApptNoShow  = "noshow"  // 爽约
	ApptCancel  = "cancel"  // 取消
)

// 牙位状态（FDI 编号）；缺省视为健康。
const (
	ToothCaries  = "caries"  // 龋坏
	ToothMissing = "missing" // 缺失
	ToothTreated = "treated" // 已治疗
	ToothImplant = "implant" // 种植
	ToothCrown   = "crown"   // 修复/冠
)

// ToothStatuses 状态 → 显示名（牙位图图例）。
var ToothStatuses = map[string]string{
	ToothCaries:  "龋坏",
	ToothMissing: "缺失",
	ToothTreated: "已治",
	ToothImplant: "种植",
	ToothCrown:   "修复",
}

// Patient 患者医疗扩展档案：身份主体是 basedata 客户（CustomerID），
// 本表只存医疗字段。姓名/电话经 CustomerID join 客户表取。
type Patient struct {
	model.Doc  `bson:",inline"`
	CustomerID bson.ObjectID     `bson:"customer_id"` // → basedata 客户
	Code       string            `bson:"code"`        // 病历号 PT-xxxxxx（兼作客户编码）
	Gender     string            `bson:"gender"`
	Birth      string            `bson:"birth"`
	Allergy    string            `bson:"allergy"` // 过敏史
	History    string            `bson:"history"` // 既往史
	Note       string            `bson:"note"`
	Teeth      map[string]string `bson:"teeth"`
}

// View 患者 + 客户资料 join 后的展示模型。
type View struct {
	Patient
	Name  string `bson:"-"`
	Phone string `bson:"-"`
}

// ServiceItem 诊疗价目表：服务型门诊的核心主数据（替代 basedata 商品）。
// Category 约定：洁治/充填/根管/拔牙/正畸/种植/修复/检查/药品，其他 free text。
// 药品少量直接建 Category=药品 的条目，按次收费不走库存。
type ServiceItem struct {
	model.Doc `bson:",inline"`
	Code      string  `bson:"code"`     // 如 SV-0001，租户内唯一
	Name      string  `bson:"name"`     // 如 洗牙（超声洁治）
	Category  string  `bson:"category"` // 分类
	Price     float64 `bson:"price"`    // 单价
	Unit      string  `bson:"unit"`     // 次/颗/小时…
	Status    string  `bson:"status"`   // active / disabled
}

// ApptItem 预约/结算明细行：下单时快照 Code/Name/Price，防价目表改价影响历史单。
type ApptItem struct {
	ServiceID bson.ObjectID `bson:"service_id"`
	Code      string        `bson:"code"`
	Name      string        `bson:"name"`
	Qty       float64       `bson:"qty"`
	Price     float64       `bson:"price"`
	Amount    float64       `bson:"amount"` // Qty*Price
}

// Appointment 预约（按椅位/医生/时段排）。
type Appointment struct {
	model.Doc   `bson:",inline"`
	PatientID   bson.ObjectID `bson:"patient_id"`
	PatientName string        `bson:"patient_name"`
	Doctor      string        `bson:"doctor"`
	Chair       string        `bson:"chair"` // 椅位号
	Date        string        `bson:"date"`  // YYYY-MM-DD
	Slot        string        `bson:"slot"`  // HH:MM
	Item        string        `bson:"item"`  // 存量自由文本（兼容老单）；新单由 Items 汇总生成
	Items       []ApptItem    `bson:"items,omitempty"`
	Status      string        `bson:"status"`
	Charge      float64       `bson:"charge"` // 完成时收费总额（明细合计或手工额）
	ChargeNo    string        `bson:"charge_no"`
}

// DisplayItem 列表展示用：有明细显示明细名，否则回落老 Item。
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

// Total 明细合计（无明细回落 Charge，保持老模板可用）。
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

func trimNum(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
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
