package version

import "testing"

func TestNormVerIsPositive(t *testing.T) {
	if NormVer < 1 {
		t.Fatalf("NormVer must be >= 1, got %d", NormVer)
	}
}

func TestVersionNotEmpty(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}
