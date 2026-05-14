package cli

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/model"
	"github.com/CoreyRDean/intent/internal/model/llamafile"
	"github.com/CoreyRDean/intent/internal/model/mock"
	"github.com/CoreyRDean/intent/internal/verbose"
)

// buildBackend resolves the configured backend name to a model.Backend.
// The second return value is true only when the requested backend was
// unavailable and we silently fell back to the mock — callers use this to
// surface a per-invocation warning so users aren't left confused.
//
// In v1 we wire: mock, llamafile-local, llamafile-network, ollama (as a
// llamafile-shaped HTTP), openai (as a llamafile-shaped HTTP). The grammar
// constraint is the same across all of them; the only differences are the
// endpoint and the auth header.
func buildBackend(name string, cfg *config.Config, modelOverride string) (model.Backend, bool, error) {
	if v := os.Getenv("INTENT_FORCE_BACKEND"); v != "" {
		name = v
	}
	switch name {
	case "mock":
		return mock.New(), false, nil
	case "llamafile-local":
		// We expect the daemon (`intentd`) to have started llamafile on
		// the loopback host:port from config. If nothing's listening, we
		// fall back to the mock backend so `i hello` doesn't hard-fail
		// for a brand-new install — instead the mock returns an honest
		// "the local model isn't installed yet" response.
		host, port, err := resolveLocalDaemonEndpoint(cfg)
		if err != nil {
			return nil, false, err
		}
		endpoint := fmt.Sprintf("http://%s:%s", host, port)
		if !endpointReachable(endpoint) {
			return mock.New(), true, nil
		}
		b := llamafile.New(endpoint)
		if modelOverride != "" {
			b.ModelTag = modelOverride
		} else {
			b.ModelTag = cfg.Model
		}
		return b, false, nil
	case "llamafile-network":
		ep := os.Getenv("INTENT_LLAMAFILE_ENDPOINT")
		if ep == "" {
			ep = cfg.Raw["backends.llamafile-network.endpoint"]
		}
		if ep == "" {
			ep = "http://127.0.0.1:8080"
		}
		b := llamafile.New(ep)
		if modelOverride != "" {
			b.ModelTag = modelOverride
		}
		return b, false, nil
	case "ollama":
		ep := cfg.Raw["backends.ollama.endpoint"]
		if ep == "" {
			ep = "http://127.0.0.1:11434/v1"
		}
		b := llamafile.New(ep)
		if modelOverride != "" {
			b.ModelTag = modelOverride
		} else if v := cfg.Raw["backends.ollama.model"]; v != "" {
			b.ModelTag = v
		}
		return b, false, nil
	case "openai":
		ep := cfg.Raw["backends.openai.base_url"]
		if ep == "" {
			ep = "https://api.openai.com/v1"
		}
		b := llamafile.New(ep)
		if v := cfg.Raw["backends.openai.api_key_env"]; v != "" {
			b.APIKey = os.Getenv(v)
		} else {
			b.APIKey = os.Getenv("OPENAI_API_KEY")
		}
		if modelOverride != "" {
			b.ModelTag = modelOverride
		} else if v := cfg.Raw["backends.openai.model"]; v != "" {
			b.ModelTag = v
		} else {
			b.ModelTag = "gpt-4o-mini"
		}
		return b, false, nil
	default:
		return nil, false, fmt.Errorf("unknown backend: %q", name)
	}
}

// buildBackendCtx is the ctx-aware variant used by commands that want
// verbose logging to cover model I/O. It wraps the chosen backend in a
// verbose.Backend decorator if a verbose.Logger is present on ctx.
// The wrapper preserves StructuredBackend capability so `i report`'s
// capability type-assertion still succeeds through it.
func buildBackendCtx(ctx context.Context, name string, cfg *config.Config, modelOverride string) (model.Backend, bool, error) {
	be, fb, err := buildBackend(name, cfg, modelOverride)
	if err != nil {
		return be, fb, err
	}
	if l := verbose.FromContext(ctx); l.Enabled() {
		l.Section("backend resolved")
		l.KV("name", be.Name())
		if b, ok := be.(*llamafile.Backend); ok {
			l.KV("endpoint", b.Endpoint)
			l.KV("model_tag", b.ModelTag)
		}
		be = verbose.Backend(l, be)
	}
	return be, fb, nil
}

// printMockFallbackBanner writes a one-line stderr notice when the real backend
// was unavailable and we fell back to mock. Safe to call on every invocation —
// it is a no-op when isFallback is false.
func printMockFallbackBanner(isFallback bool) {
	if !isFallback {
		return
	}
	fmt.Fprintln(os.Stderr, "[MOCK] real backend unavailable — responses are simulated. Run 'i doctor', 'i model list', or 'i daemon start' to fix.")
}

// isMockBackend reports whether b is the mock backend (by name).
// Used by subcommands that cannot function usefully with mock output.
func isMockBackend(b model.Backend) bool {
	return b.Name() == "mock"
}

// endpointReachable does a short-timeout TCP check on the host:port of a URL.
func endpointReachable(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Host
	if host == "" {
		return false
	}
	if !strings.Contains(host, ":") {
		switch u.Scheme {
		case "https":
			host += ":443"
		default:
			host += ":80"
		}
	}
	c, err := net.DialTimeout("tcp", host, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
