package rbac

import "testing"

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
