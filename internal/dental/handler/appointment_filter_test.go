package handler

import (
	"net/url"
	"testing"

	"erp/internal/dental/appointment"
)

func TestFilterAppts(t *testing.T) {
	list := []appointment.Appointment{
		{Source: appointment.SourceAdmin, PatientName: "后台已预约", Status: appointment.Booked},
		{Source: appointment.SourceAdmin, PatientName: "后台已完成", Status: appointment.Done},
		{Source: appointment.SourceAdmin, PatientName: "后台已取消", Status: appointment.Cancel},
		{Source: appointment.SourceWxapp, PatientName: "小程序爽约", Status: appointment.NoShow},
		{Source: appointment.SourceWxapp, PatientName: "小程序已取消", Status: appointment.Cancel},
	}
	cases := []struct {
		name   string
		source string
		status string
		want   []string
	}{
		{"默认不含已取消", "", "", []string{"后台已预约", "后台已完成", "小程序爽约"}},
		{"全部状态", "", "all", []string{"后台已预约", "后台已完成", "后台已取消", "小程序爽约", "小程序已取消"}},
		{"只看已预约", "", appointment.Booked, []string{"后台已预约"}},
		{"只看已完成", "", appointment.Done, []string{"后台已完成"}},
		{"只看爽约", "", appointment.NoShow, []string{"小程序爽约"}},
		{"只看已取消", "", appointment.Cancel, []string{"后台已取消", "小程序已取消"}},
		{"后台且只看已取消", appointment.SourceAdmin, appointment.Cancel, []string{"后台已取消"}},
		{"小程序且默认", appointment.SourceWxapp, "", []string{"小程序爽约"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterAppts(append([]appointment.Appointment(nil), list...), tc.source, tc.status)
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
	q, err := url.ParseQuery(string(weekQuery("2026-12-21", "13900000001", appointment.SourceAdmin, appointment.Cancel)))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"week": "2026-12-21", "phone": "13900000001",
		"source": appointment.SourceAdmin, "status": appointment.Cancel,
	} {
		if got := q.Get(k); got != want {
			t.Fatalf("weekQuery %s=%q, want %q", k, got, want)
		}
	}
	if got := string(weekQuery("2026-12-21", "", "", "")); got != "week=2026-12-21" {
		t.Fatalf("weekQuery empty filters = %q", got)
	}
}
