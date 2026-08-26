package notes

import (
	"encoding/base64"
	"testing"
)

func TestParseNoteImageAttachments_empty(t *testing.T) {
	got, err := parseNoteImageAttachments(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("got %#v, want nil", got)
	}
}

func TestParseNoteImageAttachments_rejectsNonImage(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 fake"))
	_, err := parseNoteImageAttachments([]noteAttachmentInput{{
		Kind:       "file",
		Filename:   "doc.pdf",
		Mime:       "application/pdf",
		DataBase64: payload,
	}})
	if err == nil {
		t.Fatal("expected error for non-image attachment")
	}
}

func TestParseNoteImageAttachments_image(t *testing.T) {
	// Minimal JPEG SOI marker so ResolveMime can treat it as an image.
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	payload := base64.StdEncoding.EncodeToString(jpeg)
	got, err := parseNoteImageAttachments([]noteAttachmentInput{{
		Kind:       "file",
		Filename:   "photo.jpg",
		Mime:       "image/jpeg",
		DataBase64: payload,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Filename != "photo.jpg" {
		t.Fatalf("unexpected attachments: %#v", got)
	}
	if got[0].Mime != "image/jpeg" {
		t.Fatalf("mime = %q", got[0].Mime)
	}
}

func TestParseNoteImageAttachments_tooMany(t *testing.T) {
	inputs := make([]noteAttachmentInput, 11)
	_, err := parseNoteImageAttachments(inputs)
	if err == nil {
		t.Fatal("expected too-many error")
	}
}
