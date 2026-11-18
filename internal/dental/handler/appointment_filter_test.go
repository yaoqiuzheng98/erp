package handler

import (
	"net/url"
	"testing"

	"erp/internal/dental/appointment"
)

func TestFilterAppts(t *testing.T) {
	list := []appointment.Appointment{
		{Source: appointment.SourceAdmin, PatientName: "后台单", Status: appointment.Booked},
		{Source: appointment.SourceAdmin, PatientName: "后台已取消", Status: appointment.Cancel},
		{Source: appointment.SourceWxapp, PatientName: "小程序单", Status: appointment.Booked},
		{Source: "", PatientName: "历史单", Status: appointment.Done},
	}
	cases := []struct {
		name       string
		source     string
		showCancel bool
		want       []string
	}{
		{"默认隐藏已取消", "", false, []string{"后台单", "小程序单", "历史单"}},
		{"显示已取消", "", true, []string{"后台单", "后台已取消", "小程序单", "历史单"}},
		{"后台且隐藏已取消", appointment.SourceAdmin, false, []string{"后台单"}},
		{"后台且显示已取消", appointment.SourceAdmin, true, []string{"后台单", "后台已取消"}},
		{"小程序", appointment.SourceWxapp, false, []string{"小程序单"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterAppts(append([]appointment.Appointment(nil), list...), tc.source, tc.showCancel)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d, want %d", len(got), len(tc.want))
			}
			for i, name := range tc.want {
				if got[i].PatientName != name {
					t.Fatalf("got[%d]=%q, want %q", i, got[i].PatientName, name)
				}
			}
		})
	}
}

func TestWeekQueryKeepsFilters(t *testing.T) {
	q, err := url.ParseQuery(string(weekQuery("2026-12-21", "13900000001", appointment.SourceAdmin, true)))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"week": "2026-12-21", "phone": "13900000001",
		"source": appointment.SourceAdmin, "cancel": "1",
	} {
		if got := q.Get(k); got != want {
			t.Fatalf("weekQuery %s=%q, want %q", k, got, want)
		}
	}
	if got := string(weekQuery("2026-12-21", "", "", false)); got != "week=2026-12-21" {
		t.Fatalf("weekQuery empty filters = %q", got)
	}
}
