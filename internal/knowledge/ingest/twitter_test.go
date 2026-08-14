package ingest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseTweetID(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"https://x.com/karpathy/status/2039805659525644595", "2039805659525644595", true},
		{"https://twitter.com/karpathy/status/2039805659525644595?s=20", "2039805659525644595", true},
		{"https://mobile.twitter.com/foo/status/1", "1", true},
		{"https://x.com/i/web/status/99", "99", true},
		{"https://vxtwitter.com/user/status/42", "42", true},
		{"https://example.com/status/1", "", false},
		{"https://x.com/karpathy", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseTweetID(tt.raw)
		if ok != tt.ok || got != tt.want {
			t.Fatalf("ParseTweetID(%q) = %q %v, want %q %v", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

func TestFindTweetURLsDedupes(t *testing.T) {
	text := "see https://x.com/a/status/1 and https://twitter.com/a/status/1 plus https://x.com/b/status/2."
	got := FindTweetURLs(text)
	if len(got) != 2 {
		t.Fatalf("FindTweetURLs = %#v", got)
	}
	if !strings.Contains(got[0], "/status/1") || !strings.Contains(got[1], "/status/2") {
		t.Fatalf("FindTweetURLs = %#v", got)
	}
}

func TestExtractTweetFx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status/123" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(fxTweetResponse{
			Code: 200,
			Tweet: fxTweet{
				ID:               "123",
				URL:              "https://x.com/karpathy/status/123",
				Text:             "compiled wiki > RAG",
				CreatedTimestamp: 1700000000,
				Author:           fxAuthor{Name: "Andrej Karpathy", ScreenName: "karpathy"},
				Quote: &fxTweet{
					Text:   "chunking is a smell",
					Author: fxAuthor{ScreenName: "other"},
				},
			},
		})
	}))
	defer srv.Close()
	restore := SetTweetEndpointsForTest(srv.URL, "", srv.Client())
	defer restore()

	got, err := ExtractTweet("https://x.com/karpathy/status/123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Extractor != twitterExtractor {
		t.Fatalf("extractor = %q", got.Extractor)
	}
	if got.AssetKind != AssetLink {
		t.Fatalf("kind = %q", got.AssetKind)
	}
	if !strings.Contains(got.Content, "compiled wiki > RAG") {
		t.Fatalf("missing tweet text: %s", got.Content)
	}
	if !strings.Contains(got.Content, "@karpathy") {
		t.Fatalf("missing author: %s", got.Content)
	}
	if !strings.Contains(got.Content, "Quoted @other") {
		t.Fatalf("missing quote: %s", got.Content)
	}
	if !strings.Contains(got.Content, "User saved a tweet to memory") {
		t.Fatalf("missing capture marker: %s", got.Content)
	}
	if got.SourceURL != "https://x.com/karpathy/status/123" {
		t.Fatalf("SourceURL = %q", got.SourceURL)
	}
}

func TestExtractTweetFallsBackToOEmbed(t *testing.T) {
	fx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTeapot)
	}))
	defer fx.Close()
	oembed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(twitterOEmbedResponse{
			HTML:       `<blockquote><p>hello from oembed</p></blockquote>`,
			AuthorName: "Ada",
			URL:        "https://x.com/ada/status/9",
		})
	}))
	defer oembed.Close()

	restore := SetTweetEndpointsForTest(fx.URL, oembed.URL, http.DefaultClient)
	defer restore()

	got, err := ExtractTweet("https://x.com/ada/status/9")
	if err != nil {
		t.Fatal(err)
	}
	if got.Extractor != twitterOEmbedExtractor {
		t.Fatalf("extractor = %q", got.Extractor)
	}
	if !strings.Contains(got.Content, "hello from oembed") {
		t.Fatalf("content = %s", got.Content)
	}
}

func TestExtractURLUsesTweetExtractor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(fxTweetResponse{
			Code: 200,
			Tweet: fxTweet{
				ID:     "55",
				URL:    "https://x.com/u/status/55",
				Text:   "from extract url",
				Author: fxAuthor{ScreenName: "u"},
			},
		})
	}))
	defer srv.Close()
	restore := SetTweetEndpointsForTest(srv.URL, "", srv.Client())
	defer restore()

	got, err := ExtractURL("https://twitter.com/u/status/55")
	if err != nil {
		t.Fatal(err)
	}
	if got.Extractor != twitterExtractor {
		t.Fatalf("extractor = %q", got.Extractor)
	}
	if !strings.Contains(got.Content, "from extract url") {
		t.Fatalf("content = %s", got.Content)
	}
}

func TestExpandTweetLinksReplacesBareURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(fxTweetResponse{
			Code: 200,
			Tweet: fxTweet{
				ID:     "7",
				URL:    "https://x.com/u/status/7",
				Text:   "expanded body",
				Author: fxAuthor{ScreenName: "u"},
			},
		})
	}))
	defer srv.Close()
	restore := SetTweetEndpointsForTest(srv.URL, "", srv.Client())
	defer restore()

	got := ExpandTweetLinks("https://x.com/u/status/7")
	if !strings.Contains(got, "expanded body") {
		t.Fatalf("got %s", got)
	}
	if got == "https://x.com/u/status/7" {
		t.Fatal("did not expand bare tweet URL")
	}

	again := ExpandTweetLinks(got)
	if again != got {
		t.Fatalf("second expand changed content:\n%s\nvs\n%s", again, got)
	}
}

func TestExpandTweetLinksKeepsCommentary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(fxTweetResponse{
			Code: 200,
			Tweet: fxTweet{
				ID:     "8",
				URL:    "https://x.com/u/status/8",
				Text:   "the take",
				Author: fxAuthor{ScreenName: "u"},
			},
		})
	}))
	defer srv.Close()
	restore := SetTweetEndpointsForTest(srv.URL, "", srv.Client())
	defer restore()

	got := ExpandTweetLinks("worth rereading\nhttps://x.com/u/status/8")
	if !strings.Contains(got, "worth rereading") || !strings.Contains(got, "the take") {
		t.Fatalf("got %s", got)
	}
}
