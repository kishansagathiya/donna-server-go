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
	APIKey    string
	Model     string
	MaxTokens int
	Client    *http.Client
}

func NewLLM(apiKey, model string, maxTokens int) *LLM {
	return &LLM{
		APIKey:    apiKey,
		Model:     model,
		MaxTokens: maxTokens,
		Client:    &http.Client{Timeout: 120 * time.Second},
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

func (l *LLM) chatRequestBody(messages []ChatMessage, stream bool, capOutput bool) ([]byte, error) {
	payload := map[string]any{
		"model":    l.Model,
		"messages": messages,
		"stream":   stream,
	}
	if capOutput && l.MaxTokens > 0 {
		payload["max_tokens"] = l.MaxTokens
	}
	return json.Marshal(payload)
}

func (l *LLM) StreamCompletion(ctx context.Context, messages []ChatMessage, onChunk func(string) error) error {
	body, err := l.chatRequestBody(messages, true, true)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterBase+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range l.headers() {
		req.Header.Set(k, v)
	}

	res, err := l.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("OpenRouter LLM %d: %s", res.StatusCode, string(b))
	}

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
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		text := chunk.Choices[0].Delta.Content
		if text != "" && onChunk != nil {
			if err := onChunk(text); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func (l *LLM) CompleteOnce(ctx context.Context, messages []ChatMessage) (string, error) {
	body, err := l.chatRequestBody(messages, false, false)
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
		return "", fmt.Errorf("OpenRouter LLM %d: %s", res.StatusCode, string(b))
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

func (l *LLM) CompleteOnceVision(ctx context.Context, prompt, imageDataURL string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model": l.Model,
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
