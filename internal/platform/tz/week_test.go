package tz

import "testing"

func TestWeekOf(t *testing.T) {
	// 2026-10-09 是周五：所在周一为 10-05，周日为 10-11。
	got := WeekOf("2026-10-09")
	want := [7]string{
		"2026-10-05", "2026-10-06", "2026-10-07", "2026-10-08",
		"2026-10-09", "2026-10-10", "2026-10-11",
	}
	if got != want {
		t.Fatalf("WeekOf(2026-10-09) = %v, want %v", got, want)
	}
	// 周一锚点与周日锚点落到同一周。
	if WeekOf("2026-10-05") != want || WeekOf("2026-10-11") != want {
		t.Fatal("Monday/Sunday anchor should give the same week")
	}
	// 跨月：2026-10-01（周四）所在周一为 09-28。
	if w := WeekOf("2026-10-01"); w[0] != "2026-09-28" || w[6] != "2026-10-04" {
		t.Fatalf("cross-month week = %v", w)
	}
	// 跨年：2026-01-01（周四）所在周一为 2025-12-29。
	if w := WeekOf("2026-01-01"); w[0] != "2025-12-29" || w[6] != "2026-01-04" {
		t.Fatalf("cross-year week = %v", w)
	}
	// 非法/空锚点回落本周：必含今天且周一~周日连续。
	for _, bad := range []string{"", "not-a-date", "2026-13-01"} {
		w := WeekOf(bad)
		found := false
		for _, d := range w {
			if d == Today() {
				found = true
			}
		}
		if !found {
			t.Fatalf("WeekOf(%q) = %v, missing today %s", bad, w, Today())
		}
	}
}
