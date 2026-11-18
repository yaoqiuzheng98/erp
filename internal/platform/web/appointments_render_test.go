package web

import (
	"bytes"
	"strings"
	"testing"

	"erp/internal/dental/appointment"
	"erp/internal/dental/patient"
	"erp/internal/platform/model"
	"erp/internal/platform/tenant"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
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
		Doc:       model.Doc{ID: bson.NewObjectID()},
		PatientID: bson.NewObjectID(),
		Slot:      "08:30", EndSlot: "12:00",
		PatientName: "长单患者", Doctor: "郑医生", Status: appointment.Booked,
		Source: appointment.SourceAdmin,
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
			"WeekTotal": 1, "WeekMonday": "2026-11-10", "Source": "", "Status": "",
			"Patients": []patient.Patient{{
				Doc:   model.Doc{ID: bson.NewObjectID()},
				Name:  "张三",
				Phone: "13900000001",
			}}, "Doctors": nil, "DoctorCount": 1,
			"Slots":       []appointment.Slot{{Value: "08:30", Label: "08:30-09:00"}},
			"SlotMinutes": 30, "SlotSegs": nil,
			"ApptColors":  map[string]string{a.ID.Hex(): "doc-bg-1"},
			"PatProfiles": map[string]string{a.PatientID.Hex(): "男 40岁"},
			"PatPhones":   map[string]string{a.PatientID.Hex(): "15889862321"},
		},
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "dental/appointments", p); err != nil {
		t.Fatalf("render dental/appointments: %v", err)
	}
	html := buf.String()
	for _, want := range []string{"长单患者", "08:30-12:00", "占用：郑医生", "id=\"appt-span\"", "name=\"end_slot\"", "占用到（含该档）", "先选到诊时段", "id=\"patient-search\"", "id=\"patient-select\"", "id=\"patient-results\"", `data-name="张三"`, `data-phone="13900000001"`, "电话：15889862321", `name="source"`, "全部来源", `name="status"`, "全部（不含已取消）"} {
		if !strings.Contains(html, want) {
			t.Fatalf("appointments missing %q", want)
		}
	}
	if strings.Contains(html, ">1 档<") {
		t.Fatal("appointments should not render static duration options")
	}
	// 同医生底色：起始卡片和覆盖格都带同一个色 class。
	for _, want := range []string{
		`class="border rounded p-1 mb-1 appt-card doc-bg-1"`,
		`class="border rounded p-1 mb-1 appt-cover text-muted small doc-bg-1"`,
		`class="week-canvas"`,
		`class="week-links"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("appointments missing doctor color/connector %q", want)
		}
	}
	if n := strings.Count(html, `data-appt-id="`+a.ID.Hex()+`"`); n != 2 {
		t.Fatalf("data-appt-id count = %d, want 2 (start + cover)", n)
	}
}
