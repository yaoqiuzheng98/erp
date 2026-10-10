package appointment

import (
	"context"
	"errors"
	"hash/fnv"
	"regexp"
	"strconv"
	"sync"
	"time"

	"erp/internal/dental/patient"
	"erp/internal/platform/auth"
	"erp/internal/platform/repo"
	"erp/internal/platform/seqno"
	"erp/internal/platform/tz"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var ErrBadStatus = errors.New("状态不允许该操作")

// slotRe HH:MM 24小时制。
var slotRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

type Service struct {
	db    *mongo.Database
	seq   *seqno.Generator
	appts *repo.TenantRepo[Appointment]
	pats  *patient.Service
	users *auth.Service
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

// New 装配预约服务；接诊医生即在职员工。
func New(db *mongo.Database, seq *seqno.Generator,
	pats *patient.Service, users *auth.Service) *Service {
	return &Service{
		db: db, seq: seq, appts: repo.NewTenantRepo[Appointment](db, "appointments"),
		pats: pats, users: users,
	}
}

func (s *Service) doctorName(ctx context.Context, tenantID, doctorID bson.ObjectID) (string, error) {
	d, err := s.users.UserByID(ctx, tenantID, doctorID)
	if err != nil {
		return "", errors.New("医生不存在")
	}
	if d.Status != "active" {
		return "", errors.New("医生不在职")
	}
	return d.Name, nil
}

func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, a *Appointment, slotMinutes, slotCapacity int) error {
	p, err := s.pats.ByID(ctx, tenantID, a.PatientID)
	if err != nil {
		return errors.New("患者不存在")
	}
	minutes, capacity := NormSlotConfig(slotMinutes, slotCapacity)
	// 同医生同档串行化：查（档内人数）与写（插入）之间不许插并发，否则同档超售。
	// 档位严格对齐后同档即同时刻，锁键直接用日期+时段。
	unlock := s.stripe("slot:" + a.DoctorID.Hex() + ":" + a.Date + ":" + a.Slot)
	defer unlock()
	// 日期时段只收合法格式，脏数据进库后筛选查不到
	if _, ok := tz.DayStart(a.Date); !ok {
		return errors.New("日期格式不正确")
	}
	if !slotRe.MatchString(a.Slot) {
		return errors.New("时间格式不正确")
	}
	// 档位对齐：只收放号档起点（如 30 分钟档只收 :00/:30），界面只给档位下拉，这里防绕过直调
	if mm, _ := strconv.Atoi(a.Slot[3:]); mm%minutes != 0 {
		return errors.New("预约时间不在放号时段内")
	}
	a.PatientName = p.Name
	// 医生可选：不选则签到时分配；选了则校验并快照
	if !a.DoctorID.IsZero() {
		name, err := s.doctorName(ctx, tenantID, a.DoctorID)
		if err != nil {
			return err
		}
		a.Doctor = name
		// 同医生同档防超售（自助约诊必需；前台走同一入口同样受检）。
		// 未完结的 noshow/cancel 不占档。
		if n, _ := s.appts.Count(ctx, tenantID, bson.M{
			"doctor_id": a.DoctorID, "date": a.Date, "slot": a.Slot,
			"status": Booked,
		}); n >= int64(capacity) {
			return errors.New("该时段已约满，换个时间试试")
		}
	}
	a.TenantID, a.Status, a.CreatedAt = tenantID, Booked, time.Now()
	id, err := s.appts.Insert(ctx, tenantID, a)
	if err != nil {
		return errors.New("建单失败: " + err.Error())
	}
	a.ID = id
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

// ListRange 按日期闭区间查（周视图用）：同 List 的医生/电话筛选，
// 按日期+时段排序，不分页（周数据有界，limit 只做兜底）。
func (s *Service) ListRange(ctx context.Context, tenantID bson.ObjectID, start, end string, doctorID bson.ObjectID, phone string, limit int64) ([]Appointment, error) {
	f := bson.M{"date": bson.M{"$gte": start, "$lte": end}}
	if !doctorID.IsZero() {
		f["doctor_id"] = doctorID
	}
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

// pickDoctor 签到分配：无指定医生时取首位在职员工。
func (s *Service) pickDoctor(ctx context.Context, tenantID bson.ObjectID) (bson.ObjectID, string, error) {
	docs, err := s.users.ListActive(ctx, tenantID)
	if err != nil || len(docs) == 0 {
		return bson.NilObjectID, "", errors.New("暂无在职员工")
	}
	d := docs[0]
	return d.ID, d.Name, nil
}

// CheckIn 签到：booked → done（签到即完成）。已指定医生则保留（医生失效则自动改派）；
// 未指定医生则取首位在职员工。返回最终医生名。
func (s *Service) CheckIn(ctx context.Context, tenantID, id bson.ObjectID) (string, error) {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return "", err
	}
	if a.Status != Booked {
		return "", ErrBadStatus
	}
	docID, docName := a.DoctorID, a.Doctor
	if docID.IsZero() {
		var err error
		docID, docName, err = s.pickDoctor(ctx, tenantID)
		if err != nil {
			return "", err
		}
	} else if name, err := s.doctorName(ctx, tenantID, docID); err != nil {
		docID, docName, err = s.pickDoctor(ctx, tenantID)
		if err != nil {
			return "", err
		}
	} else {
		docName = name
	}
	matched, err := s.appts.UpdateWhere(ctx, tenantID,
		bson.M{"_id": id, "status": Booked},
		bson.M{"status": Done, "doctor_id": docID, "doctor": docName})
	if err != nil {
		return "", err
	}
	if matched == 0 {
		return "", ErrBadStatus
	}
	return docName, nil
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
