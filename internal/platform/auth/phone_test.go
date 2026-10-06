package auth

import "testing"

func TestValidPhone(t *testing.T) {
	for _, ok := range []string{"13800001111", "15912345678", "19900001111"} {
		if !ValidPhone(ok) {
			t.Fatalf("ValidPhone(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "12345678901", "12800001111", "1380000111", "138000011111", "23800001111", " 13800001111"} {
		if ValidPhone(bad) {
			t.Fatalf("ValidPhone(%q) = true, want false", bad)
		}
	}
}
