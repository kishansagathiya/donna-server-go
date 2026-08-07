package config

import "testing"

func TestParseBoolDefault(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		def  bool
		want bool
	}{
		{"", true, true},
		{"", false, false},
		{"true", false, true},
		{"false", true, false},
		{"0", true, false},
		{"1", false, true},
		{"off", true, false},
		{"on", false, true},
		{"  TRUE  ", false, true},
		{"no", true, false},
	}
	for _, tt := range cases {
		if got := parseBoolDefault(tt.raw, tt.def); got != tt.want {
			t.Fatalf("parseBoolDefault(%q, %v) = %v, want %v", tt.raw, tt.def, got, tt.want)
		}
	}
}

func TestParseBoolEmptyIsFalse(t *testing.T) {
	t.Parallel()
	if parseBool("") {
		t.Fatal("parseBool empty should be false")
	}
	if !parseBool("true") {
		t.Fatal("parseBool true")
	}
}
