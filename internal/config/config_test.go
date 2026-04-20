package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadSupportsSectionedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
backend = "openai"
model = "gpt-test"
timeout = "90s"

[backends.openai]
base_url = "https://example.test/v1"
model = "gpt-4.1-mini"

[daemon]
host = "127.0.0.1"
port = "18080"

[cache]
enabled = true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := read(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if cfg.Backend != "openai" {
		t.Fatalf("backend=%q want %q", cfg.Backend, "openai")
	}
	if cfg.Model != "gpt-test" {
		t.Fatalf("model=%q want %q", cfg.Model, "gpt-test")
	}
	if cfg.Timeout != 90*time.Second {
		t.Fatalf("timeout=%s want %s", cfg.Timeout, 90*time.Second)
	}
	if got := cfg.Raw["backends.openai.base_url"]; got != "https://example.test/v1" {
		t.Fatalf("backends.openai.base_url=%q want %q", got, "https://example.test/v1")
	}
	if got := cfg.Raw["backends.openai.model"]; got != "gpt-4.1-mini" {
		t.Fatalf("backends.openai.model=%q want %q", got, "gpt-4.1-mini")
	}
	if got := cfg.Raw["daemon.host"]; got != "127.0.0.1" {
		t.Fatalf("daemon.host=%q want %q", got, "127.0.0.1")
	}
	if got := cfg.Raw["daemon.port"]; got != "18080" {
		t.Fatalf("daemon.port=%q want %q", got, "18080")
	}
}
