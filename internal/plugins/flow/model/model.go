package model

import (
	"time"

	"erp/internal/platform/model"
)

const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)

// Approval 审批实例：其他插件经 doc.submit 事件产生，单步审批。
type Approval struct {
	model.Doc  `bson:",inline"`
	TargetType string    `bson:"target_type"` // sales_order / purchase_order / hr_leave
	TargetID   string    `bson:"target_id"`
	Title      string    `bson:"title"`
	Status     string    `bson:"status"`
	By         string    `bson:"by"` // 提交人
	Approver   string    `bson:"approver"`
	Comment    string    `bson:"comment"`
	DecidedAt  time.Time `bson:"decided_at,omitempty"`
}
