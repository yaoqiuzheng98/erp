package patient

import (
	"strconv"

	"erp/internal/platform/model"
)

// Patient 患者档案：姓名+电话直接标识（电话可不填），无客户中间层。
// PasswordHash 患者端登录密码（门诊后台配置，空=未开通患者端）。
type Patient struct {
	model.Doc    `bson:",inline"`
	Name         string `bson:"name"`
	Phone        string `bson:"phone"`
	Gender       string `bson:"gender,omitempty"` // 空=未填写，取值：男/女
	Age          *int   `bson:"age,omitempty"`    // nil=未填写；指针保留 0 岁（婴儿）
	PasswordHash string `bson:"password_hash,omitempty"`
	OpenID       string `bson:"open_id,omitempty"` // 微信小程序 openId（小程序建档/登录时绑定，免密登录用）
}

// AgeText 年龄展示/编辑框回填用：未填写返回空串。
func (p Patient) AgeText() string {
	if p.Age == nil {
		return ""
	}
	return strconv.Itoa(*p.Age)
}
