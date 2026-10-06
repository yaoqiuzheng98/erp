package appointment

import (
	"context"
	"errors"
	"hash/fnv"
	"regexp"
	"strings"
	"sync"
	"time"

	"erp/internal/billing"
	"erp/internal/dental/catalog"
	"erp/internal/dental/patient"
	"erp/internal/dental/treatment"
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
	db         *mongo.Database
	seq        *seqno.Generator
	billing    *billing.Service
	appts      *repo.TenantRepo[Appointment]
	pats       *patient.Service
	items      *catalog.Service
	users      *auth.Service
	treatments *treatment.Service
	// stripes 同键串行锁（防并发重约/挂号费重单）：key 越细粒度并发越高，
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

// New 装配预约服务；医生即"可接诊的在职员工"（用户）。
func New(db *mongo.Database, seq *seqno.Generator, b *billing.Service,
	pats *patient.Service, items *catalog.Service, users *auth.Service, treats *treatment.Service) *Service {
	return &Service{
		db: db, seq: seq, billing: b, appts: repo.NewTenantRepo[Appointment](db, "appointments"),
		pats: pats, items: items, users: users, treatments: treats,
	}
}

// fillItems 按价目表回填明细快照（防前端改价），并汇总显示串与合计。
func (s *Service) fillItems(ctx context.Context, tenantID bson.ObjectID, in []ApptItem) ([]ApptItem, string, float64, error) {
	out := make([]ApptItem, 0, len(in))
	var total float64
	names := []string{}
	for _, l := range in {
		if l.Qty <= 0 {
			continue
		}
		if l.ServiceID.IsZero() {
			// 手工项：名称必填，单价以传入为准
			if l.Name == "" || l.Price < 0 {
				continue
			}
			amt := l.Qty * l.Price
			out = append(out, ApptItem{
				Name: l.Name, Qty: l.Qty, Price: l.Price, Amount: amt,
			})
			total += amt
			names = append(names, l.Name)
			continue
		}
		si, err := s.items.ByID(ctx, tenantID, l.ServiceID)
		if err != nil {
			return nil, "", 0, errors.New("价目项目不存在")
		}
		if si.Status != "active" {
			return nil, "", 0, errors.New("项目已停用：" + si.Name)
		}
		amt := l.Qty * si.Price
		out = append(out, ApptItem{
			ServiceID: si.ID, Name: si.Name,
			Qty: l.Qty, Price: si.Price, Amount: amt,
		})
		total += amt
		names = append(names, si.Name)
	}
	return out, strings.Join(names, "、"), total, nil
}

func (s *Service) doctorName(ctx context.Context, tenantID, doctorID bson.ObjectID) (string, error) {
	d, err := s.users.UserByID(ctx, tenantID, doctorID)
	if err != nil {
		return "", errors.New("医生不存在")
	}
	if d.Status != "active" || !d.CanPractice {
		return "", errors.New("医生不在职或不可接诊")
	}
	return d.Name, nil
}

// EnsureIndexes 排号部分唯一：只有已取号（queue_no>0）的单据参与唯一约束。
// 注意不能用 sparse——复合稀疏索引会把缺字段当 null，同医生同天第二单就撞键。
func (s *Service) EnsureIndexes(ctx context.Context) error {
	// 删掉历史错误版本（同名不同选项会报 IndexOptionsConflict）。
	_ = s.appts.Col.Indexes().DropOne(ctx, "tenant_id_1_doctor_id_1_date_1_queue_no_1")
	_, err := s.appts.Col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "doctor_id", Value: 1},
			{Key: "date", Value: 1}, {Key: "queue_no", Value: 1}},
		Options: options.Index().SetUnique(true).
			SetPartialFilterExpression(bson.M{"queue_no": bson.M{"$gt": 0}}),
	})
	return err
}

func (s *Service) Create(ctx context.Context, tenantID bson.ObjectID, a *Appointment) error {
	p, err := s.pats.ByID(ctx, tenantID, a.PatientID)
	if err != nil {
		return errors.New("患者不存在")
	}
	// 同医生同时段串行化：查（有无占位）与写（插入）之间不许插并发，否则双人同 slot 重约
	unlock := s.stripe("slot:" + a.DoctorID.Hex() + ":" + a.Date + ":" + a.Slot)
	defer unlock()
	// 日期时段只收合法格式，脏数据进库后筛选查不到
	if _, ok := tz.DayStart(a.Date); !ok {
		return errors.New("日期格式不正确")
	}
	if !slotRe.MatchString(a.Slot) {
		return errors.New("时间格式不正确")
	}
	a.PatientName = p.Name
	// 医生可选：不选则签到时分配；选了则校验并快照
	if !a.DoctorID.IsZero() {
		name, err := s.doctorName(ctx, tenantID, a.DoctorID)
		if err != nil {
			return err
		}
		a.Doctor = name
		// 同医生同时段防重（自助约诊必需；前台走同一入口同样受检）。
		if n, _ := s.appts.Count(ctx, tenantID, bson.M{
			"doctor_id": a.DoctorID, "date": a.Date, "slot": a.Slot,
			"status": bson.M{"$in": []string{Booked, Arrived, Serving}},
		}); n > 0 {
			return errors.New("该医生该时段已约满，换个时间试试")
		}
	}
	if len(a.Items) > 0 {
		items, summary, _, err := s.fillItems(ctx, tenantID, a.Items)
		if err != nil {
			return err
		}
		a.Items, a.Item = items, summary
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

func (s *Service) List(ctx context.Context, tenantID bson.ObjectID, date string, doctorID bson.ObjectID, phone string, skip, limit int64) ([]Appointment, int64, error) {
	f := bson.M{}
	if date != "" {
		f["date"] = date
	}
	if !doctorID.IsZero() {
		f["doctor_id"] = doctorID
	}
	if phone != "" {
		// 按患者电话筛：先找出匹配患者（上限放宽，超大门诊也够用）
		pats, _, err := s.pats.List(ctx, tenantID, phone, 0, 2000)
		if err != nil {
			return nil, 0, err
		}
		ids := make([]bson.ObjectID, 0, len(pats))
		for _, p := range pats {
			ids = append(ids, p.ID)
		}
		f["patient_id"] = bson.M{"$in": ids}
	}
	total, err := s.appts.Count(ctx, tenantID, f)
	if err != nil {
		return nil, 0, err
	}
	list, err := s.appts.FindMany(ctx, tenantID, f, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetSkip(skip).SetLimit(limit))
	return list, total, err
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

// activeLoad 当天某医生手上未完结的单数（候诊+就诊中）。
func (s *Service) activeLoad(ctx context.Context, tenantID, doctorID bson.ObjectID, date string) int64 {
	n, _ := s.appts.Count(ctx, tenantID, bson.M{
		"doctor_id": doctorID, "date": date,
		"status": bson.M{"$in": []string{Arrived, Serving}},
	})
	return n
}

// pickDoctor 签到分配：有空闲医生（当天手上没活）用第一个；
// 都没有则取当天负载最小的医生。无可接诊医生时报错。
func (s *Service) pickDoctor(ctx context.Context, tenantID bson.ObjectID, date string) (bson.ObjectID, string, error) {
	docs, err := s.users.ListPractitioners(ctx, tenantID)
	if err != nil || len(docs) == 0 {
		return bson.NilObjectID, "", errors.New("暂无可接诊医生")
	}
	for _, d := range docs {
		if s.activeLoad(ctx, tenantID, d.ID, date) == 0 {
			return d.ID, d.Name, nil
		}
	}
	best, bestLoad := docs[0], s.activeLoad(ctx, tenantID, docs[0].ID, date)
	for _, d := range docs[1:] {
		if l := s.activeLoad(ctx, tenantID, d.ID, date); l < bestLoad {
			best, bestLoad = d, l
		}
	}
	return best.ID, best.Name, nil
}

// nextQueueNo 当天当医生下一个排号（已用号+1）。
// 注意：排号一旦分配就占号（开单待缴费也占），必须统计所有带号单据，
// 否则下一位签到会撞唯一索引。
func (s *Service) nextQueueNo(ctx context.Context, tenantID, doctorID bson.ObjectID, date string) int {
	n, _ := s.appts.Count(ctx, tenantID, bson.M{
		"doctor_id": doctorID, "date": date,
		"queue_no": bson.M{"$gt": 0},
	})
	return int(n) + 1
}

// CheckIn 签到：booked → arrived。已指定医生则进该医生队列（医生失效则自动改派）；
// 未指定医生则按空闲优先/最短队分配。返回最终医生名与排号。
func (s *Service) CheckIn(ctx context.Context, tenantID, id bson.ObjectID) (string, int, error) {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return "", 0, err
	}
	if a.Status != Booked {
		return "", 0, ErrBadStatus
	}
	docID, docName := a.DoctorID, a.Doctor
	if docID.IsZero() {
		var err error
		docID, docName, err = s.pickDoctor(ctx, tenantID, a.Date)
		if err != nil {
			return "", 0, err
		}
	} else if name, err := s.doctorName(ctx, tenantID, docID); err != nil {
		docID, docName, err = s.pickDoctor(ctx, tenantID, a.Date)
		if err != nil {
			return "", 0, err
		}
	} else {
		docName = name
	}
	set := bson.M{"status": Arrived, "doctor_id": docID, "doctor": docName}
	// 排号带重试：同号撞唯一索引则重取；状态被人动过（matched=0）则报状态错，不重试
	var lastErr error
	for i := 0; i < 3; i++ {
		qno := s.nextQueueNo(ctx, tenantID, docID, a.Date)
		set["queue_no"] = qno
		matched, err := s.appts.UpdateWhere(ctx, tenantID,
			bson.M{"_id": id, "status": Booked}, set)
		if err != nil {
			if !mongo.IsDuplicateKeyError(err) {
				return "", 0, err
			}
			lastErr = err
			continue
		}
		if matched == 0 {
			return "", 0, ErrBadStatus
		}
		return docName, qno, nil
	}
	return "", 0, lastErr
}

// StartServe 就诊：arrived → serving。
func (s *Service) StartServe(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Arrived}, Serving)
	return err
}

// nextInLine 同医生当天排号最小的候诊单（自动叫号用）。
func (s *Service) nextInLine(ctx context.Context, tenantID, doctorID bson.ObjectID, date string) (*Appointment, error) {
	list, err := s.appts.FindMany(ctx, tenantID,
		bson.M{"doctor_id": doctorID, "date": date, "status": Arrived})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	best := list[0]
	for _, a := range list[1:] {
		if a.QueueNo < best.QueueNo {
			best = a
		}
	}
	return &best, nil
}

// PayReg 挂号费模拟支付：建应收并即时全额核销（method=mock），成功置 RegPaid。
// 仅 booked/arrived 可缴；费用为0或已缴直接返回 nil。
func (s *Service) PayReg(ctx context.Context, tenantID, id bson.ObjectID, method, by string) error {
	// 同预约串行化：查（有无 RG 单）与建（新 RG 单）之间不许插并发，否则挂号费重单
	unlock := s.stripe("payreg:" + id.Hex())
	defer unlock()
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if a.Status != Booked && a.Status != Arrived {
		return ErrBadStatus
	}
	if a.RegFee <= 0 || a.RegPaid {
		return nil
	}
	if !billing.ValidMethod(method) {
		return errors.New("未知收款方式")
	}
	// 幂等修复：之前建了 RG 单但没置位（崩溃/重试），直接续上不重单
	if eb, err := s.billing.ByRefPrefix(ctx, tenantID, id.Hex(), "RG"); err != nil {
		return err
	} else if eb != nil {
		if eb.Status != billing.BillPaid {
			rest := eb.Amount - eb.PaidAmount
			if rest > 0 {
				if _, err := s.billing.Pay(ctx, tenantID, eb.ID, rest, method, by); err != nil {
					return err
				}
			}
		}
		return s.appts.Update(ctx, tenantID, id, bson.M{"reg_paid": true})
	}
	no, err := s.seq.Next(ctx, tenantID, "RG")
	if err != nil {
		return err
	}
	if err := s.billing.CreateAR(ctx, tenantID, billing.AR{
		PatientID: a.PatientID, PatientName: a.PatientName,
		DocNo: no, Amount: a.RegFee, RefID: id.Hex(), By: by,
		Lines: []billing.BillLine{{Name: "挂号费", Qty: 1, Price: a.RegFee, Amount: a.RegFee}},
	}); err != nil {
		return err
	}
	b, err := s.billing.ByDocNo(ctx, tenantID, no)
	if err != nil || b == nil {
		if err == nil {
			err = errors.New("收费单生成异常")
		}
		return err
	}
	if _, err := s.billing.Pay(ctx, tenantID, b.ID, a.RegFee, method, by); err != nil {
		return err
	}
	return s.appts.Update(ctx, tenantID, id, bson.M{"reg_paid": true})
}

// Position 排位：serving=0（正在就诊），候诊=前面人数+1，其他状态=-1。
func (s *Service) Position(ctx context.Context, tenantID, id bson.ObjectID) int {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return -1
	}
	switch a.Status {
	case Serving:
		return 0
	case Arrived:
		n, _ := s.appts.Count(ctx, tenantID, bson.M{
			"doctor_id": a.DoctorID, "date": a.Date, "status": Arrived,
			"queue_no": bson.M{"$lt": a.QueueNo},
		})
		return int(n) + 1
	default:
		return -1
	}
}

func (s *Service) NoShow(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Booked}, NoShow)
	return err
}

func (s *Service) Cancel(ctx context.Context, tenantID, id bson.ObjectID) error {
	_, err := s.setStatus(ctx, tenantID, id, []string{Booked, Arrived}, Cancel)
	return err
}

// CancelUnpaid 门诊后台取消待缴费/已接诊未收单：先作废其名下所有未收单，再取消预约。
// 未开单的走普通 Cancel；动过钱的一律拒绝（已结清/部分已收都要人工处理）；
// serving 等其他状态拒绝。返回被作废的单号（审计留痕）。
// 患者端不调这个（防患者自助作废逃费），只调 Cancel。
func (s *Service) CancelUnpaid(ctx context.Context, tenantID, id bson.ObjectID) ([]string, error) {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	switch a.Status {
	case Booked, Arrived:
		return nil, s.Cancel(ctx, tenantID, id)
	case Unpaid, Done:
	default:
		return nil, ErrBadStatus
	}
	// 有收款记录（已结清/部分已收）不自动取消：钱动了必须人工处理
	bills, err := s.billing.BillsByRef(ctx, tenantID, id.Hex())
	if err != nil {
		return nil, err
	}
	var received float64
	for _, b := range bills {
		received += b.PaidAmount
	}
	if received > billing.Epsilon {
		return nil, errors.New("已有收款记录，无法取消，请人工处理")
	}
	voided, err := s.billing.VoidOpenByRef(ctx, tenantID, id.Hex())
	if err != nil {
		return nil, err
	}
	matched, err := s.appts.UpdateWhere(ctx, tenantID,
		bson.M{"_id": id, "status": a.Status}, bson.M{"status": Cancel})
	if err != nil {
		return voided, err
	}
	if matched == 0 && !s.cancelConverged(ctx, tenantID, id) {
		return voided, ErrBadStatus
	}
	return voided, nil
}

// cancelConverged 取消是否已收敛到一致态：预约已是取消，或（已接诊且名下无未收单）。
// 并发中被别人先处理完时直接成功，不报状态错。
func (s *Service) cancelConverged(ctx context.Context, tenantID, id bson.ObjectID) bool {
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return false
	}
	if a.Status == Cancel {
		return true
	}
	bills, err := s.billing.BillsByRef(ctx, tenantID, id.Hex())
	if err != nil {
		return false
	}
	for _, b := range bills {
		if b.Status == billing.BillOpen {
			return false
		}
	}
	return true
}

// Complete 开单：仅就诊中（serving）可开单，不许跳过叫号。
// 优先按明细结算，否则沿用预约存量明细，再否则用手工 charge。
// deduct 为真且已缴挂号费时，挂号费当定金抵扣（抵扣额=min(挂号费,小计)，明细列抵扣行）。
// 开诊疗单 + 建费用单（1:1）+ 预约落已接诊；前台分次收款收清后诊疗单翻已收。
// 幂等：并发第二人/崩溃重试命中诊疗单唯一索引后走修复（补单补翻），直接成功。
// 返回下一位患者姓名（无则空）。
func (s *Service) Complete(ctx context.Context, tenantID, id bson.ObjectID, items []ApptItem, charge float64, deduct bool, diagnosis, result, by string) (string, error) {
	// 同预约串行化：查（状态）算（总额）写（诊疗单+费用单+翻转）一气呵成，
	// 并发第二人等第一人落完再读到 done，直接幂等成功
	unlock := s.stripe("complete:" + id.Hex())
	defer unlock()
	a, err := s.appts.FindByID(ctx, tenantID, id)
	if err != nil {
		return "", err
	}
	if a.Status == Done {
		// 已开过：幂等成功（刷新/双击/重试）
		return "", nil
	}
	if a.Status != Serving {
		return "", ErrBadStatus
	}
	finalItems := a.Items
	summary := a.Item
	total := charge
	if len(items) > 0 {
		filled, sum, t, err := s.fillItems(ctx, tenantID, items)
		if err != nil {
			return "", err
		}
		finalItems, summary, total = filled, sum, t
	} else if len(a.Items) > 0 {
		var t float64
		for _, it := range a.Items {
			t += it.Amount
		}
		total = t
	}
	if total < 0 {
		return "", errors.New("收费额不能为负")
	}
	if deduct && a.RegPaid && a.RegFee > 0 && total > 0 {
		d := a.RegFee
		if d > total {
			d = total
		}
		finalItems = append(finalItems, ApptItem{Name: "挂号费抵扣", Qty: 1, Price: -d, Amount: -d})
		summary += "、挂号费抵扣"
		total -= d
	}
	tItems := make([]treatment.Item, 0, len(finalItems))
	lines := make([]billing.BillLine, 0, len(finalItems))
	for _, it := range finalItems {
		tItems = append(tItems, treatment.Item{
			ServiceID: it.ServiceID, Name: it.Name, Qty: it.Qty, Price: it.Price, Amount: it.Amount,
		})
		lines = append(lines, billing.BillLine{
			Name: it.Name, Qty: it.Qty, Price: it.Price, Amount: it.Amount,
		})
	}
	tStatus := treatment.Billed
	billNo := ""
	if total == 0 {
		// 零收费直接完结，不走应收
		tStatus = treatment.Paid
	} else {
		var err error
		billNo, err = s.seq.Next(ctx, tenantID, "CH")
		if err != nil {
			return "", err
		}
	}
	t := &treatment.Treatment{
		ApptID: a.ID, PatientID: a.PatientID, PatientName: a.PatientName,
		DoctorID: a.DoctorID, Doctor: a.Doctor, Date: a.Date, Slot: a.Slot,
		Item: summary, Items: tItems, Diagnosis: diagnosis, Result: result,
		Total: total, BillNo: billNo, Status: tStatus,
	}
	if err := s.treatments.Insert(ctx, tenantID, t); err != nil {
		if err == treatment.ErrExists {
			// 并发第二人/崩溃重试：补齐费用单和预约翻转，直接成功
			return "", s.repairComplete(ctx, tenantID, a)
		}
		return "", err
	}
	if total > 0 {
		if err := s.billing.CreateAR(ctx, tenantID, billing.AR{
			PatientID: a.PatientID, PatientName: a.PatientName,
			DocNo: billNo, Amount: total, Lines: lines,
			RefID: id.Hex(), TreatmentID: t.ID, By: by,
		}); err != nil {
			// 建单失败删诊疗单回滚（最佳努力），预约仍是 serving，可重试
			s.treatments.DeleteByAppt(ctx, tenantID, id)
			return "", err
		}
	}
	// 预约落已接诊 + 显示快照（列表/旧读数用，权威值在诊疗单）
	snap := bson.M{"status": Done, "charge": total, "charge_no": billNo,
		"items": finalItems, "item": summary, "diagnosis": diagnosis, "result": result}
	if _, err := s.appts.UpdateWhere(ctx, tenantID,
		bson.M{"_id": id, "status": Serving}, snap); err != nil {
		return "", err
	}
	// 下一位只提示、不自动流转：前台点叫号播报后再点就诊
	next, err := s.nextInLine(ctx, tenantID, a.DoctorID, a.Date)
	if err != nil || next == nil {
		return "", nil
	}
	return next.PatientName, nil
}

// repairComplete 开单修复：诊疗单已存在时补齐费用单和预约翻转（幂等）。
func (s *Service) repairComplete(ctx context.Context, tenantID bson.ObjectID, a *Appointment) error {
	t, err := s.treatments.ByAppt(ctx, tenantID, a.ID)
	if err != nil || t == nil {
		return ErrBadStatus
	}
	if t.Total > 0 && t.BillNo != "" {
		if b, _ := s.billing.BillByRef(ctx, tenantID, a.ID.Hex()); b == nil {
			lines := make([]billing.BillLine, 0, len(t.Items))
			for _, it := range t.Items {
				lines = append(lines, billing.BillLine{
					Name: it.Name, Qty: it.Qty, Price: it.Price, Amount: it.Amount,
				})
			}
			if err := s.billing.CreateAR(ctx, tenantID, billing.AR{
				PatientID: t.PatientID, PatientName: t.PatientName,
				DocNo: t.BillNo, Amount: t.Total, Lines: lines,
				RefID: a.ID.Hex(), TreatmentID: t.ID, By: "",
			}); err != nil {
				return err
			}
		}
	}
	snap := bson.M{"status": Done, "charge": t.Total, "charge_no": t.BillNo,
		"items": a.Items, "item": t.Item, "diagnosis": t.Diagnosis, "result": t.Result}
	matched, err := s.appts.UpdateWhere(ctx, tenantID,
		bson.M{"_id": a.ID, "status": Serving}, snap)
	if err != nil {
		return err
	}
	if matched == 0 {
		// 预约已不在 serving：done 即成功，其他状态报状态错
		if cur, ferr := s.appts.FindByID(ctx, tenantID, a.ID); ferr != nil {
			return ferr
		} else if cur.Status != Done {
			return ErrBadStatus
		}
	}
	return nil
}
