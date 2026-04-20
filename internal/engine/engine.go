// Package engine drives one full intent turn:
//
//   gather context → call model → run tool calls (loop) → safety guard →
//   cache lookup → return final Response to the CLI for confirm/exec.
//
// The engine is backend-agnostic and mode-agnostic. It does not render UI or
// execute the final command — that is the CLI's job.
package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/CoreyRDean/intent/internal/cache"
	"github.com/CoreyRDean/intent/internal/contextpack"
	"github.com/CoreyRDean/intent/internal/model"
	"github.com/CoreyRDean/intent/internal/safety"
	"github.com/CoreyRDean/intent/internal/tools"
)

// Options control one turn.
type Options struct {
	Backend       model.Backend
	MaxToolSteps  int
	UseCache      bool
	WriteCache    bool
	UserContext   []string // --context flags
	ProjectRC     string
	OnPhase       func(phase string)        // for spinner labels
	OnToolCall    func(name string, args []byte) // for visibility
}

// Result is what the engine returns.
type Result struct {
	Response     *model.Response
	GuardResult  safety.Result
	CacheHit     bool
	CacheKey     string
	ContextPack  contextpack.Pack
	ToolStepsUsed int
}

// Engine wires backend + cache for repeated invocations.
type Engine struct {
	cache *cache.Store
}

func New(c *cache.Store) *Engine { return &Engine{cache: c} }

// Run executes one turn for the given user prompt.
func (e *Engine) Run(ctx context.Context, prompt string, opts Options) (*Result, error) {
	if opts.Backend == nil {
		return nil, fmt.Errorf("engine: nil backend")
	}
	if opts.MaxToolSteps <= 0 {
		opts.MaxToolSteps = 5
	}

	pack := contextpack.Gather(ctx)
	res := &Result{ContextPack: pack}

	// Cache key.
	key := cache.Key(cache.KeyInputs{
		Prompt:                prompt,
		CwdFingerprint:        cache.CwdFingerprint(pack.Cwd, ""),
		OS:                    pack.OS,
		BinariesFingerprint:   cache.BinariesFingerprint(pack.AvailableBins),
		ModelName:             opts.Backend.Name(),
		PromptTemplateVersion: model.PromptTemplateVersion,
	})
	res.CacheKey = key

	if e.cache != nil && opts.UseCache {
		if entry := e.cache.Get(key); entry != nil && entry.Response != nil {
			// Re-run the static guard on the cached response in case patterns
			// have been updated since it was stored.
			cp := *entry.Response
			gr := safety.Apply(&cp)
			if !gr.HardReject {
				res.Response = &cp
				res.GuardResult = gr
				res.CacheHit = true
				return res, nil
			}
		}
	}

	sysPrompt := model.BuildSystemPrompt(pack.AsPromptInputs(opts.ProjectRC, opts.UserContext))
	msgs := []model.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: prompt},
	}

	if opts.OnPhase != nil {
		opts.OnPhase("Understanding...")
	}

	for step := 0; step < opts.MaxToolSteps+1; step++ {
		req := model.CompleteRequest{
			Messages:    msgs,
			SchemaJSON:  []byte(model.SchemaJSON),
			Temperature: 0.2,
			MaxTokens:   2048,
		}
		resp, err := opts.Backend.Complete(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("backend: %w", err)
		}
		if err := resp.Validate(); err != nil {
			return nil, fmt.Errorf("invalid model response: %w", err)
		}

		if resp.Approach != model.ApproachToolCall {
			gr := safety.Apply(resp)
			res.Response = resp
			res.GuardResult = gr
			res.ToolStepsUsed = step
			if e.cache != nil && opts.WriteCache && eligibleForCache(resp) {
				_ = e.cache.Put(&cache.Entry{
					Key:      key,
					Prompt:   prompt,
					Response: resp,
				})
			}
			return res, nil
		}

		// approach == tool_call: run the tool, append result, loop.
		if step == opts.MaxToolSteps {
			refused := &model.Response{
				IntentSummary: "Refused: exceeded tool-call budget.",
				Approach:      model.ApproachRefuse,
				RefusalReason: fmt.Sprintf("model exceeded max_tool_steps (%d) without producing a final answer", opts.MaxToolSteps),
			}
			res.Response = refused
			res.ToolStepsUsed = step
			return res, nil
		}

		if opts.OnPhase != nil {
			opts.OnPhase(fmt.Sprintf("Reading context (%s)...", resp.ToolCall.Name))
		}
		if opts.OnToolCall != nil {
			opts.OnToolCall(resp.ToolCall.Name, resp.ToolCall.Arguments)
		}
		out, err := tools.Run(ctx, resp.ToolCall.Name, resp.ToolCall.Arguments)
		if err != nil {
			out = tools.Result{"error": err.Error()}
		}
		toolResultJSON, _ := json.Marshal(out)
		msgs = append(msgs, model.Message{
			Role:    "assistant",
			Content: jsonMust(resp),
		})
		msgs = append(msgs, model.Message{
			Role:    "tool",
			Name:    resp.ToolCall.Name,
			Content: string(toolResultJSON),
		})
	}

	return nil, fmt.Errorf("engine: exhausted tool-call loop without returning")
}

func eligibleForCache(r *model.Response) bool {
	if r == nil {
		return false
	}
	switch r.Approach {
	case model.ApproachCommand, model.ApproachScript, model.ApproachInform:
		return r.Confidence != model.ConfidenceLow
	}
	return false
}

func jsonMust(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
