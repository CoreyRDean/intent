package models

import (
	"strings"
	"testing"
)

// Built-in catalog must be non-empty and each entry must carry the
// minimum fields the rest of the system relies on. Drift here
// (dropping a field during a refactor) would silently break every
// downstream caller, so we pin it down.
func TestBuiltInCatalogIsWellFormed(t *testing.T) {
	list := BuiltInList()
	if len(list) == 0 {
		t.Fatal("built-in catalog is empty")
	}
	seen := map[string]bool{}
	for _, m := range list {
		if m.ID == "" {
			t.Errorf("entry missing ID: %+v", m)
		}
		if seen[m.ID] {
			t.Errorf("duplicate ID %q in catalog", m.ID)
		}
		seen[m.ID] = true
		if m.Repo == "" {
			t.Errorf("%s: missing Repo", m.ID)
		}
		if m.File == "" {
			t.Errorf("%s: missing File", m.ID)
		}
		if !strings.HasSuffix(strings.ToLower(m.File), ".gguf") {
			t.Errorf("%s: File %q should end in .gguf", m.ID, m.File)
		}
		if !ValidGGUFQuant(m.Quant) {
			t.Errorf("%s: quant %q not recognised", m.ID, m.Quant)
		}
		if m.SizeMB <= 0 {
			t.Errorf("%s: SizeMB should be positive", m.ID)
		}
		if !m.BuiltIn {
			t.Errorf("%s: BuiltIn flag should be true for catalog entries", m.ID)
		}
	}
	// DefaultID must resolve against the catalog — otherwise the
	// "just works" experience is broken out of the box.
	if !seen[DefaultID] {
		t.Errorf("DefaultID %q not in built-in catalog", DefaultID)
	}
}

// Resolve must understand the three forms documented in its doc
// comment. Failure here means a user typing a perfectly valid HF
// reference gets an "unknown model" error.
func TestCatalogResolve(t *testing.T) {
	cat := New(nil)

	t.Run("catalog id", func(t *testing.T) {
		m, err := cat.Resolve("qwen2.5-coder-3b")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.ID != "qwen2.5-coder-3b" {
			t.Errorf("got ID %q, want %q", m.ID, "qwen2.5-coder-3b")
		}
		if m.File == "" {
			t.Error("catalog entry should have File populated")
		}
	})

	t.Run("hf repo without quant", func(t *testing.T) {
		m, err := cat.Resolve("bartowski/Phi-3.5-mini-instruct-GGUF")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Repo != "bartowski/Phi-3.5-mini-instruct-GGUF" {
			t.Errorf("got Repo %q", m.Repo)
		}
		if m.File != "" {
			t.Error("HF-only ref should have empty File (resolved via HF probe)")
		}
	})

	t.Run("hf repo with quant", func(t *testing.T) {
		m, err := cat.Resolve("bartowski/Phi-3.5-mini-instruct-GGUF:Q6_K")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Quant != "Q6_K" {
			t.Errorf("got Quant %q, want Q6_K", m.Quant)
		}
		if !strings.Contains(m.ID, "q6_k") {
			t.Errorf("ID %q should include quant suffix", m.ID)
		}
	})

	t.Run("unknown short tag", func(t *testing.T) {
		_, err := cat.Resolve("not-a-real-model")
		if err == nil {
			t.Fatal("expected error for unknown short tag")
		}
	})

	t.Run("empty ref", func(t *testing.T) {
		_, err := cat.Resolve("")
		if err == nil {
			t.Fatal("expected error for empty ref")
		}
	})
}

// Legacy configs stored the GGUF filename stem in Config.Model
// (e.g. "qwen2.5-coder-7b-instruct-q4_k_m"). Catalog.Get must
// recognise those so upgrades don't require manual migration.
func TestCatalogGetBackwardCompat(t *testing.T) {
	cat := New(nil)
	legacyStems := []struct {
		in   string
		want string // expected catalog ID
	}{
		{"qwen2.5-coder-7b-instruct-q4_k_m", "qwen2.5-coder-7b"},
		{"qwen2.5-coder-3b-instruct-q4_k_m.gguf", "qwen2.5-coder-3b"},
		{"qwen2.5-coder-1.5b-instruct-q4_k_m", "qwen2.5-coder-1.5b"},
	}
	for _, c := range legacyStems {
		m := cat.Get(c.in)
		if m == nil {
			t.Errorf("Get(%q): got nil, want catalog hit on %q", c.in, c.want)
			continue
		}
		if m.ID != c.want {
			t.Errorf("Get(%q): got ID %q, want %q", c.in, m.ID, c.want)
		}
	}
}

// Custom models must override same-ID built-ins. Use case: a user
// wants to pin a different quant of an existing catalog entry.
func TestCatalogCustomOverridesBuiltIn(t *testing.T) {
	custom := []Model{{
		ID:    "qwen2.5-coder-3b",
		File:  "qwen2.5-coder-3b-instruct-q6_k.gguf",
		Repo:  "Qwen/Qwen2.5-Coder-3B-Instruct-GGUF",
		Quant: "Q6_K",
	}}
	cat := New(custom)
	m := cat.Get("qwen2.5-coder-3b")
	if m == nil {
		t.Fatal("expected override to be findable")
	}
	if m.Quant != "Q6_K" {
		t.Errorf("got Quant %q, want Q6_K (override should win)", m.Quant)
	}
	if m.BuiltIn {
		t.Error("overridden entry should report BuiltIn=false")
	}
}

func TestValidGGUFQuant(t *testing.T) {
	cases := []struct {
		q  string
		ok bool
	}{
		{"Q4_K_M", true},
		{"q4_k_m", true}, // case-insensitive
		{"Q8_0", true},
		{"IQ2_XXS", true},
		{"F16", true},
		{"BF16", true},
		{"BOGUS", false},
		{"", false},
		{"GPTQ", false},
	}
	for _, c := range cases {
		got := ValidGGUFQuant(c.q)
		if got != c.ok {
			t.Errorf("ValidGGUFQuant(%q) = %v, want %v", c.q, got, c.ok)
		}
	}
}

func TestSplitRepoQuant(t *testing.T) {
	cases := []struct {
		in        string
		wantRepo  string
		wantQuant string
	}{
		{"foo/bar", "foo/bar", ""},
		{"foo/bar:Q4_K_M", "foo/bar", "Q4_K_M"},
		{"bartowski/Name-Q6_K-thing-GGUF", "bartowski/Name-Q6_K-thing-GGUF", ""},
		{"bartowski/Name-Q6_K-thing-GGUF:Q8_0", "bartowski/Name-Q6_K-thing-GGUF", "Q8_0"},
	}
	for _, c := range cases {
		r, q := splitRepoQuant(c.in)
		if r != c.wantRepo || q != c.wantQuant {
			t.Errorf("splitRepoQuant(%q) = (%q,%q), want (%q,%q)",
				c.in, r, q, c.wantRepo, c.wantQuant)
		}
	}
}

func TestHFToID(t *testing.T) {
	cases := []struct {
		repo, quant, want string
	}{
		{"bartowski/Phi-3.5-mini-instruct-GGUF", "", "bartowski_phi-3.5-mini-instruct-gguf"},
		{"bartowski/Phi-3.5-mini-instruct-GGUF", "Q6_K", "bartowski_phi-3.5-mini-instruct-gguf-q6_k"},
	}
	for _, c := range cases {
		got := HFToID(c.repo, c.quant)
		if got != c.want {
			t.Errorf("HFToID(%q,%q) = %q, want %q", c.repo, c.quant, got, c.want)
		}
	}
}

// Default must always return a non-nil model if the catalog has any
// entries — callers rely on this to avoid nil panics.
func TestCatalogDefault(t *testing.T) {
	cat := New(nil)
	if cat.Default() == nil {
		t.Fatal("Default returned nil on populated catalog")
	}
	// Even with a corrupt DefaultID the fallback should kick in.
	cat2 := New([]Model{{ID: "only-one"}})
	// DefaultID may or may not match; either way Default should
	// never return nil when there are entries.
	if cat2.Default() == nil {
		t.Fatal("Default returned nil on non-empty catalog")
	}
}

// Sorted list must group by family and ascend by params within a
// family. Purely UX but worth pinning: a shuffled list in
// `i model list` is disorienting.
func TestCatalogAllOrdering(t *testing.T) {
	cat := New(nil)
	all := cat.All()
	for i := 1; i < len(all); i++ {
		prev, cur := all[i-1], all[i]
		if prev.Family > cur.Family {
			t.Errorf("family out of order: %q before %q", prev.Family, cur.Family)
		}
		if prev.Family == cur.Family && paramsMB(prev.Params) > paramsMB(cur.Params) {
			t.Errorf("within %s: %q (%s) came before %q (%s)",
				prev.Family, prev.ID, prev.Params, cur.ID, cur.Params)
		}
	}
}
