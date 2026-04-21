package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/CoreyRDean/intent/internal/model"
)

type cacheIdentityBackend struct {
	name          string
	cacheIdentity string
}

type captureSystemPromptBackend struct {
	systemPrompt string
}

func (b *captureSystemPromptBackend) Name() string { return "capture" }

func (b *captureSystemPromptBackend) Available(context.Context) error { return nil }

func (b *captureSystemPromptBackend) Complete(_ context.Context, req model.CompleteRequest) (*model.Response, error) {
	if len(req.Messages) > 0 {
		b.systemPrompt = req.Messages[0].Content
	}
	return &model.Response{
		IntentSummary:   "List files in the current directory.",
		Approach:        model.ApproachCommand,
		Command:         "ls -la",
		Description:     "List files in the current working directory.",
		Risk:            model.RiskSafe,
		ExpectedRuntime: model.RuntimeInstant,
		Confidence:      model.ConfidenceHigh,
	}, nil
}

func (b *cacheIdentityBackend) Name() string { return b.name }

func (b *cacheIdentityBackend) Available(context.Context) error { return nil }

func (b *cacheIdentityBackend) Complete(context.Context, model.CompleteRequest) (*model.Response, error) {
	return &model.Response{
		IntentSummary:   "List files in the current directory.",
		Approach:        model.ApproachCommand,
		Command:         "ls -la",
		Description:     "List files in the current working directory.",
		Risk:            model.RiskSafe,
		ExpectedRuntime: model.RuntimeInstant,
		Confidence:      model.ConfidenceHigh,
	}, nil
}

func (b *cacheIdentityBackend) CacheIdentity() string { return b.cacheIdentity }

func TestRunCacheKeyUsesBackendCacheIdentity(t *testing.T) {
	eng := New(nil)
	ctx := context.Background()
	a, err := eng.Run(ctx, "list files", Options{
		Backend: &cacheIdentityBackend{
			name:          "llamafile",
			cacheIdentity: "llamafile|http://127.0.0.1:8080|qwen2.5-coder-3b",
		},
	})
	if err != nil {
		t.Fatalf("run a: %v", err)
	}
	b, err := eng.Run(ctx, "list files", Options{
		Backend: &cacheIdentityBackend{
			name:          "llamafile",
			cacheIdentity: "llamafile|http://127.0.0.1:11434/v1|gpt-4.1-mini",
		},
	})
	if err != nil {
		t.Fatalf("run b: %v", err)
	}
	if a.CacheKey == b.CacheKey {
		t.Fatalf("cache key collision across distinct backend identities: %q", a.CacheKey)
	}
}

func TestRunInjectsUserContextIntoSystemPrompt(t *testing.T) {
	eng := New(nil)
	be := &captureSystemPromptBackend{}
	_, err := eng.Run(context.Background(), "list files", Options{
		Backend:     be,
		UserContext: []string{"repo=core", "ticket=123"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(be.systemPrompt, "User-supplied context:") {
		t.Fatalf("system prompt missing user context section: %q", be.systemPrompt)
	}
	if !strings.Contains(be.systemPrompt, "repo=core") || !strings.Contains(be.systemPrompt, "ticket=123") {
		t.Fatalf("system prompt missing context values: %q", be.systemPrompt)
	}
}

func TestRunCacheKeyIncludesUserContext(t *testing.T) {
	eng := New(nil)
	ctx := context.Background()
	backend := &cacheIdentityBackend{
		name:          "llamafile",
		cacheIdentity: "llamafile|http://127.0.0.1:8080|qwen2.5-coder-3b",
	}

	base, err := eng.Run(ctx, "list files", Options{Backend: backend})
	if err != nil {
		t.Fatalf("run base: %v", err)
	}

	withCtx, err := eng.Run(ctx, "list files", Options{
		Backend:     backend,
		UserContext: []string{"env=production"},
	})
	if err != nil {
		t.Fatalf("run with context: %v", err)
	}
	if base.CacheKey == withCtx.CacheKey {
		t.Fatalf("cache key collision when --context was added: %q", base.CacheKey)
	}

	other, err := eng.Run(ctx, "list files", Options{
		Backend:     backend,
		UserContext: []string{"env=staging"},
	})
	if err != nil {
		t.Fatalf("run with other context: %v", err)
	}
	if withCtx.CacheKey == other.CacheKey {
		t.Fatalf("cache key collision across distinct --context values: %q", withCtx.CacheKey)
	}

	reordered, err := eng.Run(ctx, "list files", Options{
		Backend:     backend,
		UserContext: []string{"env=staging", "env=production"},
	})
	if err != nil {
		t.Fatalf("run reordered: %v", err)
	}
	forward, err := eng.Run(ctx, "list files", Options{
		Backend:     backend,
		UserContext: []string{"env=production", "env=staging"},
	})
	if err != nil {
		t.Fatalf("run forward: %v", err)
	}
	if reordered.CacheKey == forward.CacheKey {
		t.Fatalf("cache key ignored --context order: %q", forward.CacheKey)
	}
}
