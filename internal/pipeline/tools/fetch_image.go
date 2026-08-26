package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
)

const (
	maxImageBytes     = 8 << 20 // 8 MiB
	imageFetchTimeout = 15 * time.Second
)

var allowedImageMIME = map[string]bool{
	"image/jpeg": true,
	"image/jpg":  true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

func FetchImageDefinition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Type: "function",
		Function: providers.ToolFunctionSchema{
			Name:        "fetch_image",
			Description: "Fetch a public HTTP(S) image and confirm it can be shown to the user. Use when you have a direct image URL (jpeg/png/gif/webp) and should display it. After it succeeds, include the returned markdown image in your reply on its own line. Do not invent image URLs. You cannot generate images.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "Fully-qualified http or https URL of the image",
					},
					"alt": map[string]any{
						"type":        "string",
						"description": "Short accessible description used as markdown alt text",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

func NewFetchImageHandler() Handler {
	return newFetchImageHandler(newImageHTTPClient())
}

func newImageHTTPClient() *http.Client {
	return &http.Client{
		Timeout: imageFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if _, err := ValidatePublicURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			return nil
		},
	}
}

func newFetchImageHandler(client *http.Client) Handler {
	if client == nil {
		client = newImageHTTPClient()
	}
	return func(ctx context.Context, argsJSON string) (Result, error) {
		var args struct {
			URL string `json:"url"`
			Alt string `json:"alt"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		parsed, err := ValidatePublicURL(args.URL)
		if err != nil {
			return Result{Content: "Error: " + err.Error()}, nil
		}

		info, fetchErr := fetchPublicImage(ctx, client, parsed)
		host := parsed.Host
		if info.URL != "" {
			if u, err := url.Parse(info.URL); err == nil && u.Host != "" {
				host = u.Host
			}
		}
		if fetchErr != nil {
			return Result{
				Content: fmt.Sprintf("Error fetching image %s: %s", parsed.String(), fetchErr.Error()),
				Phase:   string(protocol.TurnPhaseLoadingImage),
				Host:    host,
			}, nil
		}

		return Result{
			Content: formatImageToolResult(info, args.Alt),
			Citations: []Citation{{
				URL:     info.URL,
				Title:   firstNonEmpty(sanitizeAlt(args.Alt), host),
				Content: "Image (" + info.MIME + ")",
			}},
			Phase: string(protocol.TurnPhaseLoadingImage),
			Host:  host,
		}, nil
	}
}

type fetchedImage struct {
	URL    string
	MIME   string
	Bytes  int
	Width  int
	Height int
}

func fetchPublicImage(ctx context.Context, client *http.Client, parsed *url.URL) (fetchedImage, error) {
	if parsed == nil {
		return fetchedImage{}, fmt.Errorf("url is required")
	}
	if client == nil {
		client = newImageHTTPClient()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fetchedImage{}, err
	}
	req.Header.Set("User-Agent", "DonnaChatBot/1.0")
	req.Header.Set("Accept", "image/jpeg,image/png,image/gif,image/webp,image/*;q=0.8")

	res, err := client.Do(req)
	if err != nil {
		return fetchedImage{}, err
	}
	defer res.Body.Close()

	finalURL := parsed.String()
	if res.Request != nil && res.Request.URL != nil {
		finalURL = res.Request.URL.String()
	}
	info := fetchedImage{URL: finalURL}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return info, fmt.Errorf("failed to fetch image (%d)", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, maxImageBytes+1))
	if err != nil {
		return info, err
	}
	if len(body) > maxImageBytes {
		return info, fmt.Errorf("image is larger than %d bytes", maxImageBytes)
	}
	if len(body) == 0 {
		return info, fmt.Errorf("empty image response")
	}

	mime := sniffImageMIME(res.Header.Get("Content-Type"), body)
	if mime == "" {
		return info, fmt.Errorf("url is not a displayable image (need jpeg, png, gif, or webp)")
	}
	info.MIME = mime
	info.Bytes = len(body)

	if cfg, _, err := image.DecodeConfig(bytes.NewReader(body)); err == nil {
		info.Width = cfg.Width
		info.Height = cfg.Height
	}
	return info, nil
}

func sniffImageMIME(contentType string, body []byte) string {
	ct := normalizeMIME(contentType)
	if allowedImageMIME[ct] {
		return normalizeMIME(ct)
	}
	detected := normalizeMIME(http.DetectContentType(body))
	if allowedImageMIME[detected] {
		return detected
	}
	return ""
}

func normalizeMIME(raw string) string {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(raw, ";")[0]))
	if ct == "image/jpg" {
		return "image/jpeg"
	}
	return ct
}

func LooksLikeImageURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	ext := strings.ToLower(path.Ext(u.Path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

func formatImageToolResult(info fetchedImage, alt string) string {
	alt = sanitizeAlt(alt)
	if alt == "" {
		alt = "image"
	}
	var b strings.Builder
	b.WriteString("Verified public image.\n\n")
	fmt.Fprintf(&b, "URL: %s\n", info.URL)
	fmt.Fprintf(&b, "MIME: %s\n", info.MIME)
	fmt.Fprintf(&b, "BYTES: %d\n", info.Bytes)
	if info.Width > 0 && info.Height > 0 {
		fmt.Fprintf(&b, "SIZE: %dx%d\n", info.Width, info.Height)
	}
	b.WriteString("\nEmbed this markdown on its own line in your reply so the user can see the image:\n")
	fmt.Fprintf(&b, "![%s](%s)\n", alt, info.URL)
	b.WriteString("\nDo not invent other image URLs. You cannot generate images.")
	return b.String()
}

func sanitizeAlt(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "[", " ")
	s = strings.ReplaceAll(s, "]", " ")
	s = strings.Join(strings.Fields(s), " ")
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.TrimSpace(s)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
