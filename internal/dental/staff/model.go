package staff

import (
	"erp/internal/platform/model"
)

// 员工角色。
const (
	Doctor    = "doctor"
	Nurse     = "nurse"
	Front     = "front"
	Assistant = "assistant"
)

// Roles 角色 → 显示名。
var Roles = map[string]string{
	Doctor:    "医生",
	Nurse:     "护士",
	Front:     "前台",
	Assistant: "助理",
}

// Staff 医护花名册。预约选医生时只列在职医生。
type Staff struct {
	model.Doc `bson:",inline"`
	Name      string `bson:"name"`   // 租户内唯一
	Role      string `bson:"role"`   // doctor / nurse / front / assistant
	Phone     string `bson:"phone"`
	Status    string `bson:"status"` // active / disabled
}
