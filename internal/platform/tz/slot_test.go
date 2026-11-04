package tz

import "testing"

func TestSlotPassed(t *testing.T) {
	if !SlotPassed("2000-01-01", "09:00") {
		t.Fatal("past date should be passed")
	}
	today, _ := DayStart(Today())
	future := today.AddDate(0, 0, 1).Format("2006-01-02")
	if SlotPassed(future, "09:00") {
		t.Fatal("future date should not be passed")
	}
	if SlotPassed("bad-date", "09:00") {
		t.Fatal("invalid date should not be marked passed")
	}
}
