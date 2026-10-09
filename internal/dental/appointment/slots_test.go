package appointment

import "testing"

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
