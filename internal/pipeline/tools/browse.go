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

type BrowserElement struct {
	Ref  string `json:"ref"`
	Tag  string `json:"tag"`
	Type string `json:"type"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type BrowserSnapshot struct {
	URL      string           `json:"url"`
	Title    string           `json:"title"`
	Text     string           `json:"text"`
	Status   int              `json:"status,omitempty"`
	Elements []BrowserElement `json:"elements"`
	Error    string           `json:"error,omitempty"`
}

func (c *BrowserClient) CloseSession(ctx context.Context, sessionID string) error {
	if c == nil {
		return nil
	}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := c.postJSON(ctx, "/session/close", map[string]any{"session_id": sessionID}, &out); err != nil {
		return err
	}
	if out.Error != "" {
		return fmt.Errorf("%s", out.Error)
	}
	return nil
}

func (c *BrowserClient) Navigate(ctx context.Context, sessionID, rawURL string, waitMs int) (BrowserSnapshot, error) {
	parsed, err := ValidatePublicURL(rawURL)
	if err != nil {
		return BrowserSnapshot{}, err
	}
	body := map[string]any{"session_id": sessionID, "url": parsed.String()}
	if waitMs > 0 {
		body["wait_ms"] = waitMs
	}
	return c.sessionSnapshot(ctx, "/session/navigate", body)
}

func (c *BrowserClient) Snapshot(ctx context.Context, sessionID string) (BrowserSnapshot, error) {
	return c.sessionSnapshot(ctx, "/session/snapshot", map[string]any{"session_id": sessionID})
}

func (c *BrowserClient) Click(ctx context.Context, sessionID, ref string) (BrowserSnapshot, error) {
	return c.sessionSnapshot(ctx, "/session/click", map[string]any{
		"session_id": sessionID,
		"ref":        ref,
	})
}

func (c *BrowserClient) Type(ctx context.Context, sessionID, ref, text string, submit bool) (BrowserSnapshot, error) {
	return c.sessionSnapshot(ctx, "/session/type", map[string]any{
		"session_id": sessionID,
		"ref":        ref,
		"text":       text,
		"submit":     submit,
	})
}

func (c *BrowserClient) sessionSnapshot(ctx context.Context, path string, body map[string]any) (BrowserSnapshot, error) {
	var out BrowserSnapshot
	if err := c.postJSON(ctx, path, body, &out); err != nil {
		return BrowserSnapshot{}, err
	}
	if out.Error != "" {
		return out, fmt.Errorf("%s", out.Error)
	}
	return out, nil
}

func (c *BrowserClient) postJSON(ctx context.Context, path string, body any, out any) error {
	if c == nil {
		return fmt.Errorf("browser sidecar not configured")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2_000_000))
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil && res.StatusCode >= 200 && res.StatusCode < 300 {
			return fmt.Errorf("invalid browser response")
		}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &errBody)
		msg := strings.TrimSpace(errBody.Error)
		if msg == "" {
			msg = fmt.Sprintf("browser returned status %d", res.StatusCode)
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func FormatBrowserSnapshot(snap BrowserSnapshot) string {
	title := strings.TrimSpace(snap.Title)
	if title == "" {
		title = snap.URL
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\nURL: %s\n", title, snap.URL)
	if len(snap.Elements) > 0 {
		b.WriteString("\nInteractive elements (use ref with browser_click / browser_type):\n")
		for _, el := range snap.Elements {
			label := strings.TrimSpace(el.Name)
			if label == "" {
				label = el.Tag
			}
			fmt.Fprintf(&b, "- %s <%s", el.Ref, el.Tag)
			if el.Type != "" {
				fmt.Fprintf(&b, " type=%s", el.Type)
			}
			b.WriteString("> ")
			b.WriteString(label)
			b.WriteByte('\n')
		}
	}
	text := strings.TrimSpace(snap.Text)
	if text != "" {
		b.WriteString("\n")
		b.WriteString(text)
	}
	return b.String()
}
