package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	browseHTTPTimeout  = 45 * time.Second
	defaultBrowseWait  = 1500
	defaultBrowseChars = 16_000
	urlBrowseExtractor = "url_browse"
)

var browserBaseURL string

type browseRequest struct {
	URL      string `json:"url"`
	WaitMs   int    `json:"wait_ms,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

type browseResponse struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Text   string `json:"text"`
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

// SetBrowserBaseURL configures the donna-browser Playwright sidecar used when
// a static fetch returns a JS shell. Empty disables browsing.
func SetBrowserBaseURL(base string) {
	browserBaseURL = strings.TrimRight(strings.TrimSpace(base), "/")
}

func browseEnabled() bool {
	return browserBaseURL != ""
}

func browsePage(rawURL string) (ExtractedAsset, error) {
	if !browseEnabled() {
		return ExtractedAsset{}, fmt.Errorf("browser not configured")
	}
	payload, err := json.Marshal(browseRequest{
		URL:      rawURL,
		WaitMs:   defaultBrowseWait,
		MaxChars: defaultBrowseChars,
	})
	if err != nil {
		return ExtractedAsset{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), browseHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, browserBaseURL+"/browse", bytes.NewReader(payload))
	if err != nil {
		return ExtractedAsset{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := httpClient.Do(req)
	if err != nil {
		return ExtractedAsset{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 2_000_000))

	var out browseResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return ExtractedAsset{}, fmt.Errorf("invalid browser response")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || out.Error != "" {
		msg := strings.TrimSpace(out.Error)
		if msg == "" {
			msg = fmt.Sprintf("browser returned status %d", res.StatusCode)
		}
		return ExtractedAsset{}, fmt.Errorf("%s", msg)
	}

	finalURL := strings.TrimSpace(out.URL)
	if finalURL == "" {
		finalURL = rawURL
	}
	if IsTweetURL(finalURL) {
		return ExtractTweet(finalURL)
	}
	title := strings.TrimSpace(out.Title)
	if title == "" {
		if parsed, err := url.Parse(finalURL); err == nil && parsed.Host != "" {
			title = parsed.Host
		}
	}
	if title == "" {
		title = finalURL
	}
	text := strings.TrimSpace(out.Text)
	if text == "" {
		return ExtractedAsset{}, fmt.Errorf("no text content extracted from URL")
	}

	return ExtractedAsset{
		Content:   ClampText(fmt.Sprintf("# %s\nURL: %s\n\n%s", title, finalURL, text)),
		AssetKind: AssetLink,
		MimeType:  "text/html",
		Extractor: urlBrowseExtractor,
		Title:     title,
		SourceURL: finalURL,
	}, nil
}

func fetchLooksIncomplete(text string, rawBytes int, isHTML bool) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return true
	}
	if len(t) < 280 {
		return true
	}
	lower := strings.ToLower(t)
	for _, marker := range []string{
		"enable javascript",
		"enable js",
		"you need to enable javascript",
		"noscript",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if isHTML && rawBytes > 5000 && len(t) < 400 {
		return true
	}
	return false
}
