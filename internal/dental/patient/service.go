package patient

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
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

// EnsureIndexes 非空电话租户内唯一（防并发建档重号；空电话不限）；
// 非空 open_id 租户内唯一（一个微信只能绑一个档，防重复绑定串号）。
// 登录按电话/微信定位，重号会随机登错人。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	if _, err := s.pats.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "phone", Value: 1}},
		Options: options.Index().SetUnique(true).
			SetPartialFilterExpression(bson.M{"phone": bson.M{"$gt": ""}}),
	}); err != nil {
		return err
	}
	_, err := s.pats.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "open_id", Value: 1}},
		Options: options.Index().SetUnique(true).
			SetPartialFilterExpression(bson.M{"open_id": bson.M{"$gt": ""}}),
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

// BindOpenID 绑定微信 openId（建档时/登录带 code 顺手绑定）：换绑直接覆盖。
// 一个 openId 只能绑一个档（唯一索引兜底）：撞索引说明已被别的档绑走。
func (s *Service) BindOpenID(ctx context.Context, tenantID, id bson.ObjectID, openid string) error {
	if openid == "" {
		return errors.New("openId 为空")
	}
	return s.pats.Update(ctx, tenantID, id, bson.M{"open_id": openid})
}

// ClearOpenID 解绑微信（门诊后台操作）：解绑后该微信可重新建档。
func (s *Service) ClearOpenID(ctx context.Context, tenantID, id bson.ObjectID) error {
	return s.pats.Update(ctx, tenantID, id, bson.M{"open_id": ""})
}

// NormalizeGender 性别归一：空=未填写，只收 男/女。
func NormalizeGender(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "男" || v == "女" {
		return v, nil
	}
	return "", errors.New("性别不正确")
}

// ParseAge 表单年龄 → *int（空=nil）；范围 0-150。
func ParseAge(v string) (*int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 150 {
		return nil, errors.New("年龄需在 0-150 之间")
	}
	return &n, nil
}

func validateAge(age *int) error {
	if age != nil && (*age < 0 || *age > 150) {
		return errors.New("年龄需在 0-150 之间")
	}
	return nil
}

// Create 建档：姓名+电话必填（电话是患者登录/联系主键，空值成死数据）；
// 电话租户内已建过则拒绝（提示去找列表已有档案）。
// 性别/年龄允许留空：小程序自助建档只有姓名+手机号；门诊后台表单由 handler 强制必填。
func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, p *Patient) error {
	if p.Name == "" {
		return errors.New("姓名必填")
	}
	if p.Phone == "" {
		return errors.New("电话必填")
	}
	if !auth.ValidPhone(p.Phone) {
		return auth.ErrPhoneInvalid
	}
	gender, err := NormalizeGender(p.Gender)
	if err != nil {
		return err
	}
	p.Gender = gender
	if err := validateAge(p.Age); err != nil {
		return err
	}
	if n, _ := s.pats.Count(ctx, tenantID, bson.M{"phone": p.Phone}); n > 0 {
		return errors.New("该电话已建过患者档案")
	}
	p.TenantID, p.CreatedAt = tenantID, time.Now()
	id, err := s.pats.Insert(ctx, tenantID, p)
	if err != nil {
		return err
	}
	p.ID = id
	return nil
}

// Update 修改患者资料：姓名/性别/年龄；电话不可改（登录主键，故不在此入口）。
func (s *Service) Update(ctx context.Context, tenantID, id bson.ObjectID, name, gender string, age *int) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("姓名必填")
	}
	gender, err := NormalizeGender(gender)
	if err != nil {
		return err
	}
	if gender == "" {
		return errors.New("性别必填")
	}
	if age == nil {
		return errors.New("年龄必填")
	}
	if err := validateAge(age); err != nil {
		return err
	}
	matched, err := s.pats.UpdateWhere(ctx, tenantID, bson.M{"_id": id}, bson.M{
		"name": name, "gender": gender, "age": age,
	})
	if err != nil {
		return err
	}
	if matched == 0 {
		return errors.New("患者不存在")
	}
	return nil
}
