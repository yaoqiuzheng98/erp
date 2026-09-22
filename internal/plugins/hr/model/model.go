package model

import (
	"time"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	LeaveDraft     = "draft"
	LeaveSubmitted = "submitted"
	LeaveApproved  = "approved"
	LeaveRejected  = "rejected"
)

// Employee 员工档案。
type Employee struct {
	model.Doc `bson:",inline"`
	Code      string    `bson:"code"`
	Name      string    `bson:"name"`
	Dept      string    `bson:"dept"`
	Position  string    `bson:"position"`
	Phone     string    `bson:"phone"`
	HireDate  time.Time `bson:"hire_date"`
	Status    string    `bson:"status"` // active / left
}

// Attend 考勤记录。
type Attend struct {
	model.Doc  `bson:",inline"`
	EmployeeID bson.ObjectID `bson:"employee_id"`
	EmpName    string        `bson:"emp_name"`
	Date       string        `bson:"date"` // YYYY-MM-DD
	Type       string        `bson:"type"` // normal / late / absent / leave
}

// Leave 请假单。
type Leave struct {
	model.Doc  `bson:",inline"`
	DocNo      string        `bson:"doc_no"`
	EmployeeID bson.ObjectID `bson:"employee_id"`
	EmpName    string        `bson:"emp_name"`
	Type       string        `bson:"type"` // annual / sick / personal
	From       string        `bson:"from"`
	To         string        `bson:"to"`
	Reason     string        `bson:"reason"`
	Status     string        `bson:"status"`
}
