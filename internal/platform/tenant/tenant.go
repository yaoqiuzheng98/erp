package tenant

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Tenant 企业租户。
type Tenant struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	Name      string        `bson:"name"`
	Industry  string        `bson:"industry"` // 行业标识；空 = 通用
	Status    string        `bson:"status"`   // active / suspended
	CreatedAt time.Time     `bson:"created_at"`
}

// Industry 行业标识选项。插件经 Plugin.Industries() 声明适用行业。
type Industry struct {
	Code string
	Name string
}

// Industries 内置行业目录；插件声明的新行业码会在系统后台选项中自动并入。
var Industries = []Industry{
	{"retail", "零售/便利店"},
	{"wholesale", "批发/贸易"},
	{"restaurant", "餐饮"},
	{"barber", "理发/美容美发"},
	{"dental", "牙科/医疗门诊"},
	{"manufacture", "生产制造"},
}

// IndustryName 返回行业码显示名；空码 = 通用，未知码原样返回。
func IndustryName(code string) string {
	if code == "" {
		return "通用"
	}
	for _, i := range Industries {
		if i.Code == code {
			return i.Name
		}
	}
	return code
}

// IndustryKnown 校验行业码是否在内置目录中。
func IndustryKnown(code string) bool {
	if code == "" {
		return true
	}
	for _, i := range Industries {
		if i.Code == code {
			return true
		}
	}
	return false
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

func (s *Service) List(ctx context.Context) ([]Tenant, error) {
	cur, err := s.col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	var out []Tenant
	return out, cur.All(ctx, &out)
}

func (s *Service) Create(ctx context.Context, name, industry string) (*Tenant, error) {
	t := &Tenant{Name: name, Industry: industry, Status: "active", CreatedAt: time.Now()}
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

func (s *Service) SetIndustry(ctx context.Context, id bson.ObjectID, industry string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"industry": industry}})
	return err
}
