package model

import (
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

// Appointment 预约（按椅位/医生/时段排）。
type Appointment struct {
	model.Doc   `bson:",inline"`
	PatientID   bson.ObjectID `bson:"patient_id"`
	PatientName string        `bson:"patient_name"`
	Doctor      string        `bson:"doctor"`
	Chair       string        `bson:"chair"` // 椅位号
	Date        string        `bson:"date"`  // YYYY-MM-DD
	Slot        string        `bson:"slot"`  // HH:MM
	Item        string        `bson:"item"`  // 项目：洗牙/补牙/根管/正畸复诊…
	Status      string        `bson:"status"`
	Charge      float64       `bson:"charge"` // 完成时收费额
	ChargeNo    string        `bson:"charge_no"`
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
