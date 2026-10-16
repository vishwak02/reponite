package processing

import "testing"

func TestContentLang(t *testing.T) {
	for p, want := range map[string]string{
		"rr_nav_bringup/config/rviz/navigation.rviz": "rviz",
		"sootballs_bringup/kiba/errors.json":         "json",
		"web/package-lock.json":                      "",
		"src/a.cpp":                                  "",
	} {
		if got := contentLang(p); got != want {
			t.Errorf("contentLang(%q) = %q, want %q", p, got, want)
		}
	}
	if _, ok := contentFile("big.json", string(make([]byte, maxContentFileBytes+1))); ok {
		t.Error("an oversized JSON file must not be stored")
	}
}
