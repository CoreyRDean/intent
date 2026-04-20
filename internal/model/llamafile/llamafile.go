// Package llamafile speaks the OpenAI-compatible HTTP API exposed by
// llamafile (and llama.cpp's server). It can target either a locally managed
// llamafile process (via the runtime package) or a configured network endpoint.
package llamafile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/model"
)

// Backend talks to an OpenAI-compatible /v1/chat/completions endpoint.
type Backend struct {
	Endpoint string
	APIKey   string // optional; OpenAI-compatible endpoints may need this
	ModelTag string // optional; some servers ignore this and serve the loaded model
	Client   *http.Client
}

// New constructs a Backend with sensible defaults.
func New(endpoint string) *Backend {
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8080"
	}
	return &Backend{
		Endpoint: strings.TrimRight(endpoint, "/"),
		Client:   &http.Client{Timeout: 5 * time.Minute},
	}
}

func (b *Backend) Name() string { return "llamafile" }

// Available pings /health.
func (b *Backend) Available(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.Endpoint+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := b.Client.Do(req)
	if err != nil {
		return fmt.Errorf("backend unreachable at %s: %w", b.Endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("health %s", resp.Status)
	}
	return nil
}

type chatReq struct {
	Model          string         `json:"model,omitempty"`
	Messages       []model.Message `json:"messages"`
	Temperature    float64        `json:"temperature"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Seed           *int64         `json:"seed,omitempty"`
}

type responseFormat struct {
	Type   string          `json:"type"` // "json_object" | "json_schema"
	Schema json.RawMessage `json:"schema,omitempty"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete sends a chat request and parses the model's JSON response.
func (b *Backend) Complete(ctx context.Context, in model.CompleteRequest) (*model.Response, error) {
	reqBody := chatReq{
		Model:       b.ModelTag,
		Messages:    in.Messages,
		Temperature: in.Temperature,
		MaxTokens:   in.MaxTokens,
		Seed:        in.Seed,
		ResponseFormat: &responseFormat{
			Type:   "json_object",
			Schema: json.RawMessage(model.SchemaJSON),
		},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.Endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if b.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+b.APIKey)
	}
	resp, err := b.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call backend: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("backend %s: %s", resp.Status, truncate(string(raw), 400))
	}
	var cr chatResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("backend error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("backend returned no choices")
	}
	content := strings.TrimSpace(cr.Choices[0].Message.Content)
	content = stripFences(content)
	var out model.Response
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("model output not valid JSON: %w (got %q)", err, truncate(content, 200))
	}
	if err := out.Validate(); err != nil {
		return nil, fmt.Errorf("model response failed schema: %w", err)
	}
	return &out, nil
}

// stripFences tolerates a model that wraps JSON in ```json ... ``` fences,
// even though it was instructed not to.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
