package tenant

import (
	"context"
	"errors"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var codeRe = regexp.MustCompile(`^[a-z0-9-]{2,32}$`)

// ValidCode 患者端短码规则：小写字母数字横线，2~32位。
func ValidCode(code string) bool { return codeRe.MatchString(code) }

// Tenant 门诊租户（一家门诊）。Code 是患者端短码（/p/{code}），全局唯一。
type Tenant struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	Name      string        `bson:"name"`
	Code      string        `bson:"code"`   // 患者端短码，小写字母数字横线
	Status    string        `bson:"status"` // active / suspended
	CreatedAt time.Time     `bson:"created_at"`
}

var ErrSuspended = errors.New("租户已停用")

type Service struct {
	col *mongo.Collection
}

func NewService(db *mongo.Database) *Service {
	return &Service{col: db.Collection("tenants")}
}

func (s *Service) ByID(ctx context.Context, id bson.ObjectID) (*Tenant, error) {
	var t Tenant
	if err := s.col.FindOne(ctx, bson.M{"_id": id}).Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ByCode 按患者端短码查租户（公开入口用，仅 active 可用）。
func (s *Service) ByCode(ctx context.Context, code string) (*Tenant, error) {
	var t Tenant
	if err := s.col.FindOne(ctx, bson.M{"code": code, "status": "active"}).Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Service) SetCode(ctx context.Context, id bson.ObjectID, code string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"code": code}})
	return err
}

func (s *Service) List(ctx context.Context) ([]Tenant, error) {
	cur, err := s.col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	var out []Tenant
	return out, cur.All(ctx, &out)
}

func (s *Service) Create(ctx context.Context, name, code string) (*Tenant, error) {
	t := &Tenant{Name: name, Code: code, Status: "active", CreatedAt: time.Now()}
	res, err := s.col.InsertOne(ctx, t)
	if err != nil {
		return nil, err
	}
	t.ID = res.InsertedID.(bson.ObjectID)
	return t, nil
}

func (s *Service) SetStatus(ctx context.Context, id bson.ObjectID, status string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"status": status}})
	return err
}
