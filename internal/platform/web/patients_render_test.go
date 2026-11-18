package web

import (
	"bytes"
	"strings"
	"testing"

	"erp/internal/dental/patient"
	"erp/internal/platform/model"
	"erp/internal/platform/tenant"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestPatientsRendersProfileAndEdit 新建表单含性别/年龄；列表有修改入口；
// 编辑弹窗电话只读、姓名/性别/年龄可回填（含 0 岁）。
func TestPatientsRendersProfileAndEdit(t *testing.T) {
	tpl, err := Build()
	if err != nil {
		t.Fatalf("build templates: %v", err)
	}
	age30, age0 := 30, 0
	id1, id2, id3 := bson.NewObjectID(), bson.NewObjectID(), bson.NewObjectID()
	rows := []patient.Patient{
		{Doc: model.Doc{ID: id1}, Name: "张三", Phone: "13900000001", Gender: "女", Age: &age30},
		{Doc: model.Doc{ID: id2}, Name: "李四", Phone: "13900000002"},
		{Doc: model.Doc{ID: id3}, Name: "宝宝", Phone: "13900000003", Age: &age0},
	}
	p := &Page{
		Title: "患者", Path: "/app/patients", CSRF: "csrf",
		Tenant: &tenant.Tenant{Name: "测试门诊"},
		Data: gin.H{
			"Rows": rows, "Pager": Pager{Page: 1, Size: 30, Total: 3}, "Q": "",
		},
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "dental/patients", p); err != nil {
		t.Fatalf("render dental/patients: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`name="gender"`, `name="age"`,
		`data-bs-target="#patEdit` + id1.Hex() + `"`,
		`action="/app/patients/` + id1.Hex() + `/update"`,
		`value="13900000001" disabled`,
		`value="30"`,
		`value="女" selected`,
		`0岁`, `value="0"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("patients page missing %q", want)
		}
	}
}
