package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/protocol"
)

const (
	DefaultMaxRounds      = 3
	DefaultMaxFetchCalls  = 3
	DefaultMaxBrowseCalls = 2
	DefaultTotalBudget    = 45 * time.Second
)

// LoopLimits caps tool use per chat turn.
type LoopLimits struct {
	MaxRounds      int
	MaxFetchCalls  int
	MaxBrowseCalls int
	TotalBudget    time.Duration
}

func (l LoopLimits) withDefaults() LoopLimits {
	if l.MaxRounds <= 0 {
		l.MaxRounds = DefaultMaxRounds
	}
	if l.MaxFetchCalls <= 0 {
		l.MaxFetchCalls = DefaultMaxFetchCalls
	}
	if l.MaxBrowseCalls <= 0 {
		l.MaxBrowseCalls = DefaultMaxBrowseCalls
	}
	if l.TotalBudget <= 0 {
		l.TotalBudget = DefaultTotalBudget
	}
	return l
}

// LoopCallbacks surface progress to the chat turn.
type LoopCallbacks struct {
	OnPhase func(protocol.TurnPhase)
	// OnReply receives cumulative assistant text for the final answer.
	OnReply func(string) error
	// OnStatus is optional richer status (e.g. host being fetched).
	OnStatus func(phase protocol.TurnPhase, host string)
}

// LoopResult is the final model answer after any tool rounds.
type LoopResult struct {
	ReplyText  string
	Citations  []Citation
	FirstToken time.Duration
}

// RunToolLoop executes OpenAI-style tool calls until the model stops or limits hit.
// Tool rounds use non-streaming completions; the final answer is streamed when possible.
func RunToolLoop(
	ctx context.Context,
	llm *providers.LLM,
	messages []providers.ChatMessage,
	registry *Registry,
	baseOptions providers.ChatCompletionOptions,
	limits LoopLimits,
	callbacks LoopCallbacks,
) (LoopResult, error) {
	limits = limits.withDefaults()
	if registry == nil || registry.Len() == 0 {
		return streamFinal(ctx, llm, messages, baseOptions, callbacks)
	}

	deadline := time.Now().Add(limits.TotalBudget)
	defs := registry.Definitions()
	baseOptions.Tools = defs
	if baseOptions.ToolChoice == nil {
		baseOptions.ToolChoice = "auto"
	}

	working := append([]providers.ChatMessage(nil), messages...)
	var citations []Citation
	fetchCalls := 0
	browseCalls := 0
	var firstToken time.Duration
	llmStart := time.Now()

	for round := 0; round < limits.MaxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return LoopResult{}, err
		}
		if time.Now().After(deadline) {
			break
		}

		// Last round: force a text answer (no more tools).
		options := baseOptions
		if round == limits.MaxRounds-1 {
			options.ToolChoice = "none"
		}

		// Prefer streaming on the first round so simple chats keep TTFT.
		// Tool-call responses usually stream no content; if they do, the UI may
		// briefly show a preamble that is replaced after tools finish.
		var meta providers.ChatCompletionMetadata
		var err error
		streamed := ""
		if round == 0 {
			meta, err = llm.StreamCompletionWithOptions(ctx, working, options, func(chunk string) error {
				if firstToken == 0 {
					firstToken = time.Since(llmStart)
				}
				streamed += chunk
				if callbacks.OnReply != nil {
					return callbacks.OnReply(streamed)
				}
				return nil
			})
		} else {
			meta, err = llm.CompleteOnceWithOptions(ctx, working, options)
			if meta.Content != "" && firstToken == 0 {
				firstToken = time.Since(llmStart)
			}
		}
		if err != nil {
			return LoopResult{}, err
		}
		citations = appendCitations(citations, webToToolCitations(meta.WebCitations))

		if len(meta.ToolCalls) > 0 {
			assistant := providers.ChatMessage{
				Role:      "assistant",
				Content:   meta.Content,
				ToolCalls: meta.ToolCalls,
			}
			working = append(working, assistant)

			for _, call := range meta.ToolCalls {
				name := strings.TrimSpace(call.Function.Name)
				switch name {
				case "fetch_url", "fetch_image":
					fetchCalls++
					if fetchCalls > limits.MaxFetchCalls {
						working = append(working, toolMessage(call, "Error: fetch call limit reached for this turn"))
						continue
					}
				case "browse_page":
					browseCalls++
					if browseCalls > limits.MaxBrowseCalls {
						working = append(working, toolMessage(call, "Error: browse_page call limit reached for this turn"))
						continue
					}
				}

				tool, ok := registry.Get(name)
				if !ok {
					working = append(working, toolMessage(call, "Error: unknown tool "+name))
					continue
				}

				// Tell the UI what we're doing before the (often slow) tool runs.
				if phase, host := peekToolStatus(name, call.Function.Arguments); phase != "" {
					if callbacks.OnStatus != nil {
						callbacks.OnStatus(phase, host)
					} else if callbacks.OnPhase != nil {
						callbacks.OnPhase(phase)
					}
				}

				result, toolErr := tool.Handle(ctx, call.Function.Arguments)
				if toolErr != nil {
					working = append(working, toolMessage(call, "Error: "+toolErr.Error()))
					log.Warn("chat tool failed", map[string]any{
						"tool":  name,
						"error": toolErr.Error(),
					})
					continue
				}
				citations = appendCitations(citations, result.Citations)
				content := strings.TrimSpace(result.Content)
				if content == "" {
					content = "Error: tool returned empty content"
				}
				working = append(working, toolMessage(call, content))
				log.Print("chat tool ok", map[string]any{
					"tool":  name,
					"host":  result.Host,
					"chars": len(content),
				})
			}

			// Back to the model — it may answer or call another tool.
			if callbacks.OnStatus != nil {
				callbacks.OnStatus(protocol.TurnPhaseGenerating, "")
			} else if callbacks.OnPhase != nil {
				callbacks.OnPhase(protocol.TurnPhaseGenerating)
			}
			continue
		}

		// Final text answer (no tool calls).
		reply := strings.TrimSpace(streamed)
		if reply == "" {
			reply = strings.TrimSpace(meta.Content)
		}
		if reply == "" {
			// Model returned neither tools nor text — ask once without tools.
			options.ToolChoice = "none"
			options.Tools = nil
			streamedReply := ""
			meta, err = llm.StreamCompletionWithOptions(ctx, working, options, func(chunk string) error {
				if firstToken == 0 {
					firstToken = time.Since(llmStart)
				}
				streamedReply += chunk
				if callbacks.OnReply != nil {
					return callbacks.OnReply(streamedReply)
				}
				return nil
			})
			if err != nil {
				return LoopResult{}, err
			}
			citations = appendCitations(citations, webToToolCitations(meta.WebCitations))
			reply = strings.TrimSpace(streamedReply)
			if reply == "" {
				reply = strings.TrimSpace(meta.Content)
			}
			return LoopResult{ReplyText: reply, Citations: citations, FirstToken: firstToken}, nil
		}

		// Round 0 already streamed via OnReply. Later rounds used CompleteOnce.
		if round > 0 && callbacks.OnReply != nil {
			if err := callbacks.OnReply(reply); err != nil {
				return LoopResult{}, err
			}
		}
		return LoopResult{ReplyText: reply, Citations: citations, FirstToken: firstToken}, nil
	}

	// Hit round/time limits with pending context — force a closing answer.
	options := baseOptions
	options.ToolChoice = "none"
	streamedReply := ""
	meta, err := llm.StreamCompletionWithOptions(ctx, working, options, func(chunk string) error {
		if firstToken == 0 {
			firstToken = time.Since(llmStart)
		}
		streamedReply += chunk
		if callbacks.OnReply != nil {
			return callbacks.OnReply(streamedReply)
		}
		return nil
	})
	if err != nil {
		return LoopResult{}, err
	}
	citations = appendCitations(citations, webToToolCitations(meta.WebCitations))
	reply := strings.TrimSpace(streamedReply)
	if reply == "" {
		reply = strings.TrimSpace(meta.Content)
	}
	if reply == "" {
		reply = "I couldn't finish reading that page. Please try again or paste the key section."
		if callbacks.OnReply != nil {
			_ = callbacks.OnReply(reply)
		}
	}
	return LoopResult{ReplyText: reply, Citations: citations, FirstToken: firstToken}, nil
}

func streamFinal(
	ctx context.Context,
	llm *providers.LLM,
	messages []providers.ChatMessage,
	options providers.ChatCompletionOptions,
	callbacks LoopCallbacks,
) (LoopResult, error) {
	options.Tools = nil
	options.ToolChoice = nil
	llmStart := time.Now()
	var firstToken time.Duration
	reply := ""
	meta, err := llm.StreamCompletionWithOptions(ctx, messages, options, func(chunk string) error {
		if firstToken == 0 {
			firstToken = time.Since(llmStart)
		}
		reply += chunk
		if callbacks.OnReply != nil {
			return callbacks.OnReply(reply)
		}
		return nil
	})
	if err != nil {
		return LoopResult{}, err
	}
	return LoopResult{
		ReplyText:  reply,
		Citations:  webToToolCitations(meta.WebCitations),
		FirstToken: firstToken,
	}, nil
}

func toolMessage(call providers.ToolCall, content string) providers.ChatMessage {
	id := call.ID
	if id == "" {
		id = fmt.Sprintf("tool_%s", call.Function.Name)
	}
	return providers.ChatMessage{
		Role:       "tool",
		ToolCallID: id,
		Name:       call.Function.Name,
		Content:    content,
	}
}

// peekToolStatus returns a UI phase (+ optional host) before a tool executes.
func peekToolStatus(name, argsJSON string) (protocol.TurnPhase, string) {
	switch strings.TrimSpace(name) {
	case "fetch_url":
		return protocol.TurnPhaseFetching, peekArgHost(argsJSON)
	case "fetch_image":
		return protocol.TurnPhaseLoadingImage, peekArgHost(argsJSON)
	case "browse_page":
		return protocol.TurnPhaseBrowsing, peekArgHost(argsJSON)
	default:
		return "", ""
	}
}

func peekArgHost(argsJSON string) string {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	raw := strings.TrimSpace(args.URL)
	if raw == "" {
		return ""
	}
	if parsed, err := ValidatePublicURL(raw); err == nil {
		return parsed.Host
	}
	if u, err := url.Parse(raw); err == nil {
		return strings.TrimSpace(u.Host)
	}
	return ""
}

func webToToolCitations(in []providers.WebCitation) []Citation {
	out := make([]Citation, 0, len(in))
	for _, c := range in {
		out = append(out, Citation{
			URL:     c.URL,
			Title:   c.Title,
			Content: c.Content,
		})
	}
	return out
}

func appendCitations(existing []Citation, extra []Citation) []Citation {
	for _, c := range extra {
		url := strings.TrimSpace(c.URL)
		if url == "" {
			continue
		}
		dup := false
		for _, e := range existing {
			if e.URL == url {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		existing = append(existing, c)
	}
	return existing
}

// BrowseToolsPrompt is appended to the system prompt when tools are available.
const BrowseToolsPrompt = `You can use tools to read websites and show images when needed:
- fetch_url: prefer for static HTML, docs, and blogs.
- browse_page: use when fetch_url fails or the page needs JavaScript (only if available).
- fetch_image: use when you have a direct public image URL (jpeg/png/gif/webp) and should show it to the user. After it succeeds, include the returned markdown image (` + "`![description](url)`" + `) on its own line in your reply. Never invent image URLs. You cannot generate images.
Do not browse localhost, private networks, or file URLs. Cite the pages you read. Do not invent page content.`
