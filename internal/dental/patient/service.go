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

// EnsureIndexes 非空电话租户内唯一（防并发建档重号；空电话不限）。
// 登录按电话定位，重号会随机登错人。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	_, err := s.pats.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "phone", Value: 1}},
		Options: options.Index().SetUnique(true).
			SetPartialFilterExpression(bson.M{"phone": bson.M{"$gt": ""}}),
	})
	return err
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
	list, err := s.pats.FindMany(ctx, tenantID, f, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetSkip(skip).SetLimit(limit))
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

// ByOpenID 按微信 openId 定位患者（小程序免密登录用）：空串直接 miss。
func (s *Service) ByOpenID(ctx context.Context, tenantID bson.ObjectID, openid string) (*Patient, error) {
	if openid == "" {
		return nil, errors.New("未绑定微信")
	}
	return s.pats.FindOne(ctx, tenantID, bson.M{"open_id": openid})
}

// BindOpenID 绑定微信 openId（建档时/密码登录成功后调，换绑直接覆盖）。
func (s *Service) BindOpenID(ctx context.Context, tenantID, id bson.ObjectID, openid string) error {
	if openid == "" {
		return errors.New("openId 为空")
	}
	return s.pats.Update(ctx, tenantID, id, bson.M{"open_id": openid})
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
