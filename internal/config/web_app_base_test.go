package config

import "testing"

func TestResolveWebAppBase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults to donnadoesit", in: "", want: "https://donnadoesit.com"},
		{name: "whitespace defaults", in: "  ", want: "https://donnadoesit.com"},
		{
			name: "legacy railway host rewritten",
			in:   "https://donna-web-production-3d4a.up.railway.app",
			want: "https://donnadoesit.com",
		},
		{
			name: "legacy railway host with trailing slash",
			in:   "https://donna-web-production-3d4a.up.railway.app/",
			want: "https://donnadoesit.com",
		},
		{
			name: "other donna-web railway host rewritten",
			in:   "https://donna-web-staging.up.railway.app",
			want: "https://donnadoesit.com",
		},
		{
			name: "explicit custom host preserved",
			in:   "https://preview.example.com",
			want: "https://preview.example.com",
		},
		{
			name: "explicit donnadoesit preserved",
			in:   "https://donnadoesit.com",
			want: "https://donnadoesit.com",
		},
		{
			name: "www donnadoesit preserved",
			in:   "https://www.donnadoesit.com",
			want: "https://www.donnadoesit.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveWebAppBase(tc.in); got != tc.want {
				t.Fatalf("resolveWebAppBase(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
