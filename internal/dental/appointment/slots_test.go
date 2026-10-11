package appointment

import (
	"testing"

	"erp/internal/platform/model"
)

func TestDaySlots(t *testing.T) {
	s30 := DaySlots(30)
	if len(s30) != 25 { // 8:00~20:00 起约，每小时 2 档
		t.Fatalf("30min slots = %d, want 25", len(s30))
	}
	if s30[0].Value != "08:00" || s30[0].Label != "08:00-08:30" {
		t.Fatalf("first = %+v", s30[0])
	}
	if s30[1].Value != "08:30" || s30[1].Label != "08:30-09:00" {
		t.Fatalf("second = %+v", s30[1])
	}
	last := s30[len(s30)-1]
	if last.Value != "20:00" || last.Label != "20:00-20:30" {
		t.Fatalf("last = %+v", last)
	}
	if len(DaySlots(60)) != 13 {
		t.Fatalf("60min slots = %d, want 13", len(DaySlots(60)))
	}
	if len(DaySlots(15)) != 49 {
		t.Fatalf("15min slots = %d, want 49", len(DaySlots(15)))
	}
	// 非法粒度回落 30
	if len(DaySlots(0)) != 25 || len(DaySlots(45)) != 25 {
		t.Fatal("bad minutes should fall back to 30")
	}
}

func TestNormSlotConfig(t *testing.T) {
	if m, c := NormSlotConfig(0, 0); m != 30 || c != 1 {
		t.Fatalf("zero = %d,%d, want 30,1", m, c)
	}
	if m, c := NormSlotConfig(45, 99); m != 30 || c != 1 {
		t.Fatalf("bad = %d,%d, want 30,1", m, c)
	}
	if m, c := NormSlotConfig(60, 3); m != 60 || c != 3 {
		t.Fatalf("valid = %d,%d, want 60,3", m, c)
	}
}

func TestDaySlotsIn(t *testing.T) {
	biz := []model.HourSeg{{Open: "09:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}}
	s := DaySlotsIn(30, biz)
	if len(s) != 14 { // 上午 6 档 + 下午 8 档
		t.Fatalf("30min segs len = %d, want 14", len(s))
	}
	if s[0].Value != "09:00" || s[5].Value != "11:30" {
		t.Fatalf("am = %s..%s, want 09:00..11:30", s[0].Value, s[5].Value)
	}
	if s[6].Value != "14:00" || s[13].Value != "17:30" || s[13].Label != "17:30-18:00" {
		t.Fatalf("pm = %s..%s, want 14:00..17:30", s[6].Value, s[13].Value)
	}
	for _, sl := range s {
		if sl.Value >= "12:00" && sl.Value < "14:00" {
			t.Fatalf("lunch slot leaked: %s", sl.Value)
		}
	}
	// 60 分钟档：9/10/11 + 14/15/16/17
	if s60 := DaySlotsIn(60, biz); len(s60) != 7 {
		t.Fatalf("60min segs len = %d, want 7", len(s60))
	}
	// 空段回落全天
	if s0 := DaySlotsIn(30, nil); len(s0) != 25 {
		t.Fatalf("nil segs len = %d, want 25", len(s0))
	}
	// 脏时段跳过不炸
	if sx := DaySlotsIn(30, []model.HourSeg{{Open: "xx", Close: "yy"}}); len(sx) != 0 {
		t.Fatalf("bad segs len = %d, want 0", len(sx))
	}
	// 段内判定：11:30（整档止于 12:00）收，13:30（跨午休）不收
	if !slotInBiz("11:30", 30, biz) || slotInBiz("13:30", 30, biz) || slotInBiz("12:00", 30, biz) {
		t.Fatal("slotInBiz boundary wrong")
	}
	if !slotInBiz("10:00", 30, nil) {
		t.Fatal("nil segs should allow all")
	}
}
