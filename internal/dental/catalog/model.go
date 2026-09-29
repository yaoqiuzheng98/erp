package catalog

import (
	"erp/internal/platform/model"
)

// ServiceItem 诊疗价目：门诊主数据。药品少量直接建 Category=药品 的条目，
// 按次收费不走库存。以名称标识，租户内唯一。
type ServiceItem struct {
	model.Doc `bson:",inline"`
	Name      string  `bson:"name"`
	Category  string  `bson:"category"`
	Price     float64 `bson:"price"`
	Unit      string  `bson:"unit"`
	Status    string  `bson:"status"` // active / disabled
}
