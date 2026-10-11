package tenant

import (
	"testing"

	"erp/internal/platform/model"
)

func TestNormBusinessHours(t *testing.T) {
	// 正常两段：排序 + 展示串去前导零
	segs, err := NormBusinessHours([]model.HourSeg{{Open: "14:00", Close: "18:00"}, {Open: "09:00", Close: "12:00"}})
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if len(segs) != 2 || segs[0].Open != "09:00" || segs[1].Open != "14:00" {
		t.Fatalf("sort = %+v", segs)
	}
	if got := BizSegText(segs); got != "9:00-12:00 14:00-18:00" {
		t.Fatalf("text = %q", got)
	}
	// 早晚两段必填：全空/只一段/半段都不行
	for _, bad := range [][]model.HourSeg{
		{},
		{{}, {}},
		{{Open: "08:00", Close: "20:00"}},
		{{Open: "09:00"}, {}},
		{{Open: "09:00", Close: "12:00"}, {}},
	} {
		if _, err := NormBusinessHours(bad); err == nil {
			t.Fatalf("bad %+v should fail", bad)
		}
	}
	// 格式错 / 起晚于止 / 重叠 / 超两段
	for _, bad := range [][]model.HourSeg{
		{{Open: "9:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}},
		{{Open: "12:00", Close: "09:00"}, {Open: "14:00", Close: "18:00"}},
		{{Open: "09:00", Close: "12:00"}, {Open: "11:00", Close: "18:00"}},
		{{Open: "08:00", Close: "10:00"}, {Open: "10:00", Close: "12:00"}, {Open: "14:00", Close: "18:00"}},
	} {
		if _, err := NormBusinessHours(bad); err == nil {
			t.Fatalf("bad %+v should fail", bad)
		}
	}
	// 新建默认两段
	if got := BizSegText(DefaultBizSegs); got != "9:00-12:00 14:00-18:00" {
		t.Fatalf("default = %q", got)
	}
	// 展示 + 老数据回落
	tn := &Tenant{Hours: "老文本"}
	if tn.HoursText() != "老文本" {
		t.Fatalf("fallback = %q", tn.HoursText())
	}
	tn.HoursSeg = segs
	if tn.HoursText() != "9:00-12:00 14:00-18:00" {
		t.Fatalf("derived = %q", tn.HoursText())
	}
}
