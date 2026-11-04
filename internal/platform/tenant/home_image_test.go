package tenant

import "testing"

// TestHomeImageField 图位 kind 与 bson 字段一一对应，防止上传接口拼错落不到库。
func TestHomeImageField(t *testing.T) {
	want := map[string]string{
		"hero":    "hero_image",
		"clinic":  "clinic_image",
		"parking": "parking_image",
	}
	for kind, field := range want {
		got, ok := homeImageField(kind)
		if !ok || got != field {
			t.Fatalf("homeImageField(%q) = %q, %v; want %q, true", kind, got, ok, field)
		}
	}
	if _, ok := homeImageField("unknown"); ok {
		t.Fatal("unknown image kind should be rejected")
	}
}
