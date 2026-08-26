package buildinfo

import "testing"

func TestRelease(t *testing.T) {
	if Version != "1.0.0" {
		t.Fatalf("Version = %q, want 1.0.0", Version)
	}
	if Name != "Personal Assistant" {
		t.Fatalf("Name = %q, want Personal Assistant", Name)
	}
	if Label != "v1 · Personal Assistant" {
		t.Fatalf("Label = %q, want v1 · Personal Assistant", Label)
	}
}
