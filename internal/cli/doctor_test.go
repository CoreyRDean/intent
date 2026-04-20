package cli

import (
	"testing"

	"github.com/CoreyRDean/intent/internal/config"
	intentruntime "github.com/CoreyRDean/intent/internal/runtime"
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
