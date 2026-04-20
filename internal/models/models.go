// Package models is the source of truth for which LLMs intent can
// run. It holds a curated catalog of well-tested GGUF models, plus
// support for custom user-added Hugging Face models.
//
// Design goals:
//   - The catalog is data, not logic. A `Model` struct is enough to
//     find, download, and load any supported model.
//   - Short, memorable IDs ("qwen2.5-coder-3b") for the happy path;
//     full HF repo IDs (with optional quant selectors) for anything
//     else.
//   - Custom models are persisted alongside the main config so they
//     survive restarts. The catalog transparently merges built-in +
//     custom entries.
//   - The single "current" model is a reference to a catalog ID,
//     stored in Config.Model. Switching is just rewriting that field.
//
// Non-goals (yet):
//   - VRAM / GPU compatibility analysis.
//   - Benchmark-driven quality/speed scoring.
//   - Multi-arch support (only GGUF, which is what llamafile loads).
package models

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Model is a fully-specified LLM entry the rest of the system can
// act on. Everything needed to download it, load it in the daemon,
// and describe it to the user lives here.
type Model struct {
	// ID is the stable short identifier ("qwen2.5-coder-3b"). Stored
	// in Config.Model and used by `i model use <ID>`.
	ID string `json:"id"`

	// DisplayName is the human-facing name shown in `i model list`.
	DisplayName string `json:"display_name"`

	// Family / author ("Qwen", "Meta", "Microsoft", ...).
	Family string `json:"family"`

	// Params reports parameter count ("1.5B", "7B"). Purely cosmetic.
	Params string `json:"params"`

	// Quant is the quantisation level ("Q4_K_M", "Q6_K"). Affects
	// size on disk and quality. Q4_K_M is the recommended default
	// balance for llama.cpp-style runtimes.
	Quant string `json:"quant"`

	// Repo is the Hugging Face repo ID ("Qwen/Qwen2.5-Coder-3B-Instruct-GGUF").
	Repo string `json:"repo"`

	// File is the GGUF filename inside Repo.
	File string `json:"file"`

	// SizeMB is approximate download size (used for progress UX).
	SizeMB int `json:"size_mb"`

	// ContextTokens is the model's advertised context window.
	ContextTokens int `json:"context_tokens,omitempty"`

	// Tagline is a one-line description for `i model list`.
	Tagline string `json:"tagline,omitempty"`

	// BuiltIn is true when the entry came from the curated catalog
	// (not user-added). Transient; not serialised.
	BuiltIn bool `json:"-"`
}

// HFRef returns the canonical "repo/path" reference for the model.
// Useful for logs and `i model show`.
func (m *Model) HFRef() string {
	if m.Repo == "" {
		return ""
	}
	return m.Repo + ":" + m.Quant
}

// DownloadURL returns the HF "resolve" URL for the model's GGUF file.
// Anonymous access works for any public repo.
func (m *Model) DownloadURL() string {
	if m.Repo == "" || m.File == "" {
		return ""
	}
	return fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s?download=true", m.Repo, m.File)
}

// BuiltIn is the curated catalog. Every entry is tested to load in
// llamafile 0.10.x on both arm64 and x86_64 macOS + Linux. When
// adding an entry:
//   - Choose Q4_K_M unless there's a specific reason not to (universal
//     quality/size sweet spot for llama.cpp).
//   - Verify the GGUF filename exactly matches what HF serves (case-
//     sensitive; `bartowski` uses CamelCase, `Qwen` uses lowercase).
//   - Fill in SizeMB from the actual file size on HF, rounded to ~100 MB
//     granularity so the progress bar total is accurate.
//   - Keep Tagline under ~60 chars so `i model list` formats cleanly.
var builtIn = []Model{
	{
		ID:            "qwen2.5-coder-1.5b",
		DisplayName:   "Qwen2.5-Coder 1.5B Instruct",
		Family:        "Qwen",
		Params:        "1.5B",
		Quant:         "Q4_K_M",
		Repo:          "Qwen/Qwen2.5-Coder-1.5B-Instruct-GGUF",
		File:          "qwen2.5-coder-1.5b-instruct-q4_k_m.gguf",
		SizeMB:        1100,
		ContextTokens: 32768,
		Tagline:       "Tiny and fast. Good for structured output, weak on nuance.",
	},
	{
		ID:            "qwen2.5-coder-3b",
		DisplayName:   "Qwen2.5-Coder 3B Instruct",
		Family:        "Qwen",
		Params:        "3B",
		Quant:         "Q4_K_M",
		Repo:          "Qwen/Qwen2.5-Coder-3B-Instruct-GGUF",
		File:          "qwen2.5-coder-3b-instruct-q4_k_m.gguf",
		SizeMB:        2000,
		ContextTokens: 32768,
		Tagline:       "Balanced default. Best daily driver for most machines.",
	},
	{
		ID:            "qwen2.5-coder-7b",
		DisplayName:   "Qwen2.5-Coder 7B Instruct",
		Family:        "Qwen",
		Params:        "7B",
		Quant:         "Q4_K_M",
		Repo:          "Qwen/Qwen2.5-Coder-7B-Instruct-GGUF",
		File:          "qwen2.5-coder-7b-instruct-q4_k_m.gguf",
		SizeMB:        4700,
		ContextTokens: 32768,
		Tagline:       "Heavier, meaningfully better at nuanced intent parsing.",
	},
	{
		ID:            "llama-3.2-3b",
		DisplayName:   "Llama 3.2 3B Instruct",
		Family:        "Meta",
		Params:        "3B",
		Quant:         "Q4_K_M",
		Repo:          "bartowski/Llama-3.2-3B-Instruct-GGUF",
		File:          "Llama-3.2-3B-Instruct-Q4_K_M.gguf",
		SizeMB:        2000,
		ContextTokens: 131072,
		Tagline:       "Meta generalist. Huge context window, solid reasoning.",
	},
	{
		ID:            "llama-3.1-8b",
		DisplayName:   "Llama 3.1 8B Instruct",
		Family:        "Meta",
		Params:        "8B",
		Quant:         "Q4_K_M",
		Repo:          "bartowski/Meta-Llama-3.1-8B-Instruct-GGUF",
		File:          "Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf",
		SizeMB:        4900,
		ContextTokens: 131072,
		Tagline:       "Strong generalist; heavier than Qwen-7B, worth it for prose.",
	},
	{
		ID:            "phi-3.5-mini",
		DisplayName:   "Phi-3.5 Mini Instruct",
		Family:        "Microsoft",
		Params:        "3.8B",
		Quant:         "Q4_K_M",
		Repo:          "bartowski/Phi-3.5-mini-instruct-GGUF",
		File:          "Phi-3.5-mini-instruct-Q4_K_M.gguf",
		SizeMB:        2300,
		ContextTokens: 131072,
		Tagline:       "Microsoft's compact. Exceptional at structured output.",
	},
	{
		ID:            "gemma-2-2b",
		DisplayName:   "Gemma 2 2B Instruct",
		Family:        "Google",
		Params:        "2B",
		Quant:         "Q4_K_M",
		Repo:          "bartowski/gemma-2-2b-it-GGUF",
		File:          "gemma-2-2b-it-Q4_K_M.gguf",
		SizeMB:        1600,
		ContextTokens: 8192,
		Tagline:       "Very fast. Concise, confident; small context window.",
	},
}

// BuiltInList returns a copy of the built-in catalog. Callers must not
// mutate the returned slice; Model structs are small enough to copy.
func BuiltInList() []Model {
	out := make([]Model, len(builtIn))
	for i, m := range builtIn {
		m.BuiltIn = true
		out[i] = m
	}
	return out
}

// DefaultID is what Config.Model resolves to when unset. We pick the
// balanced 3B as the "just works" default: small enough to run on
// any laptop but strong enough that `i report` and similar don't
// routinely need the fallback parser.
const DefaultID = "qwen2.5-coder-3b"

// Catalog is the merged view of built-in + user-added custom models.
// It's cheap to construct (all in-memory) and safe to pass around.
type Catalog struct {
	all []Model
	by  map[string]int
}

// New builds a catalog from the built-in list plus optional custom
// entries (typically loaded from disk by the caller). Custom entries
// override built-in ones with the same ID — useful if a user wants
// to pin a different quant for a known model.
func New(custom []Model) *Catalog {
	all := BuiltInList()
	for _, cm := range custom {
		cm.BuiltIn = false
		// Replace if custom ID shadows a built-in.
		replaced := false
		for i := range all {
			if all[i].ID == cm.ID {
				all[i] = cm
				replaced = true
				break
			}
		}
		if !replaced {
			all = append(all, cm)
		}
	}
	c := &Catalog{all: all, by: make(map[string]int, len(all))}
	for i, m := range all {
		c.by[m.ID] = i
	}
	return c
}

// All returns every entry in insertion order (built-ins first, then
// custom), sorted by family and params ascending. Stable for display.
func (c *Catalog) All() []Model {
	out := make([]Model, len(c.all))
	copy(out, c.all)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family < out[j].Family
		}
		return paramsMB(out[i].Params) < paramsMB(out[j].Params)
	})
	return out
}

// Get looks up a model by ID. Returns nil if not found.
//
// As a backward-compatibility fallback, if id doesn't match a catalog
// ID we also try matching it against the GGUF filename stem. That
// lets older configs (which stored `model = "qwen2.5-coder-7b-
// instruct-q4_k_m"`) keep resolving without a migration step: such
// strings map cleanly to the canonical catalog entry.
func (c *Catalog) Get(id string) *Model {
	if id == "" {
		return nil
	}
	if i, ok := c.by[id]; ok {
		m := c.all[i]
		return &m
	}
	stem := strings.TrimSuffix(id, ".gguf")
	for _, m := range c.all {
		if m.File == "" {
			continue
		}
		ms := strings.TrimSuffix(m.File, ".gguf")
		if strings.EqualFold(ms, stem) {
			mm := m
			return &mm
		}
	}
	return nil
}

// Default returns the catalog's default model (Config.Model == "").
// Falls back to the first entry if the nominal default isn't present.
func (c *Catalog) Default() *Model {
	if m := c.Get(DefaultID); m != nil {
		return m
	}
	if len(c.all) == 0 {
		return nil
	}
	m := c.all[0]
	return &m
}

// Resolve turns a user-entered model reference into a Model. Accepts:
//
//   - Catalog ID:        "qwen2.5-coder-3b"
//   - HF repo ID:        "bartowski/Phi-3.5-mini-instruct-GGUF"
//   - HF repo + quant:   "bartowski/Phi-3.5-mini-instruct-GGUF:Q6_K"
//
// For HF-style refs, Resolve only produces a partial Model (Repo,
// Quant filled, File/SizeMB empty). Callers that need the actual
// GGUF filename and size must probe HF via the hf subpackage.
// This lets Resolve stay pure — no network — so tests are cheap.
//
// Unknown short tags return an error listing close matches.
func (c *Catalog) Resolve(ref string) (*Model, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("empty model reference")
	}
	// Catalog hit (short ID).
	if m := c.Get(ref); m != nil {
		return m, nil
	}
	// HF-style: contains a slash. Optionally suffixed with ":QUANT".
	if strings.Contains(ref, "/") {
		repo, quant := splitRepoQuant(ref)
		return &Model{
			ID:          HFToID(repo, quant),
			DisplayName: repo,
			Family:      hfFamily(repo),
			Params:      "?",
			Quant:       quant,
			Repo:        repo,
			Tagline:     "Custom Hugging Face model. Resolved on first use.",
		}, nil
	}
	return nil, fmt.Errorf("unknown model %q; try `i model list` or an HF repo ID like %q",
		ref, "bartowski/Phi-3.5-mini-instruct-GGUF")
}

// splitRepoQuant parses "owner/repo" or "owner/repo:QUANT" into parts.
// An empty quant means "pick a sane default" (caller decides).
func splitRepoQuant(ref string) (repo, quant string) {
	if i := strings.LastIndexByte(ref, ':'); i > 0 && !strings.Contains(ref[i+1:], "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

// HFToID produces a stable short ID for a custom HF model. Keeps
// config files readable and gives the user something they can type.
// Lowercase, replaces slashes with underscores, appends quant suffix
// if set.
func HFToID(repo, quant string) string {
	base := strings.ToLower(strings.ReplaceAll(repo, "/", "_"))
	if quant != "" {
		return base + "-" + strings.ToLower(quant)
	}
	return base
}

// hfFamily extracts the owner segment of a repo ID. Purely cosmetic.
func hfFamily(repo string) string {
	i := strings.IndexByte(repo, '/')
	if i <= 0 {
		return repo
	}
	return repo[:i]
}

// paramsMB is a rough "size key" for sorting. "1.5B" < "3B" < "7B".
// Returns 0 on unparseable input so sort is still stable.
func paramsMB(p string) int {
	s := strings.TrimSpace(p)
	if s == "" || s == "?" {
		return 0
	}
	mul := 1
	switch s[len(s)-1] {
	case 'B', 'b':
		mul = 1000
		s = s[:len(s)-1]
	case 'M', 'm':
		s = s[:len(s)-1]
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0
	}
	return int(f * float64(mul))
}

// ValidGGUFQuant reports whether a quant tag is one llama.cpp
// reliably supports across recent versions. Unknown-but-plausible
// quants (e.g. IQ-series) return true; we only reject obviously
// unsupported ones. The check is lenient by design — the definitive
// answer comes from llamafile at load time.
func ValidGGUFQuant(q string) bool {
	if q == "" {
		return false
	}
	q = strings.ToUpper(q)
	// Canonical llama.cpp quants (non-exhaustive but covers everything
	// shipped by Qwen / bartowski / TheBloke).
	canon := []string{
		"Q2_K", "Q3_K_S", "Q3_K_M", "Q3_K_L",
		"Q4_0", "Q4_K_S", "Q4_K_M",
		"Q5_0", "Q5_K_S", "Q5_K_M",
		"Q6_K", "Q8_0",
		"F16", "F32", "BF16",
	}
	for _, c := range canon {
		if q == c {
			return true
		}
	}
	// IQ-series imatrix quants: IQ1_S, IQ2_XXS, IQ3_M, IQ4_NL, ...
	// Accept anything starting with IQ followed by a digit.
	if len(q) >= 3 && q[0] == 'I' && q[1] == 'Q' && (q[2] >= '0' && q[2] <= '9') {
		return true
	}
	return false
}

// PickQuant chooses a sensible quant when the user didn't specify one.
// Q4_K_M is the llama.cpp community default: smallest size that
// consistently matches F16 quality on typical benchmarks.
func PickQuant() string { return "Q4_K_M" }

// ModelFilename is what the local cache should name a given model's
// GGUF blob. Uses the HF-provided filename verbatim if present
// (important: filenames are case-sensitive on some filesystems and
// model family matters when scanning the cache). Falls back to an
// ID-derived name when we only have a Repo (custom models prior
// to HF probe).
func ModelFilename(m *Model) string {
	if m == nil {
		return ""
	}
	if m.File != "" {
		return m.File
	}
	return m.ID + ".gguf"
}

// IsGGUF returns true if path looks like a GGUF file by extension.
// Cheap check used when scanning custom user paths.
func IsGGUF(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".gguf")
}
