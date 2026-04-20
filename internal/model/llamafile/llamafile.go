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
	Model          string          `json:"model,omitempty"`
	Messages       []model.Message `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Seed           *int64          `json:"seed,omitempty"`
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

// Complete sends a chat request constrained to the standard Response
// envelope and parses the model's JSON response.
func (b *Backend) Complete(ctx context.Context, in model.CompleteRequest) (*model.Response, error) {
	content, err := b.chatJSON(ctx, in.Messages, []byte(model.SchemaJSON), in.Temperature, in.MaxTokens, in.Seed)
	if err != nil {
		return nil, err
	}
	content = stripFences(strings.TrimSpace(content))
	var out model.Response
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("model output not valid JSON: %w (got %q)", err, truncate(content, 200))
	}
	backfillRequiredFields(&out)
	if err := out.Validate(); err != nil {
		return nil, fmt.Errorf("model response failed schema: %w (got %q)", err, truncate(content, 400))
	}
	return &out, nil
}

// CompleteStructured implements model.StructuredBackend. The caller-
// supplied schema is enforced by llamafile's response_format.schema
// grammar, so the returned bytes are already schema-valid JSON.
func (b *Backend) CompleteStructured(ctx context.Context, in model.StructuredRequest) ([]byte, error) {
	if len(in.SchemaJSON) == 0 {
		return nil, fmt.Errorf("CompleteStructured: SchemaJSON is required")
	}
	content, err := b.chatJSON(ctx, in.Messages, in.SchemaJSON, in.Temperature, in.MaxTokens, in.Seed)
	if err != nil {
		return nil, err
	}
	content = stripFences(strings.TrimSpace(content))
	// Validate it parses as SOME JSON before returning so callers get a
	// clear error before they try to unmarshal into their own type.
	var any json.RawMessage
	if err := json.Unmarshal([]byte(content), &any); err != nil {
		return nil, fmt.Errorf("structured output not valid JSON: %w (got %q)", err, truncate(content, 200))
	}
	return []byte(content), nil
}

// chatJSON is the shared HTTP round-trip. Temperature/max_tokens/seed
// are passed through verbatim so the caller controls determinism.
func (b *Backend) chatJSON(ctx context.Context, messages []model.Message, schema []byte, temp float64, maxTok int, seed *int64) (string, error) {
	reqBody := chatReq{
		Model:       b.ModelTag,
		Messages:    messages,
		Temperature: temp,
		MaxTokens:   maxTok,
		Seed:        seed,
		ResponseFormat: &responseFormat{
			Type:   "json_object",
			Schema: json.RawMessage(schema),
		},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.Endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if b.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+b.APIKey)
	}
	resp, err := b.Client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call backend: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("backend %s: %s", resp.Status, truncate(string(raw), 400))
	}
	var cr chatResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("backend error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("backend returned no choices")
	}
	return cr.Choices[0].Message.Content, nil
}

// backfillRequiredFields supplies sane defaults for fields small local
// models routinely omit despite the schema. We never silently invent the
// command itself (that would defeat the whole point); we only fill in
// metadata the safety guard and TUI need to present the proposal.
func backfillRequiredFields(r *model.Response) {
	if r == nil {
		return
	}
	if r.Description == "" {
		switch {
		case r.Command != "":
			r.Description = "Run: " + truncate(r.Command, 120)
		case r.Script != nil && r.Script.Body != "":
			first := strings.SplitN(r.Script.Body, "\n", 2)[0]
			r.Description = "Run script (" + r.Script.Interpreter + "): " + truncate(first, 100)
		case r.StdoutToUser != "":
			r.Description = "Print informational answer."
		case r.ToolCall != nil && r.ToolCall.Name != "":
			r.Description = "Gather context via " + r.ToolCall.Name + "."
		case r.ClarifyingQuestion != "":
			r.Description = "Ask the user a clarifying question."
		case r.RefusalReason != "":
			r.Description = "Refuse this request."
		default:
			r.Description = "(no description provided by model)"
		}
	}
	if r.Risk == "" {
		// safe is the most conservative default for risk classification: the
		// static safety guard will bump it as needed (e.g. detecting `rm -rf`
		// or `sudo`). Marking unknown as `safe` only matters when the guard
		// agrees, by definition.
		r.Risk = model.RiskSafe
	}
	if r.Approach == "" {
		// As a last resort, infer from filled fields. Prefer the most
		// specific.
		switch {
		case r.Script != nil && r.Script.Body != "":
			r.Approach = model.ApproachScript
		case r.Command != "":
			r.Approach = model.ApproachCommand
		case r.ToolCall != nil && r.ToolCall.Name != "":
			r.Approach = model.ApproachToolCall
		case r.StdoutToUser != "":
			r.Approach = model.ApproachInform
		case r.ClarifyingQuestion != "":
			r.Approach = model.ApproachClarify
		case r.RefusalReason != "":
			r.Approach = model.ApproachRefuse
		}
	}
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
