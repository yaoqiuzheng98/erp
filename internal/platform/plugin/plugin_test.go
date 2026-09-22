package plugin

import (
	"testing"
)

type stub struct {
	Base
	id   string
	deps []string
	inds []string
}

func (s stub) ID() string             { return s.id }
func (s stub) Name() string           { return s.id }
func (s stub) Version() string        { return "0" }
func (s stub) Dependencies() []string { return s.deps }
func (s stub) Industries() []string   { return s.inds }

func ids(ps []Plugin) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID()
	}
	return out
}

func TestTopoSortedDepsFirst(t *testing.T) {
	Register(stub{id: "zz_dep"})
	Register(stub{id: "aa_main", deps: []string{"zz_dep"}})

	order := ids(TopoSorted())
	main, dep := -1, -1
	for i, id := range order {
		if id == "aa_main" {
			main = i
		}
		if id == "zz_dep" {
			dep = i
		}
	}
	if dep < 0 || main < 0 || dep > main {
		t.Fatalf("dep must precede dependent: %v", order)
	}
}

func TestMenusOfFiltersDisabled(t *testing.T) {
	// stub 插件无菜单，仅验证 enabled 集合语义不 panic
	got := MenusOf(map[string]bool{"aa_main": true})
	if len(got) != 0 {
		t.Fatalf("expected no menus, got %d", len(got))
	}
}

func TestAppliesTo(t *testing.T) {
	universal := stub{id: "universal"}
	retail := stub{id: "retail_only", inds: []string{"retail"}}

	if !AppliesTo(universal, "retail") || !AppliesTo(universal, "") {
		t.Fatal("通用插件应对所有行业适用")
	}
	if !AppliesTo(retail, "retail") {
		t.Fatal("行业插件应对匹配行业适用")
	}
	if AppliesTo(retail, "dental") {
		t.Fatal("理发店插件不应出现在牙科租户")
	}
	if AppliesTo(retail, "") {
		t.Fatal("行业插件不应对通用租户适用")
	}
}
