package cli

import (
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
)

// buildBackend resolves the configured backend name to a model.Backend.
//
// In v1 we wire: mock, llamafile-local, llamafile-network, ollama (as a
// llamafile-shaped HTTP), openai (as a llamafile-shaped HTTP). The grammar
// constraint is the same across all of them; the only differences are the
// endpoint and the auth header.
func buildBackend(name string, cfg *config.Config, modelOverride string) (model.Backend, error) {
	if v := os.Getenv("INTENT_FORCE_BACKEND"); v != "" {
		name = v
	}
	switch name {
	case "mock":
		return mock.New(), nil
	case "llamafile-local":
		// In v1 we expect the daemon to have started llamafile and we just
		// dial 127.0.0.1:8080. If the daemon isn't running and the user has
		// disabled it, we fall back to mock so `i hello` doesn't hard-fail
		// for a brand-new install — it still produces a useful, honest
		// answer that the local model isn't installed.
		if !endpointReachable("http://127.0.0.1:8080") {
			fmt.Fprintln(os.Stderr, "intent: local llamafile not reachable; falling back to mock backend")
			fmt.Fprintln(os.Stderr, "  run `i daemon start` (after `i model pull`) for the real thing")
			return mock.New(), nil
		}
		b := llamafile.New("http://127.0.0.1:8080")
		if modelOverride != "" {
			b.ModelTag = modelOverride
		} else {
			b.ModelTag = cfg.Model
		}
		return b, nil
	case "llamafile-network":
		ep := cfg.Raw["backends.llamafile-network.endpoint"]
		if ep == "" {
			ep = "http://127.0.0.1:8080"
		}
		b := llamafile.New(ep)
		if modelOverride != "" {
			b.ModelTag = modelOverride
		}
		return b, nil
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
		return b, nil
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
		return b, nil
	default:
		return nil, fmt.Errorf("unknown backend: %q", name)
	}
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
