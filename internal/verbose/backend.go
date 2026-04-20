package verbose

import (
	"context"
	"time"

	"github.com/CoreyRDean/intent/internal/model"
)

// Backend returns a model.Backend that logs every Complete and (if the
// inner supports it) CompleteStructured round-trip at verbose level.
// When l is nil or inner is nil this returns inner unchanged, so
// callers can wrap unconditionally.
//
// The wrapper preserves StructuredBackend capability: if inner
// implements model.StructuredBackend the returned value does too, so
// `i report` and any other caller that type-asserts for structured
// output still detects the capability through the wrapper.
func Backend(l *Logger, inner model.Backend) model.Backend {
	if l == nil || inner == nil {
		return inner
	}
	base := &vb{inner: inner, log: l}
	if sb, ok := inner.(model.StructuredBackend); ok {
		return &structuredVB{vb: base, sb: sb}
	}
	return base
}

type vb struct {
	inner model.Backend
	log   *Logger
}

func (v *vb) Name() string                        { return v.inner.Name() }
func (v *vb) Available(ctx context.Context) error { return v.inner.Available(ctx) }
func (v *vb) CacheIdentity() string {
	if inner, ok := v.inner.(model.CacheIdentityProvider); ok {
		return inner.CacheIdentity()
	}
	return v.inner.Name()
}

func (v *vb) Complete(ctx context.Context, req model.CompleteRequest) (*model.Response, error) {
	v.log.Section("model request (envelope)")
	v.log.KV("backend", v.inner.Name())
	v.log.KV("temperature", req.Temperature)
	v.log.KV("max_tokens", req.MaxTokens)
	if req.Seed != nil {
		v.log.KV("seed", *req.Seed)
	}
	if len(req.SchemaJSON) > 0 {
		v.log.RawBytes("schema", req.SchemaJSON)
	}
	if req.GrammarGBNF != "" {
		v.log.Printf("grammar gbnf (%d chars)", len(req.GrammarGBNF))
	}
	v.log.JSON("messages", req.Messages)

	t0 := time.Now()
	resp, err := v.inner.Complete(ctx, req)
	elapsed := time.Since(t0).Round(time.Millisecond)

	v.log.Section("model response (envelope)")
	v.log.KV("elapsed", elapsed)
	if err != nil {
		v.log.KV("error", err)
		return resp, err
	}
	v.log.JSON("response", resp)
	return resp, nil
}

// structuredVB extends vb with StructuredBackend support so the
// capability type-assertion still succeeds through the wrapper.
type structuredVB struct {
	*vb
	sb model.StructuredBackend
}

func (s *structuredVB) CompleteStructured(ctx context.Context, req model.StructuredRequest) ([]byte, error) {
	s.log.Section("model request (structured)")
	s.log.KV("backend", s.inner.Name())
	s.log.KV("temperature", req.Temperature)
	s.log.KV("max_tokens", req.MaxTokens)
	if req.Seed != nil {
		s.log.KV("seed", *req.Seed)
	}
	if len(req.SchemaJSON) > 0 {
		s.log.RawBytes("schema", req.SchemaJSON)
	}
	s.log.JSON("messages", req.Messages)

	t0 := time.Now()
	raw, err := s.sb.CompleteStructured(ctx, req)
	elapsed := time.Since(t0).Round(time.Millisecond)

	s.log.Section("model response (structured)")
	s.log.KV("elapsed", elapsed)
	if err != nil {
		s.log.KV("error", err)
		return raw, err
	}
	s.log.RawBytes("raw", raw)
	return raw, nil
}
