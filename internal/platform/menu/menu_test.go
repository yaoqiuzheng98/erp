package menu

import "testing"

func TestFilter(t *testing.T) {
	items := []Item{
		{ID: "g1", Title: "组", Children: []Item{
			{ID: "a", Title: "允许", Path: "/a", Perm: "p.a"},
			{ID: "b", Title: "拒绝", Path: "/b", Perm: "p.b"},
		}},
		{ID: "free", Title: "无权限要求", Path: "/free"},
		{ID: "hidden", Title: "隐藏", Path: "/h", Perm: "p.h"},
	}
	has := func(code string) bool { return code == "p.a" }
	got := Filter(items, has)
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(got), got)
	}
	if got[0].ID != "g1" || len(got[0].Children) != 1 || got[0].Children[0].ID != "a" {
		t.Fatalf("group should keep only permitted child: %+v", got[0])
	}
	if got[1].ID != "free" {
		t.Fatalf("perm-less item should always pass")
	}
}

func TestTitleFor(t *testing.T) {
	items := []Item{
		{ID: "d", Title: "门诊", Children: []Item{
			{ID: "p", Title: "患者", Path: "/app/patients"},
			{ID: "a", Title: "预约", Path: "/app/appointments"},
		}},
		{ID: "sys", Title: "概览", Path: "/sysadmin"},
	}
	cases := map[string]string{
		"/app/patients":        "患者",
		"/app/patients/abc123": "患者",
		"/sysadmin":            "概览",
		"/sysadmin/tenants":    "概览", // 无精确项时回退到最长前缀
		"/login":               "",
		"/app/patientsx":       "",
		"/app/appointments":    "预约",
	}
	for path, want := range cases {
		if got := TitleFor(items, path); got != want {
			t.Fatalf("TitleFor(%q) = %q, want %q", path, got, want)
		}
	}
}
