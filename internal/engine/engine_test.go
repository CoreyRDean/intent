package engine

import (
	"context"
	"testing"

	"github.com/CoreyRDean/intent/internal/model"
)

type cacheIdentityBackend struct {
	name          string
	cacheIdentity string
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
