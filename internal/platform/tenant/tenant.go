package tenant

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Tenant 门诊租户（一家门诊）。RegFee 挂号费（患者自助预约时收），0=不收。
// Intro/Address/Phone/Hours/Notice/CoverID 为患者端首页展示的诊所信息，
// CoverID 为 GridFS 图片 ID（封面图，门诊后台上传）。
type Tenant struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	Name      string        `bson:"name"`
	RegFee    float64       `bson:"reg_fee"`
	Status    string        `bson:"status"` // active / suspended
	Intro     string        `bson:"intro,omitempty"`
	Address   string        `bson:"address,omitempty"`
	Phone     string        `bson:"phone,omitempty"`
	Hours     string        `bson:"hours,omitempty"`
	Notice    string        `bson:"notice,omitempty"`
	CoverID   string        `bson:"cover_id,omitempty"`
	CreatedAt time.Time     `bson:"created_at"`
}

var ErrSuspended = errors.New("租户已停用")

// tenantCollections 带 tenant_id 的租户级集合；新增集合时同步维护。
var tenantCollections = []string{
	"users", "roles", "patients", "appointments", "service_items",
	"bills", "payments", "expenses", "sessions", "notifications", "audit_logs",
	"staff", "staff_roles", // 历史遗留集合（员工模块已并入用户）
}

type Service struct {
	col *mongo.Collection
	db  *mongo.Database
}

func NewService(db *mongo.Database) *Service {
	return &Service{col: db.Collection("tenants"), db: db}
}

func (s *Service) ByID(ctx context.Context, id bson.ObjectID) (*Tenant, error) {
	var t Tenant
	if err := s.col.FindOne(ctx, bson.M{"_id": id}).Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Service) List(ctx context.Context) ([]Tenant, error) {
	cur, err := s.col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	var out []Tenant
	return out, cur.All(ctx, &out)
}

func (s *Service) Create(ctx context.Context, name string) (*Tenant, error) {
	t := &Tenant{Name: name, Status: "active", CreatedAt: time.Now()}
	res, err := s.col.InsertOne(ctx, t)
	if err != nil {
		return nil, err
	}
	t.ID = res.InsertedID.(bson.ObjectID)
	return t, nil
}

func (s *Service) SetFee(ctx context.Context, id bson.ObjectID, fee float64) error {
	if fee < 0 {
		fee = 0
	}
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"reg_fee": fee}})
	return err
}

// SetProfile 更新诊所介绍信息（门诊后台门诊设置维护，患者端首页展示）。
func (s *Service) SetProfile(ctx context.Context, id bson.ObjectID, intro, address, phone, hours, notice string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"intro": intro, "address": address, "phone": phone, "hours": hours, "notice": notice,
	}})
	return err
}

// SetCover 更新诊所封面图（GridFS 文件 ID hex，空串=清除）。
func (s *Service) SetCover(ctx context.Context, id bson.ObjectID, coverID string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"cover_id": coverID}})
	return err
}

func (s *Service) SetStatus(ctx context.Context, id bson.ObjectID, status string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"status": status}})
	return err
}

// Purge 删除租户及其全部业务数据，不可恢复。
// 附件（含磁盘文件）由 attach.PurgeTenant 先行清理；调用方负责记审计。
func (s *Service) Purge(ctx context.Context, id bson.ObjectID) error {
	for _, c := range tenantCollections {
		if _, err := s.db.Collection(c).DeleteMany(ctx, bson.M{"tenant_id": id}); err != nil {
			return fmt.Errorf("清理 %s: %w", c, err)
		}
	}
	// 流水号 _id 形如 "<租户ID>:<规则>:<年月>"
	if _, err := s.db.Collection("sequences").DeleteMany(ctx,
		bson.M{"_id": bson.M{"$regex": "^" + id.Hex() + ":"}}); err != nil {
		return fmt.Errorf("清理 sequences: %w", err)
	}
	_, err := s.col.DeleteOne(ctx, bson.M{"_id": id})
	return err
}
