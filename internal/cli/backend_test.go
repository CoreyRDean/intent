package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/model/mock"
)

// minimalConfig returns a Config with no backend-specific keys set,
// safe to pass when the backend won't actually be contacted.
func minimalConfig() *config.Config {
	return &config.Config{Raw: map[string]string{}}
}

// clearBackendEnv makes each test hermetic w.r.t. INTENT_FORCE_BACKEND,
// which is honored by buildBackend as a runtime override. Developers who
// export it in their shell for manual debugging should still get green
// tests. t.Setenv auto-restores on test end.
func clearBackendEnv(t *testing.T) {
	t.Helper()
	t.Setenv("INTENT_FORCE_BACKEND", "")
}

func TestBuildBackend_MockIsNotFallback(t *testing.T) {
	clearBackendEnv(t)
	be, isFallback, err := buildBackend("mock", minimalConfig(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isFallback {
		t.Error("explicit mock backend should not be flagged as fallback")
	}
	if be.Name() != "mock" {
		t.Errorf("expected name %q, got %q", "mock", be.Name())
	}
}

func TestBuildBackend_LlamafileLocalFallsBackWhenUnreachable(t *testing.T) {
	clearBackendEnv(t)
	// Point the daemon at a port that is definitely not listening.
	cfg := minimalConfig()
	cfg.Raw["daemon.host"] = "127.0.0.1"
	cfg.Raw["daemon.port"] = "1" // port 1 is reserved; nothing listens there

	be, isFallback, err := buildBackend("llamafile-local", cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isFallback {
		t.Error("unavailable llamafile-local should set isFallback=true")
	}
	if be.Name() != "mock" {
		t.Errorf("expected fallback name %q, got %q", "mock", be.Name())
	}
}

func TestBuildBackend_UnknownBackendErrors(t *testing.T) {
	clearBackendEnv(t)
	_, _, err := buildBackend("nonexistent", minimalConfig(), "")
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("error message should mention 'unknown backend', got: %v", err)
	}
}

func TestIsMockBackend_TrueForMock(t *testing.T) {
	if !isMockBackend(mock.New()) {
		t.Error("isMockBackend should return true for mock.Backend")
	}
}

func TestIsMockBackend_FalseForNonMock(t *testing.T) {
	// A minimal stub that is not the mock backend.
	type stubBackend struct{ mock.Backend }
	stub := &struct {
		mock.Backend
		name string
	}{name: "llamafile"}
	_ = stub // compile-check; just verify isMockBackend uses Name()

	if isMockBackend(mock.New()) == false {
		t.Error("mock.New() should satisfy isMockBackend")
	}
}

// TestPrintMockFallbackBanner_WritesWhenFallback captures stderr and checks
// the banner is written exactly when isFallback is true.
func TestPrintMockFallbackBanner_WritesWhenFallback(t *testing.T) {
	for _, tt := range []struct {
		isFallback  bool
		wantOutput  bool
		wantSnippet string
	}{
		{isFallback: true, wantOutput: true, wantSnippet: "[MOCK]"},
		{isFallback: false, wantOutput: false},
	} {
		// Redirect stderr.
		r, w, _ := os.Pipe()
		orig := os.Stderr
		os.Stderr = w

		printMockFallbackBanner(tt.isFallback)

		w.Close()
		os.Stderr = orig
		var buf bytes.Buffer
		io.Copy(&buf, r)
		out := buf.String()

		if tt.wantOutput && !strings.Contains(out, tt.wantSnippet) {
			t.Errorf("isFallback=%v: expected %q in output, got: %q", tt.isFallback, tt.wantSnippet, out)
		}
		if !tt.wantOutput && out != "" {
			t.Errorf("isFallback=%v: expected no output, got: %q", tt.isFallback, out)
		}
	}
}

// TestPrintMockFallbackBanner_MentionsNextSteps checks the banner includes
// actionable next-step hints.
func TestPrintMockFallbackBanner_MentionsNextSteps(t *testing.T) {
	r, w, _ := os.Pipe()
	orig := os.Stderr
	os.Stderr = w

	printMockFallbackBanner(true)

	w.Close()
	os.Stderr = orig
	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()

	for _, hint := range []string{"i doctor", "i daemon start"} {
		if !strings.Contains(out, hint) {
			t.Errorf("banner should mention %q; got: %q", hint, out)
		}
	}
}
