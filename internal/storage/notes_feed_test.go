package storage

import "testing"

func TestEncodeDecodeFeedCursor(t *testing.T) {
	cur := encodeFeedCursor("2026-07-20T10:00:00Z", "note-1")
	got, err := decodeFeedCursor(cur)
	if err != nil {
		t.Fatal(err)
	}
	if got.NoteDate != "2026-07-20T10:00:00Z" || got.ID != "note-1" {
		t.Fatalf("got %#v", got)
	}
}

func TestDecodeFeedCursor_invalid(t *testing.T) {
	if _, err := decodeFeedCursor("%%%"); err == nil {
		t.Fatal("expected invalid_cursor error")
	}
}
