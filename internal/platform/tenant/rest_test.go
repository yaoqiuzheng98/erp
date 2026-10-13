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
