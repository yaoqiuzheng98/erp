package staff

import (
	"erp/internal/platform/model"
)

// RoleDef 角色定义：名称即标识（租户内唯一），可接诊的角色才能排诊。
// 不提供改名（删了重建即可）；停用后新增员工不可选，存量不受影响。
type RoleDef struct {
	model.Doc   `bson:",inline"`
	Name        string `bson:"name"`
	CanPractice bool   `bson:"can_practice"`
	Status      string `bson:"status"` // active / disabled
}

// Staff 医护花名册。Role 存角色名；预约只列可接诊角色的在职员工。
type Staff struct {
	model.Doc `bson:",inline"`
	Name      string `bson:"name"` // 租户内唯一
	Role      string `bson:"role"` // 角色名，如 医生
	Phone     string `bson:"phone"`
	Status    string `bson:"status"` // active / disabled
}
