package appointment

import "testing"

func TestSourceName(t *testing.T) {
	cases := map[string]string{
		SourceAdmin: "后台",
		SourceWxapp: "小程序",
		"":          "",
		"other":     "",
	}
	for src, want := range cases {
		if got := (Appointment{Source: src}).SourceName(); got != want {
			t.Errorf("SourceName(%q) = %q, want %q", src, got, want)
		}
	}
}
