package ingest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFindHTTPURLs(t *testing.T) {
	text := "see https://example.com/a and https://example.com/a plus https://news.ycombinator.com/item?id=1."
	got := FindHTTPURLs(text)
	if len(got) != 2 {
		t.Fatalf("FindHTTPURLs = %#v", got)
	}
	if !strings.Contains(got[0], "example.com/a") || !strings.Contains(got[1], "news.ycombinator.com") {
		t.Fatalf("FindHTTPURLs = %#v", got)
	}
}

func TestFindHTTPURLsIgnoresExpandedBody(t *testing.T) {
	text := "keep https://example.com/orig\n\nUser saved a link to memory.\n\nURL: https://example.com/orig\n\nAlso mentions https://other.example/inside"
	got := FindHTTPURLs(text)
	if len(got) != 1 || got[0] != "https://example.com/orig" {
		t.Fatalf("FindHTTPURLs = %#v", got)
	}
}

func TestExpandLinksAppendsFetchedPages(t *testing.T) {
	fetch := func(raw string) (ExtractedAsset, error) {
		return ExtractedAsset{
			Content:   "# example.com\nURL: " + raw + "\n\nHello article body",
			AssetKind: AssetLink,
			Extractor: "url_fetch",
			Title:     "example.com",
			SourceURL: raw,
		}, nil
	}
	got := expandLinks("read this https://example.com/post", fetch)
	if !strings.Contains(got, "read this") {
		t.Fatalf("lost commentary: %s", got)
	}
	if !strings.Contains(got, "Hello article body") {
		t.Fatalf("missing page text: %s", got)
	}
	if !strings.Contains(got, "User saved a link to memory") {
		t.Fatalf("missing capture marker: %s", got)
	}

	again := expandLinks(got, fetch)
	if again != got {
		t.Fatalf("second expand changed content:\n%s\nvs\n%s", again, got)
	}
}

func TestExpandLinksBareURLReplaces(t *testing.T) {
	fetch := func(raw string) (ExtractedAsset, error) {
		return ExtractedAsset{
			Content:   "# blog\nURL: " + raw + "\n\npage text",
			Extractor: "url_fetch",
			SourceURL: raw,
		}, nil
	}
	got := expandLinks("https://example.com/x", fetch)
	if strings.TrimSpace(got) == "https://example.com/x" {
		t.Fatal("did not replace bare URL")
	}
	if !strings.Contains(got, "page text") {
		t.Fatalf("got %s", got)
	}
}

func TestRejectPrivateURL(t *testing.T) {
	if err := rejectPrivateURL("http://localhost/secret"); err == nil {
		t.Fatal("expected localhost block")
	}
	if err := rejectPrivateURL("https://127.0.0.1/"); err == nil {
		t.Fatal("expected loopback block")
	}
}

func TestBrowsePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/browse" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(browseResponse{
			URL:    "https://example.com/js",
			Title:  "JS App",
			Text:   "rendered article text from the browser",
			Status: 200,
		})
	}))
	defer srv.Close()
	prev := browserBaseURL
	SetBrowserBaseURL(srv.URL)
	defer SetBrowserBaseURL(prev)

	got, err := browsePage("https://example.com/js")
	if err != nil {
		t.Fatal(err)
	}
	if got.Extractor != urlBrowseExtractor {
		t.Fatalf("extractor = %q", got.Extractor)
	}
	if !strings.Contains(got.Content, "rendered article text") {
		t.Fatalf("content = %s", got.Content)
	}
}

func TestFetchLooksIncomplete(t *testing.T) {
	if !fetchLooksIncomplete("", 10, true) {
		t.Fatal("empty should be incomplete")
	}
	if !fetchLooksIncomplete("Please enable JavaScript to continue", 8000, true) {
		t.Fatal("js shell should be incomplete")
	}
	if fetchLooksIncomplete(strings.Repeat("article word ", 80), 2000, true) {
		t.Fatal("long article should be complete")
	}
}
