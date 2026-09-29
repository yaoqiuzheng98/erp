package patient

import (
	"erp/internal/platform/model"
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
