package storage

import "testing"

func TestResolveSignedStorageURL(t *testing.T) {
	base := "https://example.supabase.co"
	token := "token=abc"

	tests := []struct {
		name   string
		signed string
		want   string
	}{
		{
			name:   "relative object sign path",
			signed: "/object/sign/note-audio/user/file.wav?" + token,
			want:   base + "/storage/v1/object/sign/note-audio/user/file.wav?" + token,
		},
		{
			name:   "already storage v1 relative",
			signed: "/storage/v1/object/sign/note-audio/user/file.wav?" + token,
			want:   base + "/storage/v1/object/sign/note-audio/user/file.wav?" + token,
		},
		{
			name:   "absolute url passthrough",
			signed: "https://cdn.example.com/file.wav?" + token,
			want:   "https://cdn.example.com/file.wav?" + token,
		},
		{
			name:   "base url trailing slash",
			signed: "/object/sign/bucket/key",
			want:   base + "/storage/v1/object/sign/bucket/key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSignedStorageURL(base, tc.signed)
			if got != tc.want {
				t.Fatalf("resolveSignedStorageURL() = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("base with trailing slash", func(t *testing.T) {
		got := resolveSignedStorageURL(base+"/", "/object/sign/bucket/key")
		want := base + "/storage/v1/object/sign/bucket/key"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}
