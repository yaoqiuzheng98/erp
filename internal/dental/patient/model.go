package patient

import (
	"erp/internal/platform/model"
)

// Patient 患者档案：姓名+电话直接标识，无客户中间层。
// PasswordHash 患者端登录密码（门诊后台配置，空=未开通患者端）。
type Patient struct {
	model.Doc    `bson:",inline"`
	Name         string `bson:"name"`
	Phone        string `bson:"phone"`
	PasswordHash string `bson:"password_hash,omitempty"`
	Gender       string `bson:"gender"`
	Birth        string `bson:"birth"`
	Allergy      string `bson:"allergy"`
	History      string `bson:"history"`
	Note         string `bson:"note"`
}
