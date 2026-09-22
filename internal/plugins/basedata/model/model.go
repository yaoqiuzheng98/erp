package model

import "erp/internal/platform/model"

// Product 商品/物料。
type Product struct {
	model.Doc `bson:",inline"`
	Code      string  `bson:"code"`
	Name      string  `bson:"name"`
	Unit      string  `bson:"unit"`
	Category  string  `bson:"category"`
	Spec      string  `bson:"spec"`
	Price     float64 `bson:"price"`
	MinStock  float64 `bson:"min_stock"` // 库存下限，inventory 预警用
	Status    string  `bson:"status"`    // active / disabled
}

// Warehouse 仓库。
type Warehouse struct {
	model.Doc `bson:",inline"`
	Code      string `bson:"code"`
	Name      string `bson:"name"`
	Address   string `bson:"address"`
}

// Customer 客户。
type Customer struct {
	model.Doc `bson:",inline"`
	Code      string `bson:"code"`
	Name      string `bson:"name"`
	Contact   string `bson:"contact"`
	Phone     string `bson:"phone"`
}

// Supplier 供应商。
type Supplier struct {
	model.Doc `bson:",inline"`
	Code      string `bson:"code"`
	Name      string `bson:"name"`
	Contact   string `bson:"contact"`
	Phone     string `bson:"phone"`
}
