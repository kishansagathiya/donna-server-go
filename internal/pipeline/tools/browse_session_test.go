package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserClientSessionNavigateAndClick(t *testing.T) {
	var lastPath string
	var lastBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &lastBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"url":   "https://example.com/form",
			"title": "Form",
			"text":  "Name field",
			"elements": []map[string]any{
				{"ref": "e1", "tag": "input", "type": "text", "name": "Name"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	client := NewBrowserClient(srv.URL)
	snap, err := client.Navigate(context.Background(), "run-12345678", "https://1.1.1.1/", 0)
	if err != nil {
		t.Fatal(err)
	}
	if lastPath != "/session/navigate" {
		t.Fatalf("path=%s", lastPath)
	}
	if lastBody["session_id"] != "run-12345678" {
		t.Fatalf("body=%#v", lastBody)
	}
	if snap.URL != "https://example.com/form" || len(snap.Elements) != 1 {
		t.Fatalf("snap=%#v", snap)
	}

	_, err = client.Click(context.Background(), "run-12345678", "e1")
	if err != nil {
		t.Fatal(err)
	}
	if lastPath != "/session/click" {
		t.Fatalf("path=%s", lastPath)
	}
	if lastBody["ref"] != "e1" {
		t.Fatalf("body=%#v", lastBody)
	}

	out := FormatBrowserSnapshot(snap)
	if !strings.Contains(out, "e1") || !strings.Contains(out, "Name") {
		t.Fatalf("format=%s", out)
	}
}

func TestBrowserClientNavigateRejectsLocalhost(t *testing.T) {
	client := NewBrowserClient("http://browser.example")
	_, err := client.Navigate(context.Background(), "run-12345678", "http://localhost:3000", 0)
	if err == nil || !strings.Contains(err.Error(), "localhost") {
		t.Fatalf("err=%v", err)
	}
}

func TestBrowserClientCloseSession(t *testing.T) {
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	client := NewBrowserClient(srv.URL)
	if err := client.CloseSession(context.Background(), "run-12345678"); err != nil {
		t.Fatal(err)
	}
	if lastPath != "/session/close" {
		t.Fatalf("path=%s", lastPath)
	}
}
