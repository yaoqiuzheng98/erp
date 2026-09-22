// Package contract 跨插件事件契约：topic 常量与 payload 结构。
// 插件间禁止直接 import，事件协作统一依赖本包。
package contract

import "go.mongodb.org/mongo-driver/v2/bson"

// Topic 约定 {domain}.{action}。
const (
	// TopicDocSubmit 单据提交审批；payload DocSubmit。
	TopicDocSubmit = "doc.submit"
	// TopicFlowApproved 审批通过；payload FlowResult。
	TopicFlowApproved = "flow.approved"
	// TopicFlowRejected 审批驳回；payload FlowResult。
	TopicFlowRejected = "flow.rejected"
	// TopicSalesApproved 销售订单审核通过；payload OrderApproved。
	TopicSalesApproved = "sales.order.approved"
	// TopicPurchaseApproved 采购订单审核通过；payload OrderApproved。
	TopicPurchaseApproved = "purchase.order.approved"
	// TopicStockChanged 库存变动；payload StockChanged。
	TopicStockChanged = "inventory.stock.changed"
	// TopicCharge 诊疗/业务收费（非订单渠道）；payload Charge。
	TopicCharge = "billing.charge"
)

// DocSubmit 提交审批的单据信息。
type DocSubmit struct {
	TargetType string `json:"target_type"` // sales_order / purchase_order / hr_leave ...
	TargetID   string `json:"target_id"`   // 单据 ObjectID hex
	Title      string `json:"title"`       // 展示用，如 "销售订单 SO-202609-0001"
	By         string `json:"by"`
}

// FlowResult 审批结论。
type FlowResult struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Comment    string `json:"comment"`
	By         string `json:"by"`
}

// OrderLine 订单行（也是库存单行）。
type OrderLine struct {
	ProductID   bson.ObjectID `json:"product_id"`
	ProductCode string        `json:"product_code"`
	ProductName string        `json:"product_name"`
	WarehouseID bson.ObjectID `json:"warehouse_id"`
	Qty         float64       `json:"qty"`
	Price       float64       `json:"price"`
}

// OrderApproved 订单审核通过。
type OrderApproved struct {
	OrderID   string        `json:"order_id"`
	DocNo     string        `json:"doc_no"`
	PartnerID bson.ObjectID `json:"partner_id"` // customer 或 supplier
	Total     float64       `json:"total"`
	Lines     []OrderLine   `json:"lines"`
	By        string        `json:"by"`
}

// StockChanged 库存余额变动。
type StockChanged struct {
	ProductID   bson.ObjectID `json:"product_id"`
	WarehouseID bson.ObjectID `json:"warehouse_id"`
	Delta       float64       `json:"delta"`
	DocNo       string        `json:"doc_no"`
}

// Charge 业务收费（门诊/服务类，非 partner 订单渠道）：财务生成应收。
type Charge struct {
	DocNo     string  `json:"doc_no"`     // 收费流水号
	PartyName string  `json:"party_name"` // 付款方名称（患者等）
	Total     float64 `json:"total"`
	By        string  `json:"by"`
	RefType   string  `json:"ref_type"` // 来源单据类型，如 dental_appt
	RefID     string  `json:"ref_id"`   // 来源单据 hex
}
