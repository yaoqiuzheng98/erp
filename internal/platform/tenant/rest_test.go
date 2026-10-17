package tenant

import (
	"testing"

	"erp/internal/platform/model"
)

func TestNormRest(t *testing.T) {
	// 正常：周日去重升序 + 日期去重排序
	wds, ds, err := NormRest([]int{3, 1, 3}, []string{"2026-10-01", " 2026-09-30 ", "2026-10-01", ""})
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if len(wds) != 2 || wds[0] != 1 || wds[1] != 3 {
		t.Fatalf("weekdays = %v", wds)
	}
	if len(ds) != 2 || ds[0] != "2026-09-30" || ds[1] != "2026-10-01" {
		t.Fatalf("dates = %v", ds)
	}
	// 空=清除
	if w, d, err := NormRest(nil, nil); err != nil || len(w) != 0 || len(d) != 0 {
		t.Fatalf("empty = %v %v %v", w, d, err)
	}
	// 非法星期 / 非法日期
	if _, _, err := NormRest([]int{7}, nil); err == nil {
		t.Fatal("weekday 7 should fail")
	}
	if _, _, err := NormRest(nil, []string{"2026/10/01"}); err == nil {
		t.Fatal("bad date should fail")
	}
}

func TestIsRest(t *testing.T) {
	// 2026-10-14 是周三、2026-10-15 是周四
	tn := &Tenant{RestWeekdays: []int{3, 4}}
	if !tn.IsRest("2026-10-14") || !tn.IsRest("2026-10-15") {
		t.Fatal("wed/thu should be rest")
	}
	if tn.IsRest("2026-10-16") {
		t.Fatal("fri should not be rest")
	}
	// 指定日期
	tn2 := &Tenant{RestDates: []string{"2026-10-01"}}
	if !tn2.IsRest("2026-10-01") || tn2.IsRest("2026-10-02") {
		t.Fatal("fixed date rest wrong")
	}
	// 两个都配
	tn3 := &Tenant{RestWeekdays: []int{0}, RestDates: []string{"2026-10-01"}}
	if !tn3.IsRest("2026-10-11") || !tn3.IsRest("2026-10-01") || tn3.IsRest("2026-10-12") {
		t.Fatal("combined rest wrong")
	}
	// 未配置
	tn4 := &Tenant{}
	if tn4.IsRest("2026-10-14") {
		t.Fatal("no config should not rest")
	}
	// 脏日期不炸
	if tn.IsRest("bad") {
		t.Fatal("bad date should not rest")
	}
}

func TestRestWeekTextAndHours(t *testing.T) {
	tn := &Tenant{HoursSeg: []model.HourSeg{{Open: "09:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}}}
	if got := tn.HoursText(); got != "9:00-12:00 14:00-18:00" {
		t.Fatalf("no rest = %q", got)
	}
	// 每周二休：末尾加"每周二休息"
	tn.RestWeekdays = []int{2}
	if got := tn.RestWeekText(); got != "每周二休息" {
		t.Fatalf("single = %q", got)
	}
	if got := tn.HoursText(); got != "9:00-12:00 14:00-18:00 每周二休息" {
		t.Fatalf("hours = %q", got)
	}
	// 多日按周一到周日排序
	tn.RestWeekdays = []int{4, 3}
	if got := tn.RestWeekText(); got != "每周三、周四休息" {
		t.Fatalf("multi = %q", got)
	}
	// 只配休息、无营业时间也展示
	tn2 := &Tenant{RestWeekdays: []int{2}}
	if got := tn2.HoursText(); got != "每周二休息" {
		t.Fatalf("rest only = %q", got)
	}
	// 老文本回落也带后缀
	tn3 := &Tenant{Hours: "9:00-18:00", RestWeekdays: []int{0}}
	if got := tn3.HoursText(); got != "9:00-18:00 每周日休息" {
		t.Fatalf("legacy = %q", got)
	}
}

func TestNoticeWindow(t *testing.T) {
	// 校验归一：内容+起止时间窗都必填
	if _, _, _, err := NormNotice("", "2026-10-01", "2026-10-07"); err == nil {
		t.Fatal("empty notice should fail")
	}
	if _, _, _, err := NormNotice("x", "", ""); err == nil {
		t.Fatal("missing window should fail")
	}
	if _, _, _, err := NormNotice("x", "2026-13-01", "2026-10-07"); err == nil {
		t.Fatal("bad date should fail")
	}
	if _, _, _, err := NormNotice("x", "2026-10-05", "2026-10-01"); err == nil {
		t.Fatal("from>to should fail")
	}
	n, f, to, err := NormNotice("  欢迎  ", "2026-10-01", "2026-10-07")
	if err != nil || n != "欢迎" || f != "2026-10-01" || to != "2026-10-07" {
		t.Fatalf("norm = %q %q %q %v", n, f, to, err)
	}
	// 生效窗口：起止都有才展示，含首尾；窗外不展示
	tn := &Tenant{Notice: "本周二休诊"}
	if tn.ActiveNotice("2026-10-14") != "" {
		t.Fatal("no window should hide")
	}
	tn.NoticeFrom, tn.NoticeTo = "2026-10-13", "2026-10-15"
	for _, d := range []string{"2026-10-13", "2026-10-14", "2026-10-15"} {
		if tn.ActiveNotice(d) != "本周二休诊" {
			t.Fatalf("in window %s should show", d)
		}
	}
	for _, d := range []string{"2026-10-12", "2026-10-16"} {
		if tn.ActiveNotice(d) != "" {
			t.Fatalf("out window %s should hide", d)
		}
	}
	// 只有起始 / 只有结束：无完整时间窗，不展示
	tn2 := &Tenant{Notice: "x", NoticeFrom: "2026-10-14"}
	if tn2.ActiveNotice("2026-10-13") != "" || tn2.ActiveNotice("2026-10-20") != "" {
		t.Fatal("from-only window should hide")
	}
	tn3 := &Tenant{Notice: "x", NoticeTo: "2026-10-14"}
	if tn3.ActiveNotice("2026-10-15") != "" || tn3.ActiveNotice("2026-10-01") != "" {
		t.Fatal("to-only window should hide")
	}
	// 无内容=无公告
	if (&Tenant{}).ActiveNotice("2026-10-14") != "" {
		t.Fatal("no content should be empty")
	}
}

func TestSlotWindow(t *testing.T) {
	seg := []model.HourSeg{{Open: "09:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}}
	// 未配放号两段：回落营业时段
	tn := &Tenant{HoursSeg: seg}
	if w := tn.SlotWindow(); len(w) != 2 || w[0].Open != "09:00" {
		t.Fatalf("fallback = %+v", w)
	}
	// 配了放号两段：优先它，不跟营业时间走（入库数据总是归一过的）
	tn.SlotSegs = []model.HourSeg{{Open: "08:00", Close: "11:30"}, {Open: "14:00", Close: "20:00"}}
	if w := tn.SlotWindow(); len(w) != 2 || w[0].Open != "08:00" || w[1].Close != "20:00" {
		t.Fatalf("slot segs = %+v", w)
	}
	// 老单段兼容：只有 slot_from/slot_to，没有 slot_segs
	tn2 := &Tenant{SlotFrom: "08:00", SlotTo: "20:00", HoursSeg: seg}
	if w := tn2.SlotWindow(); len(w) != 1 || w[0].Open != "08:00" {
		t.Fatalf("legacy = %+v", w)
	}
	// 校验：半段、格式错、起晚于止、重叠都拒
	for _, bad := range [][]model.HourSeg{
		{{Open: "09:00", Close: "12:00"}, {}},
		{{Open: "9:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}},
		{{Open: "12:00", Close: "09:00"}, {Open: "14:00", Close: "18:00"}},
		{{Open: "09:00", Close: "12:00"}, {Open: "11:00", Close: "18:00"}},
	} {
		if _, err := NormSlotSegs(bad); err == nil {
			t.Fatalf("bad %+v should fail", bad)
		}
	}
	// 两段全空=不合规（放号时间必填，不再回落营业时间）
	if _, err := NormSlotSegs([]model.HourSeg{{}, {}}); err == nil {
		t.Fatalf("empty should fail")
	}
	// 正常两段
	if out, err := NormSlotSegs([]model.HourSeg{{Open: "09:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}}); err != nil || len(out) != 2 {
		t.Fatalf("valid = %+v %v", out, err)
	}
}
