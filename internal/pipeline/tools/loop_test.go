package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
)

func TestRunToolLoop_executesFetchThenAnswers(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &req)

		if n == 1 {
			// Streaming first round with tool_calls.
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"fetch_url","arguments":"{\"url\":\"https://example.com\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}

		// Non-stream final answer after tools.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": "Example Domain is a placeholder site.",
				},
			}},
		})
		_ = req // silence
	}))
	t.Cleanup(server.Close)

	llm := providers.NewLLM("test", "provider/model", "")
	llm.Client = &http.Client{Transport: rewriteHost(server.URL)}

	reg := NewRegistry()
	reg.Register(RegisteredTool{
		Definition: FetchURLDefinition(),
		Handle: func(ctx context.Context, argsJSON string) (Result, error) {
			return Result{
				Content: "# example.com\nURL: https://example.com\n\nExample Domain",
				Citations: []Citation{{
					URL:   "https://example.com",
					Title: "example.com",
				}},
				Phase: string(protocol.TurnPhaseFetching),
				Host:  "example.com",
			}, nil
		},
	})

	var phases []protocol.TurnPhase
	var statusHosts []string
	result, err := RunToolLoop(
		context.Background(),
		llm,
		[]providers.ChatMessage{{Role: "user", Content: "summarize https://example.com"}},
		reg,
		providers.ChatCompletionOptions{},
		LoopLimits{MaxRounds: 3},
		LoopCallbacks{
			OnPhase: func(p protocol.TurnPhase) { phases = append(phases, p) },
			OnStatus: func(p protocol.TurnPhase, host string) {
				phases = append(phases, p)
				statusHosts = append(statusHosts, host)
			},
			OnReply: func(text string) error { return nil },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.ReplyText, "Example Domain") {
		t.Fatalf("reply = %q", result.ReplyText)
	}
	if len(result.Citations) != 1 || result.Citations[0].URL != "https://example.com" {
		t.Fatalf("citations = %#v", result.Citations)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", calls.Load())
	}
	sawFetch := false
	sawGenerating := false
	for _, p := range phases {
		if p == protocol.TurnPhaseFetching {
			sawFetch = true
		}
		if p == protocol.TurnPhaseGenerating {
			sawGenerating = true
		}
	}
	if !sawFetch {
		t.Fatalf("expected fetching phase, got %#v", phases)
	}
	if !sawGenerating {
		t.Fatalf("expected generating phase after tools, got %#v", phases)
	}
	foundHost := false
	for _, h := range statusHosts {
		if h == "example.com" {
			foundHost = true
			break
		}
	}
	if !foundHost {
		t.Fatalf("expected example.com status host, got %#v", statusHosts)
	}
}

func TestRunToolLoop_capsRounds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Stream     bool `json:"stream"`
			ToolChoice any  `json:"tool_choice"`
		}
		_ = json.Unmarshal(body, &req)

		forceText := req.ToolChoice == "none" || n >= 3
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			if forceText {
				_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}` + "\n\n"))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				return
			}
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"fetch_url","arguments":"{\"url\":\"https://example.com\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if forceText {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"finish_reason": "stop",
					"message":       map[string]any{"role": "assistant", "content": "done"},
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":   "call_x",
						"type": "function",
						"function": map[string]any{
							"name":      "fetch_url",
							"arguments": `{"url":"https://example.com"}`,
						},
					}},
				},
			}},
		})
	}))
	t.Cleanup(server.Close)

	llm := providers.NewLLM("test", "provider/model", "")
	llm.Client = &http.Client{Transport: rewriteHost(server.URL)}

	reg := NewRegistry()
	reg.Register(RegisteredTool{
		Definition: FetchURLDefinition(),
		Handle: func(ctx context.Context, argsJSON string) (Result, error) {
			return Result{Content: "page"}, nil
		},
	})

	result, err := RunToolLoop(
		context.Background(),
		llm,
		[]providers.ChatMessage{{Role: "user", Content: "go"}},
		reg,
		providers.ChatCompletionOptions{},
		LoopLimits{MaxRounds: 3},
		LoopCallbacks{OnReply: func(string) error { return nil }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReplyText != "done" {
		t.Fatalf("reply = %q calls=%d", result.ReplyText, calls.Load())
	}
}

type rewriteRoundTripper struct {
	base   string
	parent http.RoundTripper
}

func rewriteHost(base string) http.RoundTripper {
	return rewriteRoundTripper{base: strings.TrimRight(base, "/"), parent: http.DefaultTransport}
}

func (r rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	u := r.base + "/chat/completions"
	out, err := http.NewRequestWithContext(req.Context(), req.Method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	out.Header = req.Header.Clone()
	if r.parent == nil {
		r.parent = http.DefaultTransport
	}
	return r.parent.RoundTrip(out)
}
