package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// 1×1 transparent PNG.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54,
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
	0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestLooksLikeImageURL(t *testing.T) {
	yes := []string{
		"https://cdn.example.com/cat.jpg",
		"https://cdn.example.com/cat.JPEG",
		"https://upload.wikimedia.org/wikipedia/commons/thumb/a/a9/Foo.jpg/400px-Foo.jpg",
		"http://example.com/a.png?w=200",
		"https://example.com/x.gif",
		"https://example.com/x.webp",
	}
	for _, raw := range yes {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !LooksLikeImageURL(u) {
			t.Fatalf("expected image url: %s", raw)
		}
	}
	no := []string{
		"https://example.com/article",
		"https://example.com/photo.html",
		"https://example.com/a.svg",
	}
	for _, raw := range no {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if LooksLikeImageURL(u) {
			t.Fatalf("did not expect image url: %s", raw)
		}
	}
}

func TestSniffImageMIME(t *testing.T) {
	if got := sniffImageMIME("image/jpeg; charset=binary", []byte("nope")); got != "image/jpeg" {
		t.Fatalf("jpeg header: %q", got)
	}
	if got := sniffImageMIME("image/jpg", tinyPNG); got != "image/jpeg" {
		t.Fatalf("jpg alias: %q", got)
	}
	if got := sniffImageMIME("application/octet-stream", tinyPNG); got != "image/png" {
		t.Fatalf("png sniff: %q", got)
	}
	if got := sniffImageMIME("text/html", []byte("<html>hi</html>")); got != "" {
		t.Fatalf("html should not sniff as image: %q", got)
	}
}

func TestFetchPublicImage_png(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	t.Cleanup(server.Close)

	parsed, err := url.Parse(server.URL + "/photo.png")
	if err != nil {
		t.Fatal(err)
	}
	info, err := fetchPublicImage(context.Background(), server.Client(), parsed)
	if err != nil {
		t.Fatal(err)
	}
	if info.MIME != "image/png" {
		t.Fatalf("mime = %q", info.MIME)
	}
	if info.Bytes != len(tinyPNG) {
		t.Fatalf("bytes = %d", info.Bytes)
	}
	if info.Width != 1 || info.Height != 1 {
		t.Fatalf("size = %dx%d", info.Width, info.Height)
	}
	if !strings.Contains(info.URL, "/photo.png") {
		t.Fatalf("url = %q", info.URL)
	}
}

func TestFetchPublicImage_rejectsHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not an image</html>"))
	}))
	t.Cleanup(server.Close)

	parsed, err := url.Parse(server.URL + "/page")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fetchPublicImage(context.Background(), server.Client(), parsed)
	if err == nil {
		t.Fatal("expected error for html")
	}
}

func TestFetchImageHandler_rejectsPrivateURL(t *testing.T) {
	handler := NewFetchImageHandler()
	res, err := handler(context.Background(), `{"url":"http://127.0.0.1/secret.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Error:") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestFetchImageHandler_verifiesRewrittenPublicURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cat.png" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler := newFetchImageHandler(&http.Client{
		Transport: rewriteTransport{base: base},
	})
	res, err := handler(context.Background(), `{"url":"https://1.1.1.1/cat.png","alt":"A cat"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(res.Content, "Error:") {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "![A cat](") {
		t.Fatalf("missing markdown image: %s", res.Content)
	}
	if !strings.Contains(res.Content, "MIME: image/png") {
		t.Fatalf("missing mime: %s", res.Content)
	}
	if res.Host == "" {
		t.Fatal("expected host")
	}
}

func TestFetchURLHandler_routesImageExtension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	images := newFetchImageHandler(&http.Client{Transport: rewriteTransport{base: base}})
	handler := newFetchURLHandler(images)
	res, err := handler(context.Background(), `{"url":"https://1.1.1.1/photo.jpg"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Verified public image") {
		t.Fatalf("expected image result, got %s", res.Content)
	}
}

func TestPeekToolStatus_fetchImage(t *testing.T) {
	phase, host := peekToolStatus("fetch_image", `{"url":"https://1.1.1.1/a.png"}`)
	if phase != "loading_image" {
		t.Fatalf("phase = %s", phase)
	}
	if host != "1.1.1.1" {
		t.Fatalf("host = %q", host)
	}
}

func TestDefaultRegistry_includesFetchImage(t *testing.T) {
	reg := DefaultRegistry("")
	if _, ok := reg.Get("fetch_image"); !ok {
		t.Fatal("fetch_image should always register")
	}
	if _, ok := reg.Get("fetch_url"); !ok {
		t.Fatal("fetch_url should always register")
	}
}

func TestFormatImageToolResult_sanitizesAlt(t *testing.T) {
	got := formatImageToolResult(fetchedImage{
		URL:   "https://example.com/a.png",
		MIME:  "image/png",
		Bytes: 12,
	}, "A [cute]\ncat")
	if !strings.Contains(got, "![A cute cat](https://example.com/a.png)") {
		t.Fatalf("markdown = %s", got)
	}
}

type rewriteTransport struct {
	base   *url.URL
	parent http.RoundTripper
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.base.Scheme
	clone.URL.Host = t.base.Host
	clone.Host = t.base.Host
	parent := t.parent
	if parent == nil {
		parent = http.DefaultTransport
	}
	return parent.RoundTrip(clone)
}
