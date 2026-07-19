package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
)

const defaultBrowseMaxChars = 16_000

// BrowserClient talks to the donna-browser Playwright sidecar.
type BrowserClient struct {
	BaseURL string
	Client  *http.Client
}

func NewBrowserClient(baseURL string) *BrowserClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &BrowserClient{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 45 * time.Second},
	}
}

func BrowsePageDefinition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Type: "function",
		Function: providers.ToolFunctionSchema{
			Name:        "browse_page",
			Description: "Open a public page in a real browser with JavaScript executed, then return extracted main text. Use when fetch_url returns empty/incomplete content or the page is clearly a JS-rendered app.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "Fully-qualified http or https URL to open",
					},
					"wait_ms": map[string]any{
						"type":        "integer",
						"description": "Optional extra wait after load before extraction (ms)",
					},
					"max_chars": map[string]any{
						"type":        "integer",
						"description": "Optional max characters of extracted text to return",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

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

func NewBrowsePageHandler(browser *BrowserClient) Handler {
	return func(ctx context.Context, argsJSON string) (Result, error) {
		if browser == nil {
			return Result{Content: "Error: browse_page is unavailable (browser sidecar not configured)"}, nil
		}
		var args browseRequest
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		parsed, err := ValidatePublicURL(args.URL)
		if err != nil {
			return Result{Content: "Error: " + err.Error()}, nil
		}
		args.URL = parsed.String()
		if args.MaxChars <= 0 {
			args.MaxChars = defaultBrowseMaxChars
		}

		payload, err := json.Marshal(args)
		if err != nil {
			return Result{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, browser.BaseURL+"/browse", bytes.NewReader(payload))
		if err != nil {
			return Result{}, err
		}
		req.Header.Set("Content-Type", "application/json")

		res, err := browser.Client.Do(req)
		if err != nil {
			return Result{
				Content: fmt.Sprintf("Error browsing %s: %s", args.URL, err.Error()),
				Phase:   string(protocol.TurnPhaseBrowsing),
				Host:    parsed.Host,
			}, nil
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2_000_000))

		var out browseResponse
		if err := json.Unmarshal(body, &out); err != nil {
			return Result{
				Content: fmt.Sprintf("Error browsing %s: invalid browser response", args.URL),
				Phase:   string(protocol.TurnPhaseBrowsing),
				Host:    parsed.Host,
			}, nil
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 || out.Error != "" {
			msg := strings.TrimSpace(out.Error)
			if msg == "" {
				msg = fmt.Sprintf("browser returned status %d", res.StatusCode)
			}
			return Result{
				Content: fmt.Sprintf("Error browsing %s: %s", args.URL, msg),
				Phase:   string(protocol.TurnPhaseBrowsing),
				Host:    parsed.Host,
			}, nil
		}

		finalURL := strings.TrimSpace(out.URL)
		if finalURL == "" {
			finalURL = args.URL
		}
		title := strings.TrimSpace(out.Title)
		if title == "" {
			title = parsed.Host
		}
		text := strings.TrimSpace(out.Text)
		if text == "" {
			text = fmt.Sprintf("No text content extracted from %s after browsing.", finalURL)
		}

		return Result{
			Content: fmt.Sprintf("# %s\nURL: %s\n\n%s", title, finalURL, text),
			Citations: []Citation{{
				URL:     finalURL,
				Title:   title,
				Content: truncateSnippet(text, 240),
			}},
			Phase: string(protocol.TurnPhaseBrowsing),
			Host:  parsed.Host,
		}, nil
	}
}
