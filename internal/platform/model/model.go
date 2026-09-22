package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Doc 是租户文档的公共基座，插件 model 通过内嵌获得通用字段。
type Doc struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	TenantID  bson.ObjectID `bson:"tenant_id"`
	CreatedAt time.Time     `bson:"created_at"`
	CreatedBy string        `bson:"created_by,omitempty"`
	UpdatedAt time.Time     `bson:"updated_at,omitempty"`
	UpdatedBy string        `bson:"updated_by,omitempty"`
}
