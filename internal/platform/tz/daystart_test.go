package tz

import "testing"

func TestDayStart(t *testing.T) {
	got, ok := DayStart("2026-10-06")
	if !ok {
		t.Fatal("DayStart valid date returned false")
	}
	if y, m, d := got.Date(); y != 2026 || m != 10 || d != 6 {
		t.Fatalf("date = %v", got)
	}
	if _, offset := got.Zone(); offset != 8*3600 {
		t.Fatalf("offset = %d, want 28800", offset)
	}
	for _, bad := range []string{"", "2026-13-01", "2026-10-32", "not-a-date", "2026/10/06"} {
		if _, ok := DayStart(bad); ok {
			t.Fatalf("DayStart(%q) = true, want false", bad)
		}
	}
}
