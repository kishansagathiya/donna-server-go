package ingest

import "testing"

func TestResolveMime_pdfMagic(t *testing.T) {
	buf := []byte("%PDF-1.4 sample")
	got := ResolveMime(buf, "", "doc.bin")
	if got != "application/pdf" {
		t.Fatalf("got %q, want application/pdf", got)
	}
}

func TestResolveMime_extensionFallback(t *testing.T) {
	got := ResolveMime([]byte("hello"), "", "notes.md")
	if got != "text/markdown" {
		t.Fatalf("got %q, want text/markdown", got)
	}
}

func TestResolveMime_utf8CodeFile(t *testing.T) {
	buf := []byte("package main\n\nfunc main() {}\n")
	got := ResolveMime(buf, "application/octet-stream", "main.go")
	if got != "text/plain" {
		t.Fatalf("got %q, want text/plain", got)
	}
}

func TestAssetKindFromMime(t *testing.T) {
	tests := []struct {
		mime string
		want AssetKind
	}{
		{"image/png", AssetImage},
		{"audio/mpeg", AssetAudio},
		{"text/plain", AssetText},
		{"application/pdf", AssetDocument},
	}
	for _, tt := range tests {
		if got := AssetKindFromMime(tt.mime); got != tt.want {
			t.Fatalf("AssetKindFromMime(%q) = %q, want %q", tt.mime, got, tt.want)
		}
	}
}
