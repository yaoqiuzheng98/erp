package tenant

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Tenant 门诊租户（一家门诊）。RegFee 挂号费（患者自助预约时收），0=不收。
// Intro/Address/Phone/Hours/Notice/Gallery 为患者端首页展示的诊所信息，
// Gallery 为 GridFS 图片 ID（hex）列表，多图画廊，门诊后台上传维护。
type Tenant struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	Name      string        `bson:"name"`
	RegFee    float64       `bson:"reg_fee"`
	RegDeduct bool          `bson:"reg_deduct,omitempty"` // 挂号费抵扣最终诊疗费（开时已缴挂号费当定金抵）
	// 放号配置：SlotMinutes 每 N 分钟一档（15/30/60，0=默认30），
	// SlotCapacity 每档可约人数（0=默认1）。改配置只影响之后的新预约。
	SlotMinutes  int `bson:"slot_minutes,omitempty"`
	SlotCapacity int `bson:"slot_capacity,omitempty"`
	Status       string        `bson:"status"`               // active / suspended
	Intro     string        `bson:"intro,omitempty"`
	Address   string        `bson:"address,omitempty"`
	Phone     string        `bson:"phone,omitempty"`
	Hours     string        `bson:"hours,omitempty"`
	Notice    string        `bson:"notice,omitempty"`
	Gallery   []string      `bson:"gallery,omitempty"`
	CreatedAt time.Time     `bson:"created_at"`
}

var ErrSuspended = errors.New("租户已停用")

// 放号默认值：半小时一档，每档 1 人。
const (
	DefaultSlotMinutes  = 30
	DefaultSlotCapacity = 1
)

// SlotMinutesAllowed 放号粒度档位（改档加这里，下拉与校验共用一处）。
var SlotMinutesAllowed = []int{15, 30, 60}

// SlotConfig 生效的放号配置（未配回落默认；非法值也回落，不炸老数据）。
func (t *Tenant) SlotConfig() (minutes, capacity int) {
	minutes, capacity = t.SlotMinutes, t.SlotCapacity
	allowed := false
	for _, m := range SlotMinutesAllowed {
		if minutes == m {
			allowed = true
		}
	}
	if !allowed {
		minutes = DefaultSlotMinutes
	}
	if capacity < 1 || capacity > 10 {
		capacity = DefaultSlotCapacity
	}
	return minutes, capacity
}

// tenantCollections 带 tenant_id 的租户级集合；新增集合时同步维护。
var tenantCollections = []string{
	"users", "roles", "patients", "appointments", "service_items",
	"bills", "payments", "sessions", "notifications", "audit_logs",
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

func (s *Service) List(ctx context.Context, skip, limit int64) ([]Tenant, error) {
	cur, err := s.col.Find(ctx, bson.M{},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetSkip(skip).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	var out []Tenant
	return out, cur.All(ctx, &out)
}

// Count 门诊总数（分页/首页统计用）。
func (s *Service) Count(ctx context.Context) (int64, error) {
	return s.col.CountDocuments(ctx, bson.M{})
}

func (s *Service) Create(ctx context.Context, name string) (*Tenant, error) {
	t := &Tenant{Name: name, Status: "active", CreatedAt: time.Now()}
	res, err := s.col.InsertOne(ctx, t)
	if err != nil {
		return nil, err
	}
	oid, ok := res.InsertedID.(bson.ObjectID)
	if !ok {
		return nil, errors.New("创建失败: 非法 ID")
	}
	t.ID = oid
	return t, nil
}

// SetBilling 挂号费与抵扣开关一起存（门诊设置）。
func (s *Service) SetBilling(ctx context.Context, id bson.ObjectID, fee float64, deduct bool) error {
	if fee < 0 {
		fee = 0
	}
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$set": bson.M{"reg_fee": fee, "reg_deduct": deduct}})
	return err
}

// SetSchedule 放号粒度与每档人数一起存（门诊设置）：非法值直接报错，不落库。
func (s *Service) SetSchedule(ctx context.Context, id bson.ObjectID, minutes, capacity int) error {
	allowed := false
	for _, m := range SlotMinutesAllowed {
		if minutes == m {
			allowed = true
		}
	}
	if !allowed {
		return errors.New("放号粒度只能选 15/30/60 分钟")
	}
	if capacity < 1 || capacity > 10 {
		return errors.New("每档人数只能填 1~10")
	}
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$set": bson.M{"slot_minutes": minutes, "slot_capacity": capacity}})
	return err
}

// SetProfile 更新诊所介绍信息（门诊后台门诊设置维护，患者端首页展示）。
func (s *Service) SetProfile(ctx context.Context, id bson.ObjectID, intro, address, phone, hours, notice string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"intro": intro, "address": address, "phone": phone, "hours": hours, "notice": notice,
	}})
	return err
}

// AddGalleryPhoto 门诊图库追加一张（GridFS 文件 ID hex）。
func (s *Service) AddGalleryPhoto(ctx context.Context, id bson.ObjectID, fileID string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$push": bson.M{"gallery": fileID}})
	return err
}

// RemoveGalleryPhoto 门诊图库删除一张。
func (s *Service) RemoveGalleryPhoto(ctx context.Context, id bson.ObjectID, fileID string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$pull": bson.M{"gallery": fileID}})
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
