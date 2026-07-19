package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge/ingest"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
)

const defaultFetchMaxChars = 12_000

func FetchURLDefinition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Type: "function",
		Function: providers.ToolFunctionSchema{
			Name:        "fetch_url",
			Description: "Fetch a public HTTP(S) page and return extracted text. Prefer for static HTML, docs, and blogs. If the result is empty or clearly incomplete (JS-heavy SPA), use browse_page instead when available.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "Fully-qualified http or https URL to fetch",
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

func NewFetchURLHandler() Handler {
	return func(ctx context.Context, argsJSON string) (Result, error) {
		var args struct {
			URL      string `json:"url"`
			MaxChars int    `json:"max_chars"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
		parsed, err := ValidatePublicURL(args.URL)
		if err != nil {
			return Result{Content: "Error: " + err.Error()}, nil
		}

		extracted, err := ingest.ExtractURL(parsed.String())
		if err != nil {
			return Result{
				Content: fmt.Sprintf("Error fetching %s: %s", parsed.String(), err.Error()),
				Phase:   string(protocol.TurnPhaseFetching),
				Host:    parsed.Host,
			}, nil
		}

		maxChars := args.MaxChars
		if maxChars <= 0 {
			maxChars = defaultFetchMaxChars
		}
		content := strings.TrimSpace(extracted.Content)
		if len(content) > maxChars {
			content = content[:maxChars] + "\n\n[truncated]"
		}
		if content == "" {
			content = fmt.Sprintf("No text content extracted from %s. Try browse_page if available.", parsed.String())
		}

		title := strings.TrimSpace(extracted.Title)
		if title == "" {
			title = parsed.Host
		}
		return Result{
			Content: content,
			Citations: []Citation{{
				URL:     parsed.String(),
				Title:   title,
				Content: truncateSnippet(content, 240),
			}},
			Phase: string(protocol.TurnPhaseFetching),
			Host:  parsed.Host,
		}, nil
	}
}

func truncateSnippet(text string, n int) string {
	text = strings.TrimSpace(text)
	if len(text) <= n {
		return text
	}
	return text[:n] + "…"
}
