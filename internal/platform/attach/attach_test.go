package attach

import "testing"

func TestSafeFilename(t *testing.T) {
	cases := map[string]string{
		"0.png":            "0.png",
		"卡通头像1.png":          "卡通头像1.png",
		`a"b.png`:          "ab.png",
		"a\\b.png":         "ab.png",
		"a\rb\nc.png":      "abc.png",
		`"""`:              "image",
		"":                 "image",
		"../etc/passwd":    "passwd",
	}
	for in, want := range cases {
		if got := SafeFilename(in); got != want {
			t.Fatalf("SafeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
