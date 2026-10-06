package audit

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Action 审计动作常量：字符串即入库值，handler 一律用常量，防手写拼错导致查不到。
// 注意改名等于改历史数据语义，新增只增不改。
const (
	ActLogin          = "login"
	ActSysLogin       = "sys.login"
	ActChangePassword = "user.change_password"
	ActUserCreate     = "user.create"
	ActUserUpdate     = "user.update"
	ActUserResetPwd   = "user.reset_password"
	ActRoleCreate     = "role.create"
	ActTenantCreate   = "tenant.create"
	ActTenantToggle   = "tenant.toggle"
	ActTenantDelete   = "tenant.delete"
	ActTenantSettings = "tenant.settings"
	ActTenantHome     = "tenant.home"
	ActStaffTop       = "staff.top"
	ActPatientCreate  = "patient.create"
	ActApptCreate     = "appt.create"
	ActApptCheckin    = "appt.checkin"
	ActApptServe      = "appt.serve"
	ActApptComplete   = "appt.complete"
	ActApptNoshow     = "appt.noshow"
	ActApptCancel     = "appt.cancel"
	ActCatalogCreate  = "catalog.create"
	ActCatalogUpdate  = "catalog.update"
	ActCatalogDelete  = "catalog.delete"
	ActBillingPay     = "billing.pay"
	ActBillingVoid    = "billing.void"
)

// Entry 审计日志。
type Entry struct {
	ID       bson.ObjectID `bson:"_id,omitempty"`
	TenantID bson.ObjectID `bson:"tenant_id,omitempty"`
	UserID   bson.ObjectID `bson:"user_id,omitempty"`
	Username string        `bson:"username"`
	Action   string        `bson:"action"` // login / user.create / tenant.create ...
	Target   string        `bson:"target"`
	Detail   string        `bson:"detail"`
	IP       string        `bson:"ip"`
	At       time.Time     `bson:"at"`
}

type Service struct {
	col *mongo.Collection
}

func New(db *mongo.Database) *Service {
	return &Service{col: db.Collection("audit_logs")}
}

func (s *Service) Log(ctx context.Context, e Entry) {
	e.At = time.Now()
	_, _ = s.col.InsertOne(ctx, e)
}

func (s *Service) List(ctx context.Context, filter bson.M, skip, limit int64) ([]Entry, error) {
	opts := options.Find().SetSort(bson.D{{Key: "at", Value: -1}}).SetSkip(skip).SetLimit(limit)
	cur, err := s.col.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var out []Entry
	return out, cur.All(ctx, &out)
}

// Count 条件总数（分页用）。
func (s *Service) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.col.CountDocuments(ctx, filter)
}
