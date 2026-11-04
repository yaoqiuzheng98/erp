package web

import (
	"bytes"
	"strings"
	"testing"

	"erp/internal/dental/appointment"
	"erp/internal/platform/tenant"

	"github.com/gin-gonic/gin"
)

// renderDay/renderRow 与 handler 里的 week 视图数据同形（模板按字段名取值）。
type renderDay struct {
	Date    string
	Weekday string
	IsToday bool
	Total   int
}

type renderRow struct {
	Slot   string
	Label  string
	Extra  bool
	Cells  [7][]appointment.Appointment
	Covers [7][]appointment.Appointment
	Used   [7]int
}

// TestAppointmentsRendersSpan 周视图能渲染长单：起始格显示时间区间，覆盖格显示占用。
func TestAppointmentsRendersSpan(t *testing.T) {
	tpl, err := Build()
	if err != nil {
		t.Fatalf("build templates: %v", err)
	}
	a := appointment.Appointment{
		Slot: "08:30", EndSlot: "12:00",
		PatientName: "长单患者", Doctor: "郑医生", Status: appointment.Booked,
	}
	days := make([]renderDay, 7)
	for i := range days {
		days[i] = renderDay{Date: "2026-11-10", Weekday: "周一"}
	}
	rows := []renderRow{
		{Slot: "08:30", Label: "08:30-09:00", Cells: [7][]appointment.Appointment{{a}}, Used: [7]int{1}},
		{Slot: "09:00", Label: "09:00-09:30", Covers: [7][]appointment.Appointment{{a}}, Used: [7]int{1}},
	}
	p := &Page{
		Title: "预约", Path: "/app/appointments", CSRF: "csrf",
		Tenant: &tenant.Tenant{Name: "测试门诊"},
		Data: gin.H{
			"Days": days, "Rows": rows, "WeekStart": "2026-11-10", "WeekEnd": "2026-11-16",
			"WeekTotal": 1, "WeekMonday": "2026-11-10",
			"Patients": nil, "Doctors": nil, "DoctorCount": 1,
			"Slots":       []appointment.Slot{{Value: "08:30", Label: "08:30-09:00"}},
			"SlotMinutes": 30, "SlotSegs": nil,
		},
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "dental/appointments", p); err != nil {
		t.Fatalf("render dental/appointments: %v", err)
	}
	html := buf.String()
	for _, want := range []string{"长单患者", "08:30-12:00", "占用：郑医生", "id=\"appt-span\"", "name=\"end_slot\""} {
		if !strings.Contains(html, want) {
			t.Fatalf("appointments missing %q", want)
		}
	}
}
