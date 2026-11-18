package handler

import (
	"net/url"
	"testing"

	"erp/internal/dental/appointment"
)

func TestFilterApptsBySource(t *testing.T) {
	list := []appointment.Appointment{
		{Source: appointment.SourceAdmin, PatientName: "后台单"},
		{Source: appointment.SourceWxapp, PatientName: "小程序单"},
		{Source: "", PatientName: "历史单"},
	}
	cases := []struct {
		source string
		want   int
		names  []string
	}{
		{"", 3, nil},
		{appointment.SourceAdmin, 1, []string{"后台单"}},
		{appointment.SourceWxapp, 1, []string{"小程序单"}},
	}
	for _, tc := range cases {
		got := filterApptsBySource(append([]appointment.Appointment(nil), list...), tc.source)
		if len(got) != tc.want {
			t.Fatalf("source=%q len=%d, want %d", tc.source, len(got), tc.want)
		}
		for i, name := range tc.names {
			if got[i].PatientName != name {
				t.Fatalf("source=%q got[%d]=%q, want %q", tc.source, i, got[i].PatientName, name)
			}
		}
	}
}

func TestWeekQueryKeepsFilters(t *testing.T) {
	q, err := url.ParseQuery(string(weekQuery("2026-12-21", "13900000001", appointment.SourceAdmin)))
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("week") != "2026-12-21" || q.Get("phone") != "13900000001" || q.Get("source") != appointment.SourceAdmin {
		t.Fatalf("weekQuery lost filters: %v", q)
	}
	if got := string(weekQuery("2026-12-21", "", "")); got != "week=2026-12-21" {
		t.Fatalf("weekQuery empty filters = %q", got)
	}
}
