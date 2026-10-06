package patient

import (
	"context"
	"errors"
	"regexp"
	"time"

	"erp/internal/platform/auth"
	"erp/internal/platform/repo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Service struct {
	pats *repo.TenantRepo[Patient]
}

func New(db *mongo.Database) *Service {
	return &Service{pats: repo.NewTenantRepo[Patient](db, "patients")}
}

// List 姓名/电话模糊搜（输入按字面匹配，正则元字符已转义）。
func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, q string, skip, limit int64) ([]Patient, int64, error) {
	f := bson.M{}
	if q != "" {
		q = regexp.QuoteMeta(q)
		f["$or"] = []bson.M{
			{"name": bson.M{"$regex": q}},
			{"phone": bson.M{"$regex": q}},
		}
	}
	total, err := s.pats.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.pats.FindMany(ctx, tenantID, f, options.Find().SetSkip(skip).SetLimit(limit))
	return list, total, err
}

func (s *Service) ByID(ctx context.Context, tenantID, id bson.ObjectID) (*Patient, error) {
	return s.pats.FindByID(ctx, tenantID, id)
}

// SetPassword 门诊后台配置患者端登录密码。
func (s *Service) SetPassword(ctx context.Context, tenantID, id bson.ObjectID, plain string) error {
	if len(plain) < 6 {
		return errors.New("密码至少6位")
	}
	hash, err := auth.HashPassword(plain)
	if err != nil {
		return err
	}
	return s.pats.Update(ctx, tenantID, id, bson.M{"password_hash": hash})
}

// Login 患者端登录：电话定位 + 密码校验。未配密码拒绝。
func (s *Service) Login(ctx context.Context, tenantID bson.ObjectID, phone, password string) (*Patient, error) {
	p, err := s.pats.FindOne(ctx, tenantID, bson.M{"phone": phone})
	if err != nil || p.PasswordHash == "" || !auth.CheckPassword(p.PasswordHash, password) {
		return nil, errors.New("手机号或密码错误")
	}
	return p, nil
}

// Create 建档：按电话自动认领已有患者（有则报错防重），认领不到才新建。
func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, p *Patient) error {
	if p.Name == "" {
		return errors.New("姓名必填")
	}
	// 电话可不填（儿童/老人常见），填了必须合法，否则患者端登录成死数据
	if p.Phone != "" {
		if !auth.ValidPhone(p.Phone) {
			return auth.ErrPhoneInvalid
		}
		if n, _ := s.pats.Count(ctx, tenantID, bson.M{"phone": p.Phone}); n > 0 {
			return errors.New("该电话已建过患者档案")
		}
	}
	p.TenantID, p.CreatedAt = tenantID, time.Now()
	id, err := s.pats.Insert(ctx, tenantID, p)
	if err != nil {
		return err
	}
	p.ID = id
	return nil
}
