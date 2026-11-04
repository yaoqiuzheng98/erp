package appointment

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"erp/internal/dental/patient"
	"erp/internal/platform/model"
	"erp/internal/platform/repo"
	"erp/internal/platform/seqno"
	"erp/internal/platform/tz"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var ErrBadStatus = errors.New("状态不允许该操作")

// ErrOneActive 一人只能保留一笔已预约（自助与前台同一入口）。
var ErrOneActive = errors.New("每人只能保留一笔已预约的单")

// slotRe HH:MM 24小时制。
var slotRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

type Service struct {
	db    *mongo.Database
	seq   *seqno.Generator
	appts *repo.TenantRepo[Appointment]
	pats  *patient.Service
	// stripes 同键串行锁（防并发重约）：key 越细粒度并发越高，
	// 同 key 同时只进一个。单实例部署有效（compose 单副本），扩多副本换分布式锁。
	stripes [64]sync.Mutex
}

// stripe 取 key 对应的条带锁，返回解锁函数（defer 用）。
func (s *Service) stripe(key string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	m := &s.stripes[h.Sum32()%uint32(len(s.stripes))]
	m.Lock()
	return m.Unlock
}

// EnsureIndexes 一人一单部分唯一索引（只看 booked，done/noshow/cancel 不占）：
// 先查后建的竞态由索引兜底，并发第二人直接 duplicate key。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	_, err := s.appts.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "patient_id", Value: 1}},
		Options: options.Index().SetUnique(true).
			SetPartialFilterExpression(bson.M{"status": Booked}),
	})
	return err
}

// New 装配预约服务。
func New(db *mongo.Database, seq *seqno.Generator,
	pats *patient.Service) *Service {
	return &Service{
		db: db, seq: seq, appts: repo.NewTenantRepo[Appointment](db, "appointments"),
		pats: pats,
	}
}

// BookOpts 建单上下文：门诊配置 + 医生解析，收敛到一处（避免参数继续膨胀）。
type BookOpts struct {
	Minutes   int                                   // 放号粒度（15/30/60）
	Biz       []model.HourSeg                       // 放号时间窗（空=全天）
	Rest      func(string) bool                     // 休息日判定（nil=不限，测试用）
	Doctor    func(id bson.ObjectID) (string, bool) // 可接诊医生解析：id→姓名（nil=不校验，测试用）
	AllowPast bool                                  // 仅测试/历史补录用；线上入口默认 false
	AllowSpan bool                                  // 仅后台补录允许长时段（占多个档）；患者端默认 false
}

func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, a *Appointment, o BookOpts) error {
	p, err := s.pats.ByID(ctx, tenantID, a.PatientID)
	if err != nil {
		return errors.New("患者不存在")
	}
	// 预约必须指定医生：一个时段，一个医生只能约一个号。
	if a.DoctorID.IsZero() {
		return errors.New("请选择医生")
	}
	if o.Doctor != nil {
		name, ok := o.Doctor(a.DoctorID)
		if !ok {
			return errors.New("医生不存在或不可接诊")
		}
		a.Doctor = name
	}
	minutes, _ := NormSlotConfig(o.Minutes, 1)
	// 同医生同天串行化：长单会跨多个档，按档锁挡不住重叠区间（09:00 单档 vs 08:30 长单），
	// 所以锁到「医生+日期」；查（重叠）与写（插入）之间不许插并发，否则超售。
	unlock := s.stripe("doctor:" + a.Date + ":" + a.DoctorID.Hex())
	defer unlock()
	// 日期时段只收合法格式，脏数据进库后筛选查不到
	if _, ok := tz.DayStart(a.Date); !ok {
		return errors.New("日期格式不正确")
	}
	// 门诊休息日（每周固定休/指定日期休）：不放号，任何入口都不能建单。
	if o.Rest != nil && o.Rest(a.Date) {
		return errors.New("当天门诊休息，不能预约")
	}
	if !slotRe.MatchString(a.Slot) {
		return errors.New("时间格式不正确")
	}
	// 档位对齐：只收放号档起点（如 30 分钟档只收 :00/:30），界面只给档位下拉，这里防绕过直调
	if mm, _ := strconv.Atoi(a.Slot[3:]); mm%minutes != 0 {
		return errors.New("预约时间不在放号时段内")
	}
	// 过去日期/今天已过的档位一律拒绝（前端可绕过，服务端兜底）。
	if !o.AllowPast {
		if err := rejectPastTime(a.Date, a.Slot); err != nil {
			return err
		}
	}
	// 结束时间：后台可传 end_slot 占多个档；患者端不传，默认单档并补写结束时间。
	slotStart := segMinutes(a.Slot)
	a.EndSlot = strings.TrimSpace(a.EndSlot)
	spanEnd := 0
	if a.EndSlot == "" {
		end, ok := slotAdd(a.Slot, minutes)
		if !ok {
			return errors.New("时间格式不正确")
		}
		a.EndSlot = end
		spanEnd = slotStart + minutes
	} else {
		if !o.AllowSpan {
			return errors.New("该入口不支持长时段预约")
		}
		if !slotRe.MatchString(a.EndSlot) {
			return errors.New("结束时间格式不正确")
		}
		spanEnd = segMinutes(a.EndSlot)
		if spanEnd <= slotStart {
			return errors.New("结束时间必须晚于开始时间")
		}
		if (spanEnd-slotStart)%minutes != 0 {
			return errors.New("占用时长必须是放号粒度的整数倍")
		}
	}
	// 放号时间窗：窗外不收（防绕过界面直调，前台补录同样受检）。
	if !slotInBiz(a.Slot, minutes, o.Biz) {
		return errors.New("该时间不在放号时间内")
	}
	// 长单整段必须落在同一个放号时段内，不跨午休。
	if !spanInBiz(slotStart, spanEnd, o.Biz) {
		return errors.New("占用时长不能跨出当前放号时段")
	}
	a.PatientName = p.Name
	// 同医生防超售：一个医生一个时段只收一单（自助/前台同一入口受检）。
	// 长单按区间重叠判断；noshow/cancel 的不占档。
	dayAppts, err := s.appts.FindMany(ctx, tenantID, bson.M{
		"date": a.Date, "doctor_id": a.DoctorID, "status": Booked,
	})
	if err != nil {
		return errors.New("查询预约失败: " + err.Error())
	}
	for _, x := range dayAppts {
		xs := segMinutes(x.Slot)
		if xs < 0 {
			continue
		}
		if xe := x.endMinutes(minutes); xs < spanEnd && xe > slotStart {
			return errors.New("该时段该医生已约满，换个时间或医生")
		}
	}
	// 一人一单：已有已预约的不再建（自助与前台同一入口；并发竞态由部分唯一索引兜底）。
	if n, _ := s.appts.Count(ctx, tenantID, bson.M{
		"patient_id": a.PatientID, "status": Booked,
	}); n > 0 {
		return ErrOneActive
	}
	// 预约编号：YY + 日期(紧凑) + 月内序号，如 YY202610100001。
	n, err := s.seq.NextValue(ctx, tenantID, "APPT")
	if err != nil {
		return err
	}
	a.No = fmt.Sprintf("YY%s%04d", strings.ReplaceAll(a.Date, "-", ""), n)
	a.TenantID, a.Status, a.CreatedAt = tenantID, Booked, time.Now()
	id, err := s.appts.Insert(ctx, tenantID, a)
	if err != nil {
		// 并发两人都通过预检时，唯一索引拦下第二个，转友好提示。
		if mongo.IsDuplicateKeyError(err) {
			return ErrOneActive
		}
		return errors.New("建单失败: " + err.Error())
	}
	a.ID = id
	return nil
}

// rejectPastTime 拒绝过去日期；今天只允许开始时间晚于当前时刻的档位。
// 门诊时区统一走 tz（东八区），不跟容器/手机时区漂移。
func rejectPastTime(date, slot string) error {
	day, ok := tz.DayStart(date)
	if !ok {
		return errors.New("日期格式不正确")
	}
	today, _ := tz.DayStart(tz.Today())
	if day.Before(today) {
		return errors.New("不能预约过去的时间")
	}
	if day.Equal(today) {
		start, err := time.ParseInLocation("2006-01-02 15:04", date+" "+slot, tz.Loc())
		if err != nil {
			return errors.New("时间格式不正确")
		}
		if !start.After(tz.Now()) {
			return errors.New("该时段已过，请选择之后的时间")
		}
	}
	return nil
}

func (s *Service) ByID(ctx context.Context, tenantID, id bson.ObjectID) (*Appointment, error) {
	return s.appts.FindByID(ctx, tenantID, id)
}

// patientIDsForPhone 按电话模糊找患者 ID（列表与周视图共用，收敛到一处）。
func (s *Service) patientIDsForPhone(ctx context.Context, tenantID bson.ObjectID, phone string) []bson.ObjectID {
	// 按患者电话筛：先找出匹配患者（上限放宽，超大门诊也够用）
	pats, _, err := s.pats.List(ctx, tenantID, phone, 0, 2000)
	if err != nil {
		return nil
	}
	ids := make([]bson.ObjectID, 0, len(pats))
	for _, p := range pats {
		ids = append(ids, p.ID)
	}
	return ids
}

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, date string, doctorID bson.ObjectID, phone string, skip, limit int64) ([]Appointment, int64, error) {
	f := bson.M{}
	if date != "" {
		f["date"] = date
	}
	if !doctorID.IsZero() {
		f["doctor_id"] = doctorID
	}
	if phone != "" {
		f["patient_id"] = bson.M{"$in": s.patientIDsForPhone(ctx, tenantID, phone)}
	}
	total, err := s.appts.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.appts.FindMany(ctx, tenantID, f, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetSkip(skip).SetLimit(limit))
	return list, total, err
}

// ListRange 按日期闭区间查（周视图用）：电话筛选，按日期+时段排序，
// 不分页（周数据有界，limit 只做兜底）。
func (s *Service) ListRange(ctx context.Context, tenantID bson.ObjectID, start, end string, phone string, limit int64) ([]Appointment, error) {
	f := bson.M{"date": bson.M{"$gte": start, "$lte": end}}
	if phone != "" {
		f["patient_id"] = bson.M{"$in": s.patientIDsForPhone(ctx, tenantID, phone)}
	}
	if limit <= 0 {
		limit = 1000
	}
	return s.appts.FindMany(ctx, tenantID, f, options.Find().SetSort(
		bson.D{{Key: "date", Value: 1}, {Key: "slot", Value: 1}, {Key: "_id", Value: 1}},
	).SetLimit(limit))
}

func (s *Service) OfPatient(ctx context.Context, tenantID, patID bson.ObjectID) ([]Appointment, error) {
	return s.appts.FindMany(ctx, tenantID, bson.M{"patient_id": patID})
}

// BookedCounts 某日期区间内已预约单按日期+时段计数（余量接口用）。
// 长单按 [slot,end_slot) 展开到覆盖的每个当前档位；doctorID 非零时只统计该医生。
func (s *Service) BookedCounts(ctx context.Context, tenantID bson.ObjectID, from, to string, doctorID bson.ObjectID, minutes int, slots []Slot) (map[string]map[string]int64, error) {
	if minutes <= 0 {
		minutes = DefaultSlotMinutes
	}
	f := bson.M{
		"date": bson.M{"$gte": from, "$lte": to}, "status": Booked,
	}
	if !doctorID.IsZero() {
		f["doctor_id"] = doctorID
	}
	list, err := s.appts.FindMany(ctx, tenantID, f)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]int64{}
	for _, a := range list {
		m, ok := out[a.Date]
		if !ok {
			m = map[string]int64{}
			out[a.Date] = m
		}
		start := segMinutes(a.Slot)
		if start < 0 {
			continue
		}
		end := a.endMinutes(minutes)
		if len(slots) == 0 {
			// 没有档位表时退回精确匹配（兼容不传档位的调用）。
			m[a.Slot]++
			continue
		}
		for _, sl := range slots {
			ss := segMinutes(sl.Value)
			if ss < 0 {
				continue
			}
			if ss < end && ss+minutes > start {
				m[sl.Value]++
			}
		}
	}
	return out, nil
}

func (s *Service) setStatus(ctx context.Context, tenantID, id bson.ObjectID, from []string, to string) (*Appointment, error) {
	// 条件更新一步到位：并发第二人命中为 0，直接报状态错，不会把别人的流转覆盖掉
	matched, err := s.appts.UpdateWhere(ctx, tenantID,
		bson.M{"_id": id, "status": bson.M{"$in": from}}, bson.M{"status": to})
	if err != nil {
		return nil, err
	}
	if matched == 0 {
		if _, ferr := s.appts.FindByID(ctx, tenantID, id); ferr != nil {
			return nil, ferr
		}
		return nil, ErrBadStatus
	}
	return s.appts.FindByID(ctx, tenantID, id)
}

// CheckIn 签到即完成：booked → done。医生只认建档写入的快照，不再分配/改派。
// 返回预约上的医生名（预约时未选则为空）。
func (s *Service) CheckIn(ctx context.Context, tenantID, id bson.ObjectID) (string, error) {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return "", err
	}
	if a.Status != Booked {
		return "", ErrBadStatus
	}
	matched, err := s.appts.UpdateWhere(ctx, tenantID,
		bson.M{"_id": id, "status": Booked}, bson.M{"status": Done})
	if err != nil {
		return "", err
	}
	if matched == 0 {
		return "", ErrBadStatus
	}
	return a.Doctor, nil
}

func (s *Service) NoShow(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Booked}, NoShow)
	return err
}

func (s *Service) Cancel(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Booked}, Cancel)
	return err
}

// CancelUnpaid 门诊后台取消：允许未完结的预约（已预约/候诊）取消。
func (s *Service) CancelUnpaid(ctx context.Context, tenantID, id bson.ObjectID) error {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	switch a.Status {
	case Booked:
	default:
		return ErrBadStatus
	}
	matched, err := s.appts.UpdateWhere(ctx, tenantID,
		bson.M{"_id": id, "status": a.Status}, bson.M{"status": Cancel})
	if err != nil {
		return err
	}
	if matched == 0 {
		return ErrBadStatus
	}
	return nil
}
