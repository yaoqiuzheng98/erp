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
	OpenID       string `bson:"open_id,omitempty"` // 微信小程序 openId（小程序建档/登录时绑定，免密登录用）
	Gender       string `bson:"gender"`
	Birth        string `bson:"birth"`
	Allergy      string `bson:"allergy"`
	History      string `bson:"history"`
	Note         string `bson:"note"`
}
