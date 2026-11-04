package rbac

import "testing"

func TestDefaultRolesReadonly(t *testing.T) {
	for _, r := range defaultRoles() {
		if len(r.PermCodes) != 2 || r.PermCodes[0] != "patient.read" || r.PermCodes[1] != "appt.read" {
			t.Fatalf("%s default perms = %v, want patient.read + appt.read", r.Name, r.PermCodes)
		}
	}
}

func TestFilterKnownPerms(t *testing.T) {
	got := filterKnownPerms([]string{
		"patient.read", "catalog.read", "patient.write", "billing.read",
		"appt.read", "patient.read", "billing.write", "admin.users",
	})
	want := []string{"patient.read", "patient.write", "appt.read", "admin.users"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestHas(t *testing.T) {
	if !Has(map[string]bool{"*": true}, "any.code") {
		t.Fatal("wildcard should grant all")
	}
	if !Has(map[string]bool{"a.b": true}, "a.b") {
		t.Fatal("exact code should pass")
	}
	if Has(map[string]bool{"a.b": true}, "a.c") {
		t.Fatal("different code must fail")
	}
	if Has(nil, "a.b") {
		t.Fatal("nil set must fail")
	}
}
