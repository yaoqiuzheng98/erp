package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// HourSeg 营业时段（HH:MM，如 09:00-12:00）：中午休息即两段。
// 门诊设置里维护，tenant 存、appointment 按段放号，两边共用一处定义。
type HourSeg struct {
	Open  string `bson:"open" json:"open"`
	Close string `bson:"close" json:"close"`
}

// Doc 是租户文档的公共基座，插件 model 通过内嵌获得通用字段。
type Doc struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	TenantID  bson.ObjectID `bson:"tenant_id"`
	CreatedAt time.Time     `bson:"created_at"`
	CreatedBy string        `bson:"created_by,omitempty"`
	UpdatedAt time.Time     `bson:"updated_at,omitempty"`
	UpdatedBy string        `bson:"updated_by,omitempty"`
}
