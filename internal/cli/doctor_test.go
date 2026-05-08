package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/daemon"
	intentruntime "github.com/CoreyRDean/intent/internal/runtime"
	"github.com/CoreyRDean/intent/internal/state"
)

func TestResolveModelCheck(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *config.Config
		wantFile  string
		wantLabel string
	}{
		{
			name:      "nil config falls back to default",
			cfg:       nil,
			wantFile:  intentruntime.DefaultModel.File,
			wantLabel: "no model configured, checking default: " + intentruntime.DefaultModel.File,
		},
		{
			name:      "empty model name falls back to default",
			cfg:       &config.Config{Model: ""},
			wantFile:  intentruntime.DefaultModel.File,
			wantLabel: "no model configured, checking default: " + intentruntime.DefaultModel.File,
		},
		{
			name:      "default model name resolves to canonical file",
			cfg:       &config.Config{Model: intentruntime.DefaultModel.Name},
			wantFile:  intentruntime.DefaultModel.File,
			wantLabel: "checking: " + intentruntime.DefaultModel.File,
		},
		{
			name:      "non-default model name resolves to name+gguf",
			cfg:       &config.Config{Model: "qwen2.5-coder-1.5b-instruct-q4_k_m"},
			wantFile:  "qwen2.5-coder-1.5b-instruct-q4_k_m.gguf",
			wantLabel: "checking: qwen2.5-coder-1.5b-instruct-q4_k_m.gguf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFile, gotLabel := resolveModelCheck(tt.cfg)
			if gotFile != tt.wantFile {
				t.Errorf("file: got %q, want %q", gotFile, tt.wantFile)
			}
			if gotLabel != tt.wantLabel {
				t.Errorf("label: got %q, want %q", gotLabel, tt.wantLabel)
			}
		})
	}
}

type stubDaemonStatusClient struct {
	resp *daemon.Response
	err  error
}

func (s stubDaemonStatusClient) Call(_ daemon.Request) (*daemon.Response, error) {
	return s.resp, s.err
}

func TestDoctorDaemonStatus(t *testing.T) {
	origNewClient := newDaemonStatusClient
	origInstalled := daemonServiceInstalled
	t.Cleanup(func() {
		newDaemonStatusClient = origNewClient
		daemonServiceInstalled = origInstalled
	})

	dirs := state.Dirs{State: t.TempDir()}

	tests := []struct {
		name      string
		installed bool
		client    stubDaemonStatusClient
		want      string
		wantOK    bool
	}{
		{
			name:      "missing optional daemon is informational",
			installed: false,
			client:    stubDaemonStatusClient{err: errors.New("dial unix: no such file or directory")},
			want:      "not running (optional)",
			wantOK:    true,
		},
		{
			name:      "installed daemon that does not respond is unhealthy",
			installed: true,
			client:    stubDaemonStatusClient{err: errors.New("connection refused")},
			want:      "installed but not responding",
			wantOK:    false,
		},
		{
			name:      "running daemon reports endpoint",
			installed: false,
			client: stubDaemonStatusClient{resp: &daemon.Response{
				OK: true,
				Data: map[string]any{
					"llamafile_endpoint": "http://127.0.0.1:18080",
				},
			}},
			want:   "running (service installed: no, endpoint: http://127.0.0.1:18080)",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newDaemonStatusClient = func(string) daemonStatusCaller { return tt.client }
			daemonServiceInstalled = func(string) bool { return tt.installed }

			got, gotOK := doctorDaemonStatus(dirs)
			if got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tt.wantOK)
			}
		})
	}
}

func TestDoctorPrintsDaemonStatus(t *testing.T) {
	origNewClient := newDaemonStatusClient
	origInstalled := daemonServiceInstalled
	t.Cleanup(func() {
		newDaemonStatusClient = origNewClient
		daemonServiceInstalled = origInstalled
	})

	t.Setenv("HOME", t.TempDir())
	t.Setenv("INTENT_STATE_DIR", t.TempDir())
	t.Setenv("INTENT_CACHE_DIR", t.TempDir())

	newDaemonStatusClient = func(string) daemonStatusCaller {
		return stubDaemonStatusClient{err: errors.New("dial unix: no such file or directory")}
	}
	daemonServiceInstalled = func(string) bool { return false }

	out := captureStdout(func() {
		_ = cmdDoctor(context.Background(), nil)
	})
	if !strings.Contains(out, "daemon") {
		t.Fatalf("doctor output missing daemon line: %q", out)
	}
	if !strings.Contains(out, "not running (optional)") {
		t.Fatalf("doctor output missing optional daemon status: %q", out)
	}
}
