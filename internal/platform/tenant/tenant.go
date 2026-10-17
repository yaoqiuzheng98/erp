package tenant

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"erp/internal/platform/model"
	"erp/internal/platform/tz"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Tenant 门诊租户（一家门诊）。Address/Hours 与 HeroImage/ClinicImage
// （GridFS 文件 ID hex）为患者端展示的诊所信息，门诊后台上传维护。
type Tenant struct {
	ID   bson.ObjectID `bson:"_id,omitempty"`
	Name string        `bson:"name"`
	// 放号配置：SlotMinutes 每 N 分钟一档（15/30/60，0=默认30），
	// SlotCapacity 每档可约人数（0=默认1）。改配置只影响之后的新预约。
	SlotMinutes  int `bson:"slot_minutes,omitempty"`
	SlotCapacity int `bson:"slot_capacity,omitempty"`
	// SlotFrom/SlotTo 老单段放号范围（已由 SlotSegs 两段取代，仅兼容读取）：
	// 新保存一律走 slot_segs 并清掉这两个字段。
	SlotFrom string `bson:"slot_from,omitempty"`
	SlotTo   string `bson:"slot_to,omitempty"`
	// SlotSegs 放号时间两段（上午+下午，门诊设置维护，独立于营业时间）。
	SlotSegs []model.HourSeg `bson:"slot_segs,omitempty"`
	Status   string          `bson:"status"` // active / suspended
	Address  string          `bson:"address,omitempty"`
	// Contact 联系方式（自由文本，如电话/微信），首页门诊卡展示。
	Contact string `bson:"contact,omitempty"`
	// Hours 展示串（首页门诊卡用）：配了 HoursSeg 则每次保存时按段派生，
	// 未配则沿用老文本（兼容历史数据）。
	Hours string `bson:"hours,omitempty"`
	// HoursSeg 营业时段（门诊设置维护，最多两段，中午休息即两段）：
	// 为空表示未配置，放号回落全天。
	HoursSeg []model.HourSeg `bson:"hours_seg,omitempty"`
	// RestWeekdays 每周固定休息日（0=周日..6=周六，门诊设置维护）：
	// 这些星期几每周都不放号、不能预约。
	RestWeekdays []int `bson:"rest_weekdays,omitempty"`
	// RestDates 指定休息日期（YYYY-MM-DD，如节假日）：当天不能预约。
	RestDates []string `bson:"rest_dates,omitempty"`
	// Notice 首页公告（患者端首页黄色广播条）；NoticeFrom/NoticeTo 展示时间窗
	// （YYYY-MM-DD，含首尾）：留空=一直展示，填了只在窗口内展示。
	Notice     string `bson:"notice,omitempty"`
	NoticeFrom string `bson:"notice_from,omitempty"`
	NoticeTo   string `bson:"notice_to,omitempty"`
	// WechatAppID 绑定的微信小程序 AppID（门诊设置维护）：小程序请求带该
	// appid 头时，后端按它解析租户（URL 里 tid 仅兼容保留）。空=未绑定。
	WechatAppID string    `bson:"wechat_appid,omitempty"`
	HeroImage   string    `bson:"hero_image,omitempty"`   // 首页顶部大图（GridFS 文件 ID hex）
	ClinicImage string    `bson:"clinic_image,omitempty"` // 门诊卡图（医生团队入口那块）
	CreatedAt   time.Time `bson:"created_at"`
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
	"users", "roles", "patients", "appointments",
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
	// 新建门诊默认营业时间（9:00-12:00 14:00-18:00），展示串同步写。
	t := &Tenant{Name: name, Status: "active", CreatedAt: time.Now(),
		HoursSeg: append([]model.HourSeg(nil), DefaultBizSegs...),
		Hours:    BizSegText(DefaultBizSegs)}
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

// hourRe HH:MM 24小时制（营业时段输入校验用）。
var hourRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// MaxBizSegs 营业时段分段（上午+下午，中午休息）：两段必填。
const MaxBizSegs = 2

// DefaultBizSegs 新建门诊默认营业时间（9:00-12:00 14:00-18:00）。
var DefaultBizSegs = []model.HourSeg{{Open: "09:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}}

// hourMin HH:MM → 当天分钟数（已校验合法才调）。
func hourMin(h string) int {
	return int(h[0]-'0')*600 + int(h[1]-'0')*60 + int(h[3]-'0')*10 + int(h[4]-'0')
}

// trimHour 去前导零（09:00→9:00，展示串与用户写法一致）。
func trimHour(h string) string {
	return strings.TrimLeft(h[:2], "0") + h[2:]
}

// NormBusinessHours 营业时段归一：早晚两段必填（中午休息），每段起止成对、
// HH:MM、起早于止，段间不重叠，返回按开始排序的结果。
// normTwoSegs 两段时段归一（营业时间/放号时间共用）：必须正好两段、每段起止成对、
// HH:MM、起早于止，段间不重叠，返回按开始排序的结果。noun 取"营业"/"放号"。
func normTwoSegs(segs []model.HourSeg, noun, example string) ([]model.HourSeg, error) {
	if len(segs) != MaxBizSegs {
		return nil, errors.New(noun + "时间早晚两段都要填")
	}
	out := make([]model.HourSeg, 0, MaxBizSegs)
	for _, s := range segs {
		o, c := strings.TrimSpace(s.Open), strings.TrimSpace(s.Close)
		if o == "" || c == "" {
			return nil, errors.New(noun + "时间早晚两段都要填")
		}
		if !hourRe.MatchString(o) || !hourRe.MatchString(c) {
			return nil, errors.New(noun + "时间格式不对，用 HH:MM（如 " + example + "）")
		}
		if hourMin(o) >= hourMin(c) {
			return nil, errors.New(noun + "时段开始要早于结束")
		}
		out = append(out, model.HourSeg{Open: o, Close: c})
	}
	sort.Slice(out, func(i, j int) bool { return hourMin(out[i].Open) < hourMin(out[j].Open) })
	for i := 1; i < len(out); i++ {
		if hourMin(out[i].Open) < hourMin(out[i-1].Close) {
			return nil, errors.New("两段" + noun + "时间不能重叠")
		}
	}
	return out, nil
}

func NormBusinessHours(segs []model.HourSeg) ([]model.HourSeg, error) {
	return normTwoSegs(segs, "营业", "09:00")
}

// NormSlotSegs 放号时间范围归一：上午下午两段必填（门诊设置要求，不再支持"全空回落营业时间"）。
func NormSlotSegs(segs []model.HourSeg) ([]model.HourSeg, error) {
	return normTwoSegs(segs, "放号", "08:00")
}

// BizSegText 时段展示串（如 9:00-12:00 14:00-18:00，段间空格隔开）。
func BizSegText(segs []model.HourSeg) string {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		parts = append(parts, trimHour(s.Open)+"-"+trimHour(s.Close))
	}
	return strings.Join(parts, " ")
}

// BusinessHours 生效的营业时段（空=未配置，调用方回落全天）。
func (t *Tenant) BusinessHours() []model.HourSeg {
	return t.HoursSeg
}

// weekdayCN 星期中文（0=周日..6=周六）。
var weekdayCN = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// RestWeekText 每周休息日展示串（如"每周二休息"、"每周三、周四休息"），无休返回空。
func (t *Tenant) RestWeekText() string {
	if len(t.RestWeekdays) == 0 {
		return ""
	}
	on := map[int]bool{}
	for _, d := range t.RestWeekdays {
		on[d] = true
	}
	names := make([]string, 0, len(t.RestWeekdays))
	for _, d := range []int{1, 2, 3, 4, 5, 6, 0} {
		if on[d] {
			names = append(names, weekdayCN[d])
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "每" + strings.Join(names, "、") + "休息"
}

// HoursText 首页展示的营业时间：配了时段按段派生（否则沿用老文本），
// 末尾带上每周固定休息日（如"每周二休息"），让患者一眼看到哪天不开门。
func (t *Tenant) HoursText() string {
	hours := t.Hours
	if len(t.HoursSeg) > 0 {
		hours = BizSegText(t.HoursSeg)
	}
	if rest := t.RestWeekText(); rest != "" {
		if hours == "" {
			return rest
		}
		return hours + " " + rest
	}
	return hours
}

// SetBusinessHours 存营业时段（门诊设置）：早晚两段必填，校验通过才落库，
// 展示串同步派生。
func (s *Service) SetBusinessHours(ctx context.Context, id bson.ObjectID, segs []model.HourSeg) error {
	norm, err := NormBusinessHours(segs)
	if err != nil {
		return err
	}
	_, err = s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"hours_seg": norm, "hours": BizSegText(norm),
	}})
	return err
}

// MaxRestDates 指定休息日期条数上限（够用即可，防误贴一坨）。
const MaxRestDates = 366

// NormRest 休息日归一：每周休息日 0(周日)~6(周六) 去重升序；
// 指定日期去空白去重升序、必须是 YYYY-MM-DD。两空=全周无休。
func NormRest(weekdays []int, dates []string) ([]int, []string, error) {
	wdSet := map[int]bool{}
	for _, d := range weekdays {
		if d < 0 || d > 6 {
			return nil, nil, errors.New("每周休息日只能是周日到周六")
		}
		wdSet[d] = true
	}
	wds := make([]int, 0, len(wdSet))
	for d := 0; d <= 6; d++ {
		if wdSet[d] {
			wds = append(wds, d)
		}
	}
	dSet := map[string]bool{}
	for _, s := range dates {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := tz.DayStart(s); !ok {
			return nil, nil, errors.New("休息日期格式不对，用 YYYY-MM-DD")
		}
		dSet[s] = true
	}
	ds := make([]string, 0, len(dSet))
	for k := range dSet {
		ds = append(ds, k)
	}
	sort.Strings(ds)
	if len(ds) > MaxRestDates {
		return nil, nil, errors.New("指定休息日期太多")
	}
	return wds, ds, nil
}

// IsRest 当天是否休息（指定日期命中，或落在每周休息日）。
func (t *Tenant) IsRest(date string) bool {
	for _, d := range t.RestDates {
		if d == date {
			return true
		}
	}
	if len(t.RestWeekdays) > 0 {
		if day, ok := tz.DayStart(date); ok {
			for _, w := range t.RestWeekdays {
				if int(day.Weekday()) == w {
					return true
				}
			}
		}
	}
	return false
}

// SetRest 存休息日配置（门诊设置）：校验通过才落库；两空=清除配置（全周开诊）。
func (s *Service) SetRest(ctx context.Context, id bson.ObjectID, weekdays []int, dates []string) error {
	wds, ds, err := NormRest(weekdays, dates)
	if err != nil {
		return err
	}
	set := bson.M{}
	unset := bson.M{}
	if len(wds) > 0 {
		set["rest_weekdays"] = wds
	} else {
		unset["rest_weekdays"] = ""
	}
	if len(ds) > 0 {
		set["rest_dates"] = ds
	} else {
		unset["rest_dates"] = ""
	}
	update := bson.M{}
	if len(set) > 0 {
		update["$set"] = set
	}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	_, err = s.col.UpdateOne(ctx, bson.M{"_id": id}, update)
	return err
}

// NormNotice 公告归一：内容+展示时间窗都必填（不能留空一直展示），
// 日期须 YYYY-MM-DD 且 起<=止。
func NormNotice(notice, from, to string) (string, string, string, error) {
	notice = strings.TrimSpace(notice)
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if notice == "" {
		return "", "", "", errors.New("首页公告不能留空")
	}
	if from == "" || to == "" {
		return "", "", "", errors.New("公告展示时间必填，起止都要填")
	}
	if _, ok := tz.DayStart(from); !ok {
		return "", "", "", errors.New("公告开始日期格式不对，用 YYYY-MM-DD")
	}
	if _, ok := tz.DayStart(to); !ok {
		return "", "", "", errors.New("公告结束日期格式不对，用 YYYY-MM-DD")
	}
	if from > to {
		return "", "", "", errors.New("公告开始日期不能晚于结束日期")
	}
	return notice, from, to, nil
}

// SetNotice 存首页公告（门诊后台首页配置）：内容+展示时间窗都必填，校验通过才落库。
func (s *Service) SetNotice(ctx context.Context, id bson.ObjectID, notice, from, to string) error {
	n, f, t, err := NormNotice(notice, from, to)
	if err != nil {
		return err
	}
	_, err = s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"notice": n, "notice_from": f, "notice_to": t,
	}})
	return err
}

// ActiveNotice 当前生效的公告：内容为空或没有完整时间窗（起止都要有）返回空；
// 时间窗含首尾。today 为门诊时区的 YYYY-MM-DD（服务器判定，前端不用管时间窗）。
func (t *Tenant) ActiveNotice(today string) string {
	if t.Notice == "" || t.NoticeFrom == "" || t.NoticeTo == "" {
		return ""
	}
	if today < t.NoticeFrom || today > t.NoticeTo {
		return ""
	}
	return t.Notice
}

// SlotWindow 出号时间窗：配了放号两段用它（门诊设置必填，新存的数据一定有）；
// 没有才兜历史数据：老单段 slot_from/slot_to → 营业时段；都空=全天。
func (t *Tenant) SlotWindow() []model.HourSeg {
	if len(t.SlotSegs) > 0 {
		return t.SlotSegs
	}
	if t.SlotFrom != "" && t.SlotTo != "" {
		return []model.HourSeg{{Open: t.SlotFrom, Close: t.SlotTo}}
	}
	return t.BusinessHours()
}

// SetSlotSegs 存放号时间两段（门诊设置，两段必填）：校验通过才落库；
// 写入时顺手清掉老单段字段，保证只有一处真源。
func (s *Service) SetSlotSegs(ctx context.Context, id bson.ObjectID, segs []model.HourSeg) error {
	norm, err := NormSlotSegs(segs)
	if err != nil {
		return err
	}
	_, err = s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{
		"$set":   bson.M{"slot_segs": norm},
		"$unset": bson.M{"slot_from": "", "slot_to": ""},
	})
	return err
}

// wxAppIDRe 微信小程序 AppID 格式（wx + 16 位十六进制）。
var wxAppIDRe = regexp.MustCompile(`^wx[0-9a-fA-F]{16}$`)

// EnsureIndexes 微信 appid 非空唯一（一个小程序只绑一个门诊；空值不限）。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	_, err := s.col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "wechat_appid", Value: 1}},
		Options: options.Index().SetUnique(true).
			SetPartialFilterExpression(bson.M{"wechat_appid": bson.M{"$gt": ""}}),
	})
	return err
}

// ByAppID 按绑定的微信小程序 appid 定位租户（小程序 appid 头解析用）。
func (s *Service) ByAppID(ctx context.Context, appid string) (*Tenant, error) {
	var t Tenant
	if err := s.col.FindOne(ctx, bson.M{"wechat_appid": appid}).Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// SetWechatAppID 绑定微信小程序 AppID（门诊设置）：格式 + 跨租户唯一；空=解绑。
func (s *Service) SetWechatAppID(ctx context.Context, id bson.ObjectID, appid string) error {
	appid = strings.TrimSpace(appid)
	if appid == "" {
		_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$unset": bson.M{"wechat_appid": ""}})
		return err
	}
	if !wxAppIDRe.MatchString(appid) {
		return errors.New("微信小程序 AppID 格式不对（wx 开头 18 位）")
	}
	var other Tenant
	if err := s.col.FindOne(ctx, bson.M{"wechat_appid": appid, "_id": bson.M{"$ne": id}}).Decode(&other); err == nil {
		return errors.New("该 AppID 已绑定其他门诊")
	}
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"wechat_appid": appid}})
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

// SetProfile 更新诊所地址/联系方式（门诊后台首页配置维护，患者端首页展示）。
// 营业时间已移到门诊设置按段维护，不在这里改。
func (s *Service) SetProfile(ctx context.Context, id bson.ObjectID, address, contact string) error {
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"address": address, "contact": contact,
	}})
	return err
}

// homeImageField 图位 kind → bson 字段名（hero=首页大图，clinic=门诊卡图）。
func homeImageField(kind string) (string, bool) {
	switch kind {
	case "hero":
		return "hero_image", true
	case "clinic":
		return "clinic_image", true
	}
	return "", false
}

// SetHomeImage 设置首页图位（GridFS 文件 ID hex）。
func (s *Service) SetHomeImage(ctx context.Context, id bson.ObjectID, kind, fileID string) error {
	field, ok := homeImageField(kind)
	if !ok {
		return errors.New("未知图位")
	}
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{field: fileID}})
	return err
}

// ClearHomeImage 清空首页图位。
func (s *Service) ClearHomeImage(ctx context.Context, id bson.ObjectID, kind string) error {
	field, ok := homeImageField(kind)
	if !ok {
		return errors.New("未知图位")
	}
	_, err := s.col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$unset": bson.M{field: ""}})
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
