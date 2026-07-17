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
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLM struct {
	APIKey      string
	Model       string
	VisionModel string
	Client      *http.Client
	// BaseURL overrides OpenRouter for tests. Empty uses the production API.
	BaseURL string
}

type ChatCompletionOptions struct {
	WebSearch           bool
	WebSearchMaxResults int
	// MaxTokens caps completion tokens (reasoning + final answer). Zero uses
	// defaultChatMaxTokens. Reasoning models like kimi-k3 need headroom so
	// thinking does not consume the entire budget and leave an empty reply.
	MaxTokens int
	// ExcludeReasoning asks OpenRouter not to stream reasoning tokens. The
	// model still thinks when required; we only wait for final content.
	ExcludeReasoning bool
	// OnActivity is invoked on OpenRouter keepalives and reasoning-only
	// deltas so callers can flush SSE heartbeats to the browser.
	OnActivity func()
}

type ChatCompletionMetadata struct {
	WebCitations []WebCitation
}

type WebCitation struct {
	URL        string
	Title      string
	Content    string
	StartIndex int
	EndIndex   int
}

const defaultChatMaxTokens = 8192

func NewLLM(apiKey, model, visionModel string) *LLM {
	if visionModel == "" {
		visionModel = model
	}
	return &LLM{
		APIKey:      apiKey,
		Model:       model,
		VisionModel: visionModel,
		// Reasoning models (e.g. kimi-k3) can think for minutes before content.
		Client: &http.Client{Timeout: 5 * time.Minute},
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

func (l *LLM) apiBase() string {
	if strings.TrimSpace(l.BaseURL) != "" {
		return strings.TrimRight(l.BaseURL, "/")
	}
	return openRouterBase
}

func BuildLLMMessages(systemPrompt string, history []ChatMessage, userMessage string) []ChatMessage {
	messages := make([]ChatMessage, 0, len(history)+2)
	messages = append(messages, ChatMessage{Role: "system", Content: systemPrompt})
	messages = append(messages, history...)
	messages = append(messages, ChatMessage{Role: "user", Content: userMessage})
	return messages
}

func (l *LLM) chatRequestBody(messages []ChatMessage, stream bool, options ChatCompletionOptions) ([]byte, error) {
	maxTokens := options.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultChatMaxTokens
	}
	payload := map[string]any{
		"model":      l.Model,
		"messages":   messages,
		"stream":     stream,
		"max_tokens": maxTokens,
	}
	// Exclude reasoning from the wire format for Donna chat/voice. Kimi K3
	// reasons at "max" effort by default; streaming that thinking can idle
	// the client SSE connection and starve content tokens.
	if options.ExcludeReasoning {
		payload["reasoning"] = map[string]any{"exclude": true}
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
	return json.Marshal(payload)
}

func (l *LLM) StreamCompletion(ctx context.Context, messages []ChatMessage, onChunk func(string) error) error {
	_, err := l.StreamCompletionWithOptions(ctx, messages, ChatCompletionOptions{
		ExcludeReasoning: true,
	}, onChunk)
	return err
}

func (l *LLM) StreamCompletionWithOptions(ctx context.Context, messages []ChatMessage, options ChatCompletionOptions, onChunk func(string) error) (ChatCompletionMetadata, error) {
	body, err := l.chatRequestBody(messages, true, options)
	if err != nil {
		return ChatCompletionMetadata{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.apiBase()+"/chat/completions", bytes.NewReader(body))
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
	receivedContent := false
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	notifyActivity := func() {
		if options.OnActivity != nil {
			options.OnActivity()
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		// OpenRouter sends SSE comments (e.g. ": OPENROUTER PROCESSING") as keepalives.
		if strings.HasPrefix(line, ":") {
			notifyActivity()
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		text, citations, activity, streamErr := parseStreamChunk(payload)
		if streamErr != nil {
			return ChatCompletionMetadata{}, streamErr
		}
		if activity {
			notifyActivity()
		}
		if len(citations) > 0 {
			meta.WebCitations = appendURLCitations(meta.WebCitations, citations)
		}
		if text == "" {
			continue
		}
		receivedContent = true
		if onChunk != nil {
			if err := onChunk(text); err != nil {
				return ChatCompletionMetadata{}, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ChatCompletionMetadata{}, err
	}
	if !receivedContent {
		return ChatCompletionMetadata{}, fmt.Errorf("OpenRouter LLM returned an empty reply (model %s). Try another model in Profile, or disable web search", l.Model)
	}
	return meta, nil
}

// parseStreamChunk extracts text deltas and citations from one OpenRouter SSE
// payload. Mid-stream provider failures arrive as HTTP 200 with an error field
// and/or finish_reason "error" — those must not be treated as empty success.
// activity is true when the chunk indicates the model is still working
// (reasoning tokens) without producing visible content yet.
func parseStreamChunk(payload string) (text string, citations []urlCitationAnnotation, activity bool, err error) {
	var chunk struct {
		Error *struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
		} `json:"error"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Delta        struct {
				Content          string                  `json:"content"`
				Reasoning        string                  `json:"reasoning"`
				ReasoningContent string                  `json:"reasoning_content"`
				ReasoningDetails json.RawMessage         `json:"reasoning_details"`
				Annotations      []urlCitationAnnotation `json:"annotations"`
			} `json:"delta"`
			Message struct {
				Content     string                  `json:"content"`
				Annotations []urlCitationAnnotation `json:"annotations"`
			} `json:"message"`
		} `json:"choices"`
	}
	if unmarshalErr := json.Unmarshal([]byte(payload), &chunk); unmarshalErr != nil {
		return "", nil, false, nil
	}
	if chunk.Error != nil {
		msg := strings.TrimSpace(chunk.Error.Message)
		if msg == "" {
			msg = "provider stream error"
		}
		return "", nil, false, fmt.Errorf("OpenRouter LLM stream error: %s", msg)
	}
	if len(chunk.Choices) == 0 {
		return "", nil, false, nil
	}
	choice := chunk.Choices[0]
	if strings.EqualFold(choice.FinishReason, "error") {
		return "", nil, false, fmt.Errorf("OpenRouter LLM stream error: provider disconnected")
	}
	if strings.EqualFold(choice.FinishReason, "content_filter") {
		return "", nil, false, fmt.Errorf("OpenRouter LLM stream error: response blocked by content filter")
	}
	citations = append(citations, choice.Delta.Annotations...)
	citations = append(citations, choice.Message.Annotations...)
	text = choice.Delta.Content
	if text == "" {
		text = choice.Message.Content
	}
	if text == "" {
		activity = choice.Delta.Reasoning != "" ||
			choice.Delta.ReasoningContent != "" ||
			len(choice.Delta.ReasoningDetails) > 0
	}
	return text, citations, activity, nil
}

func (l *LLM) CompleteOnce(ctx context.Context, messages []ChatMessage) (string, error) {
	body, err := l.chatRequestBody(messages, false, ChatCompletionOptions{
		MaxTokens:        2048,
		ExcludeReasoning: true,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.apiBase()+"/chat/completions", bytes.NewReader(body))
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
		return "", fmt.Errorf("OpenRouter LLM %d: %s", res.StatusCode, string(b))
	}

	var out struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		msg := strings.TrimSpace(out.Error.Message)
		if msg == "" {
			msg = "provider error"
		}
		return "", fmt.Errorf("OpenRouter LLM error: %s", msg)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("OpenRouter LLM returned an empty reply")
	}
	if strings.EqualFold(out.Choices[0].FinishReason, "content_filter") {
		return "", fmt.Errorf("OpenRouter LLM error: response blocked by content filter")
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("OpenRouter LLM returned an empty reply")
	}
	return content, nil
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.apiBase()+"/chat/completions", bytes.NewReader(body))
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
