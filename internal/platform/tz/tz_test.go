package tz

import (
	"testing"
	"time"
)

func TestShanghaiOffset(t *testing.T) {
	_, offset := Now().Zone()
	if offset != 8*3600 {
		t.Fatalf("offset = %d, want 28800", offset)
	}
}

func TestFormats(t *testing.T) {
	if _, err := time.Parse("2006-01-02", Today()); err != nil {
		t.Fatalf("bad Today(): %v", err)
	}
	if _, err := time.Parse("200601", MonthKey()); err != nil {
		t.Fatalf("bad MonthKey(): %v", err)
	}
	// 与 UTC 直接取日期做对比：东八区零点后两者应可能差一天，
	// 这里只断言北京时间 = UTC+8h 的日期，逻辑恒成立。
	want := time.Now().UTC().Add(8 * time.Hour).Format("2006-01-02")
	if Today() != want {
		t.Fatalf("Today() = %s, want %s", Today(), want)
	}
}
