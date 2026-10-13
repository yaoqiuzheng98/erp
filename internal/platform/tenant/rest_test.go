package tenant

import "testing"

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
