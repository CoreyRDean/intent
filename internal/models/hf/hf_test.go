package hf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Spin up a fake HF API for deterministic offline tests. We don't
// need to reproduce HF's full surface area — only the three endpoints
// our client hits.
func fakeHF(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	mux := http.NewServeMux()

	// Repo metadata: exists.
	mux.HandleFunc("/api/models/test/ok", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(RepoInfo{ID: "test/ok", Author: "test"})
	})
	// Repo metadata: gated.
	mux.HandleFunc("/api/models/test/gated", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"gated"}`))
	})
	// Repo metadata: missing.
	mux.HandleFunc("/api/models/test/missing", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Repository not found"}`))
	})
	// File tree with multiple quants.
	mux.HandleFunc("/api/models/test/ok/tree/main", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]File{
			{Type: "file", Path: "README.md", Size: 2048},
			{Type: "file", Path: "model-Q4_K_M.gguf", Size: 2 * 1024 * 1024 * 1024},
			{Type: "file", Path: "model-Q6_K.gguf", Size: 3 * 1024 * 1024 * 1024},
			{Type: "file", Path: "model-Q8_0.gguf", Size: 4 * 1024 * 1024 * 1024},
			{Type: "directory", Path: "subfolder"},
		})
	})
	// File tree with no ggufs: compatibility check must reject.
	mux.HandleFunc("/api/models/test/nogguf/tree/main", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]File{
			{Type: "file", Path: "config.json", Size: 256},
			{Type: "file", Path: "model.safetensors", Size: 123456},
		})
	})

	srv := httptest.NewServer(mux)
	prev := endpoint
	SetEndpoint(srv.URL)
	return srv, func() {
		srv.Close()
		SetEndpoint(prev)
	}
}

func TestGetRepoOK(t *testing.T) {
	_, cleanup := fakeHF(t)
	defer cleanup()
	c := New()
	info, err := c.GetRepo(context.Background(), "test/ok")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if info.ID != "test/ok" {
		t.Errorf("got ID %q", info.ID)
	}
}

func TestGetRepoMissing(t *testing.T) {
	_, cleanup := fakeHF(t)
	defer cleanup()
	c := New()
	if _, err := c.GetRepo(context.Background(), "test/missing"); err == nil {
		t.Error("GetRepo on 404 should error")
	}
}

func TestGetRepoGatedSurfacesFriendlyError(t *testing.T) {
	_, cleanup := fakeHF(t)
	defer cleanup()
	c := New()
	_, err := c.GetRepo(context.Background(), "test/gated")
	if err == nil {
		t.Fatal("GetRepo on 403 should error")
	}
	// Error should name the likely cause so the user knows what to do.
	msg := err.Error()
	for _, want := range []string{"gated", "token"} {
		if !containsCI(msg, want) {
			t.Errorf("error %q should mention %q to guide the user", msg, want)
		}
	}
}

func TestListFilesAndFindGGUF(t *testing.T) {
	_, cleanup := fakeHF(t)
	defer cleanup()
	c := New()
	files, err := c.ListFiles(context.Background(), "test/ok")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	ggufs := FindGGUF(files)
	if len(ggufs) != 3 {
		t.Fatalf("want 3 .gguf files, got %d (%+v)", len(ggufs), ggufs)
	}
}

func TestPickQuantByTag(t *testing.T) {
	_, cleanup := fakeHF(t)
	defer cleanup()
	c := New()
	files, _ := c.ListFiles(context.Background(), "test/ok")
	ggufs := FindGGUF(files)

	chose, err := PickQuant(ggufs, "Q6_K")
	if err != nil {
		t.Fatalf("PickQuant Q6_K: %v", err)
	}
	if chose.Path != "model-Q6_K.gguf" {
		t.Errorf("got %q", chose.Path)
	}

	// Asking for a quant that's not present must error with guidance.
	_, err = PickQuant(ggufs, "Q3_K_S")
	if err == nil {
		t.Error("missing quant should error")
	}

	// Empty tag prefers Q4_K_M.
	chose, err = PickQuant(ggufs, "")
	if err != nil {
		t.Fatalf("PickQuant default: %v", err)
	}
	if chose.Path != "model-Q4_K_M.gguf" {
		t.Errorf("default pick got %q, want Q4_K_M", chose.Path)
	}
}

func TestPickQuantNoGGUFs(t *testing.T) {
	_, err := PickQuant(nil, "")
	if err == nil {
		t.Error("empty file list should error")
	}
}

func TestMatchesQuantBoundaries(t *testing.T) {
	cases := []struct {
		filename string
		tag      string
		want     bool
	}{
		{"model-Q4_K_M.gguf", "Q4_K_M", true},
		{"model.Q4_K_M.gguf", "Q4_K_M", true},
		{"MODEL-q4_k_m.gguf", "Q4_K_M", true},
		{"model-Q4_K_M-extra.gguf", "Q4_K_M", true},
		{"model-Q4_K_M_extra.gguf", "Q4_K_M", false}, // would be a different quant
		{"prefixQ4_K_M.gguf", "Q4_K_M", false},       // no left boundary
		{"model-Q4_0.gguf", "Q4_K_M", false},
	}
	for _, c := range cases {
		got := matchesQuant(c.filename, c.tag)
		if got != c.want {
			t.Errorf("matchesQuant(%q, %q) = %v, want %v", c.filename, c.tag, got, c.want)
		}
	}
}

// End-to-end compatibility check via the public surface: a repo with
// no GGUF files must be reported as incompatible. This is the
// "compatibility check" the user asked for.
func TestCompatibilityRejectsNonGGUFRepo(t *testing.T) {
	_, cleanup := fakeHF(t)
	defer cleanup()
	c := New()
	files, err := c.ListFiles(context.Background(), "test/nogguf")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	ggufs := FindGGUF(files)
	if len(ggufs) != 0 {
		t.Errorf("expected zero ggufs, got %+v", ggufs)
	}
	if _, err := PickQuant(ggufs, ""); err == nil {
		t.Error("PickQuant on non-GGUF repo must error (that's how callers detect incompatibility)")
	}
}

// Case-insensitive substring. Intentionally small (no stdlib import
// in the main helper).
func containsCI(hay, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a := hay[i+j]
			b := needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
