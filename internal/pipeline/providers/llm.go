package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const openRouterBase = "https://openrouter.ai/api/v1"

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function ToolFunctionSchema `json:"function"`
}

type ToolFunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type LLM struct {
	APIKey      string
	Model       string
	VisionModel string
	Client      *http.Client
}

type ChatCompletionOptions struct {
	WebSearch           bool
	WebSearchMaxResults int
	Tools               []ToolDefinition
	ToolChoice          any // "auto" | "none" | map
}

type ChatCompletionMetadata struct {
	WebCitations []WebCitation
	ToolCalls    []ToolCall
	FinishReason string
	Content      string
}

type WebCitation struct {
	URL        string
	Title      string
	Content    string
	StartIndex int
	EndIndex   int
}

func NewLLM(apiKey, model, visionModel string) *LLM {
	if visionModel == "" {
		visionModel = model
	}
	return &LLM{
		APIKey:      apiKey,
		Model:       model,
		VisionModel: visionModel,
		Client:      &http.Client{Timeout: 120 * time.Second},
	}
}

func (l *LLM) WithModel(model string) *LLM {
	if model == "" || model == l.Model {
		return l
	}
	copy := *l
	copy.Model = model
	return &copy
}

func (l *LLM) visionModel() string {
	if l.VisionModel != "" {
		return l.VisionModel
	}
	return l.Model
}

func (l *LLM) headers() map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + l.APIKey,
		"Content-Type":  "application/json",
	}
}

func BuildLLMMessages(systemPrompt string, history []ChatMessage, userMessage string) []ChatMessage {
	messages := make([]ChatMessage, 0, len(history)+2)
	messages = append(messages, ChatMessage{Role: "system", Content: systemPrompt})
	messages = append(messages, history...)
	messages = append(messages, ChatMessage{Role: "user", Content: userMessage})
	return messages
}

func (l *LLM) chatRequestBody(messages []ChatMessage, stream bool, options ChatCompletionOptions) ([]byte, error) {
	payload := map[string]any{
		"model":    l.Model,
		"messages": messages,
		"stream":   stream,
	}
	if options.WebSearch {
		maxResults := options.WebSearchMaxResults
		if maxResults <= 0 {
			maxResults = 3
		}
		payload["plugins"] = []map[string]any{{
			"id":          "web",
			"max_results": maxResults,
		}}
	}
	if len(options.Tools) > 0 {
		payload["tools"] = options.Tools
		if options.ToolChoice != nil {
			payload["tool_choice"] = options.ToolChoice
		} else {
			payload["tool_choice"] = "auto"
		}
	}
	return json.Marshal(payload)
}

func (l *LLM) StreamCompletion(ctx context.Context, messages []ChatMessage, onChunk func(string) error) error {
	_, err := l.StreamCompletionWithOptions(ctx, messages, ChatCompletionOptions{}, onChunk)
	return err
}

func (l *LLM) StreamCompletionWithOptions(ctx context.Context, messages []ChatMessage, options ChatCompletionOptions, onChunk func(string) error) (ChatCompletionMetadata, error) {
	body, err := l.chatRequestBody(messages, true, options)
	if err != nil {
		return ChatCompletionMetadata{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterBase+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatCompletionMetadata{}, err
	}
	for k, v := range l.headers() {
		req.Header.Set(k, v)
	}

	res, err := l.Client.Do(req)
	if err != nil {
		return ChatCompletionMetadata{}, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return ChatCompletionMetadata{}, fmt.Errorf("OpenRouter LLM %d: %s", res.StatusCode, string(b))
	}

	var meta ChatCompletionMetadata
	toolAcc := map[int]*ToolCall{}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content     string                  `json:"content"`
					ToolCalls   []streamToolCallDelta   `json:"tool_calls"`
					Annotations []urlCitationAnnotation `json:"annotations"`
				} `json:"delta"`
				Message struct {
					Annotations []urlCitationAnnotation `json:"annotations"`
				} `json:"message"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		meta.WebCitations = appendURLCitations(meta.WebCitations, choice.Delta.Annotations)
		meta.WebCitations = appendURLCitations(meta.WebCitations, choice.Message.Annotations)
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			meta.FinishReason = *choice.FinishReason
		}
		accumulateToolCallDeltas(toolAcc, choice.Delta.ToolCalls)
		text := choice.Delta.Content
		if text != "" {
			meta.Content += text
			if onChunk != nil {
				if err := onChunk(text); err != nil {
					return ChatCompletionMetadata{}, err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ChatCompletionMetadata{}, err
	}
	meta.ToolCalls = toolCallsFromAccumulator(toolAcc)
	if meta.FinishReason == "" {
		if len(meta.ToolCalls) > 0 {
			meta.FinishReason = "tool_calls"
		} else {
			meta.FinishReason = "stop"
		}
	}
	return meta, nil
}

// CompleteOnceWithOptions runs a non-streaming completion. Used for tool rounds
// where parsing tool_calls from a full message is simpler than streaming deltas.
func (l *LLM) CompleteOnceWithOptions(ctx context.Context, messages []ChatMessage, options ChatCompletionOptions) (ChatCompletionMetadata, error) {
	body, err := l.chatRequestBody(messages, false, options)
	if err != nil {
		return ChatCompletionMetadata{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterBase+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatCompletionMetadata{}, err
	}
	for k, v := range l.headers() {
		req.Header.Set(k, v)
	}

	res, err := l.Client.Do(req)
	if err != nil {
		return ChatCompletionMetadata{}, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return ChatCompletionMetadata{}, fmt.Errorf("OpenRouter LLM %d: %s", res.StatusCode, string(b))
	}

	var out struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content     string                  `json:"content"`
				ToolCalls   []ToolCall              `json:"tool_calls"`
				Annotations []urlCitationAnnotation `json:"annotations"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return ChatCompletionMetadata{}, err
	}
	if len(out.Choices) == 0 {
		return ChatCompletionMetadata{}, nil
	}
	choice := out.Choices[0]
	meta := ChatCompletionMetadata{
		Content:      strings.TrimSpace(choice.Message.Content),
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
		WebCitations: appendURLCitations(nil, choice.Message.Annotations),
	}
	if meta.FinishReason == "" {
		if len(meta.ToolCalls) > 0 {
			meta.FinishReason = "tool_calls"
		} else {
			meta.FinishReason = "stop"
		}
	}
	return meta, nil
}

func (l *LLM) CompleteOnce(ctx context.Context, messages []ChatMessage) (string, error) {
	meta, err := l.CompleteOnceWithOptions(ctx, messages, ChatCompletionOptions{})
	if err != nil {
		return "", err
	}
	return meta.Content, nil
}

type streamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func accumulateToolCallDeltas(acc map[int]*ToolCall, deltas []streamToolCallDelta) {
	for _, delta := range deltas {
		call, ok := acc[delta.Index]
		if !ok {
			call = &ToolCall{Type: "function"}
			acc[delta.Index] = call
		}
		if delta.ID != "" {
			call.ID = delta.ID
		}
		if delta.Type != "" {
			call.Type = delta.Type
		}
		if delta.Function.Name != "" {
			call.Function.Name = delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			call.Function.Arguments += delta.Function.Arguments
		}
	}
}

func toolCallsFromAccumulator(acc map[int]*ToolCall) []ToolCall {
	if len(acc) == 0 {
		return nil
	}
	maxIdx := -1
	for idx := range acc {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	out := make([]ToolCall, 0, len(acc))
	for i := 0; i <= maxIdx; i++ {
		if call, ok := acc[i]; ok && call.Function.Name != "" {
			if call.Type == "" {
				call.Type = "function"
			}
			out = append(out, *call)
		}
	}
	return out
}

type urlCitationAnnotation struct {
	Type        string `json:"type"`
	URLCitation struct {
		URL        string `json:"url"`
		Title      string `json:"title"`
		Content    string `json:"content"`
		StartIndex int    `json:"start_index"`
		EndIndex   int    `json:"end_index"`
	} `json:"url_citation"`
}

func appendURLCitations(existing []WebCitation, annotations []urlCitationAnnotation) []WebCitation {
	for _, annotation := range annotations {
		if annotation.Type != "url_citation" {
			continue
		}
		citation := annotation.URLCitation
		url := strings.TrimSpace(citation.URL)
		if url == "" {
			continue
		}
		duplicate := false
		for _, item := range existing {
			if item.URL == url {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		existing = append(existing, WebCitation{
			URL:        url,
			Title:      strings.TrimSpace(citation.Title),
			Content:    strings.TrimSpace(citation.Content),
			StartIndex: citation.StartIndex,
			EndIndex:   citation.EndIndex,
		})
	}
	return existing
}

func (l *LLM) CompleteOnceVision(ctx context.Context, prompt, imageDataURL string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model": l.visionModel(),
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": prompt},
					{"type": "image_url", "image_url": map[string]string{"url": imageDataURL}},
				},
			},
		},
		"stream": false,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterBase+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	for k, v := range l.headers() {
		req.Header.Set(k, v)
	}

	res, err := l.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return "", fmt.Errorf("OpenRouter vision %d: %s", res.StatusCode, string(b))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", nil
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
