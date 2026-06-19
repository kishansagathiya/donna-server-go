package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const openAIBase = "https://api.openai.com/v1"

type Embeddings struct {
	APIKey string
	Model  string
	Client *http.Client
}

func NewEmbeddings(apiKey, model string) *Embeddings {
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &Embeddings{
		APIKey: apiKey,
		Model:  model,
		Client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (e *Embeddings) Enabled() bool {
	return e != nil && e.APIKey != ""
}

func (e *Embeddings) headers() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+e.APIKey)
	h.Set("Content-Type", "application/json")
	return h
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed calls the OpenAI embeddings endpoint with up to 100 inputs per batch.
func (e *Embeddings) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if !e.Enabled() {
		return nil, fmt.Errorf("embeddings provider not configured")
	}
	if len(inputs) == 0 {
		return nil, nil
	}

	const batchSize = 100
	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += batchSize {
		end := start + batchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		batch := inputs[start:end]

		body, err := json.Marshal(map[string]any{
			"model":           e.Model,
			"input":           batch,
			"encoding_format": "float",
		})
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIBase+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header = e.headers()

		res, err := e.Client.Do(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			raw, _ := io.ReadAll(res.Body)
			res.Body.Close()
			return nil, fmt.Errorf("openai embeddings %d: %s", res.StatusCode, string(raw))
		}

		var resp embeddingResponse
		if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
			res.Body.Close()
			return nil, err
		}
		res.Body.Close()

		if len(resp.Data) != len(batch) {
			return nil, fmt.Errorf("embedding count mismatch: requested %d, got %d", len(batch), len(resp.Data))
		}
		for _, d := range resp.Data {
			out = append(out, d.Embedding)
		}
	}
	return out, nil
}

// EmbedOne embeds a single string.
func (e *Embeddings) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	out, err := e.Embed(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out[0], nil
}
