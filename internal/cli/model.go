package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/models"
	"github.com/CoreyRDean/intent/internal/models/hf"
	intentruntime "github.com/CoreyRDean/intent/internal/runtime"
	"github.com/CoreyRDean/intent/internal/state"
)

const modelUsage = `usage: i model <subcommand>

subcommands:
  list                        show the catalog of available models
  show <id>                   detail for one model (catalog id or HF repo)
  use  <id>                   switch current model (downloads if missing)
  pull [id]                   download without switching (defaults to current)
  rm   <id> [--purge]         forget custom entry; --purge also deletes the GGUF
  where [id]                  print on-disk path to the GGUF

<id> accepts either a short catalog id ("qwen2.5-coder-3b") or an
HF repo id, optionally suffixed with a quant tag:
  bartowski/Phi-3.5-mini-instruct-GGUF
  bartowski/Phi-3.5-mini-instruct-GGUF:Q6_K`

func cmdModel(ctx context.Context, args []string) int {
	if len(args) == 0 {
		errf(modelUsage)
		return 1
	}
	dirs, err := state.Resolve()
	if err != nil {
		errf("model: %v", err)
		return 3
	}
	cfg, _ := config.Load(dirs.ConfigPath())
	switch args[0] {
	case "--help", "-h", "help":
		fmt.Println(modelUsage)
		return 0
	case "list", "ls":
		return modelList(ctx, dirs, cfg)
	case "show":
		return modelShow(ctx, dirs, cfg, args[1:])
	case "use":
		return modelUse(ctx, dirs, cfg, args[1:])
	case "pull":
		return modelPull(ctx, dirs, cfg, args[1:])
	case "rm", "remove":
		return modelRm(ctx, dirs, cfg, args[1:])
	case "where":
		return modelWhere(dirs, cfg, args[1:])
	default:
		errf("unknown subcommand: %q\n%s", args[0], modelUsage)
		return 1
	}
}

// loadCatalog builds a catalog merging built-ins with the user's
// custom sidecar. Custom entries override same-ID built-ins. The
// sidecar lives next to config.toml (in state.Dirs.State on all
// platforms).
func loadCatalog(stateDir string) *models.Catalog {
	custom, _ := models.LoadCustom(stateDir)
	return models.New(custom)
}

// currentID returns the catalog ID of the configured model. If
// Config.Model holds a legacy filename stem, Catalog.Get's backward-
// compat path normalises it so the "CURRENT" marker in `i model
// list` lines up with the right row. Never returns empty for a
// healthy install.
func currentID(cat *models.Catalog, cfg *config.Config) string {
	raw := ""
	if cfg != nil {
		raw = cfg.Model
	}
	if raw != "" {
		if m := cat.Get(raw); m != nil {
			return m.ID
		}
		return raw
	}
	if d := cat.Default(); d != nil {
		return d.ID
	}
	return models.DefaultID
}

// modelList prints a table of available models with install + current
// markers. We use tabwriter so columns align regardless of family
// name length.
func modelList(_ context.Context, dirs state.Dirs, cfg *config.Config) int {
	cat := loadCatalog(dirs.State)
	cur := currentID(cat, cfg)
	rt := intentruntime.New(dirs.Cache)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tFAMILY\tPARAMS\tSIZE\tCONTEXT\tINSTALLED\tCURRENT\tTAGLINE")
	fmt.Fprintln(tw, "--\t------\t------\t----\t-------\t---------\t-------\t-------")
	for _, m := range cat.All() {
		installed := "no"
		if m.File != "" && rt.HaveModel(m.File) {
			installed = "yes"
		}
		current := ""
		if m.ID == cur {
			current = "←"
		}
		size := "?"
		if m.SizeMB > 0 {
			size = fmt.Sprintf("%.1f GB", float64(m.SizeMB)/1024)
		}
		context := "?"
		if m.ContextTokens > 0 {
			context = fmt.Sprintf("%dk", m.ContextTokens/1024)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.ID, m.Family, m.Params, size, context, installed, current, m.Tagline)
	}
	_ = tw.Flush()
	fmt.Println()
	fmt.Println("Switch:       i model use <id>")
	fmt.Println("HF repo:      i model use <owner>/<repo>[:QUANT]")
	fmt.Println("Download:     i model pull [id]")
	return 0
}

// modelShow prints details for one entry. For catalog IDs it's pure
// data. For HF repos it probes the Hub to surface actual file sizes
// and compatible quants — this is where the "compatibility check"
// the user asked for lives.
func modelShow(ctx context.Context, dirs state.Dirs, cfg *config.Config, args []string) int {
	if len(args) == 0 {
		errf("usage: i model show <id>")
		return 1
	}
	cat := loadCatalog(dirs.State)
	m, err := cat.Resolve(args[0])
	if err != nil {
		errf("model show: %v", err)
		return 1
	}
	fmt.Printf("ID:       %s\n", m.ID)
	fmt.Printf("Name:     %s\n", m.DisplayName)
	fmt.Printf("Family:   %s\n", m.Family)
	if m.Params != "" {
		fmt.Printf("Params:   %s\n", m.Params)
	}
	fmt.Printf("Repo:     %s\n", m.Repo)
	if m.File != "" {
		fmt.Printf("File:     %s\n", m.File)
	}
	if m.Quant != "" {
		fmt.Printf("Quant:    %s\n", m.Quant)
	}
	if m.SizeMB > 0 {
		fmt.Printf("Size:     %.2f GB\n", float64(m.SizeMB)/1024)
	}
	if m.ContextTokens > 0 {
		fmt.Printf("Context:  %d tokens\n", m.ContextTokens)
	}
	if m.Tagline != "" {
		fmt.Printf("Tagline:  %s\n", m.Tagline)
	}
	if m.BuiltIn {
		fmt.Println("Source:   built-in catalog")
	} else {
		fmt.Println("Source:   custom (user-added)")
	}
	rt := intentruntime.New(dirs.Cache)
	path := rt.ModelPath(models.ModelFilename(m))
	fmt.Printf("Path:     %s\n", path)
	if _, err := os.Stat(path); err == nil {
		fmt.Println("Status:   installed")
	} else {
		fmt.Println("Status:   not installed")
	}
	cur := currentID(cat, cfg)
	if m.ID == cur {
		fmt.Println("Current:  yes")
	}

	// Probe HF for compatibility if the entry is incomplete (custom
	// refs that haven't been resolved yet). This is the compatibility
	// check: does the repo exist, does it contain GGUF files, and is
	// the requested quant actually available.
	if m.File == "" && m.Repo != "" {
		fmt.Println()
		fmt.Println("probing Hugging Face...")
		if err := probeAndPrintHF(ctx, m); err != nil {
			errf("hf probe: %v", err)
			return 3
		}
	}
	return 0
}

// probeAndPrintHF hits the HF API, lists available GGUF quants, and
// prints them so the user can see what's on offer before committing
// to a download. Mutates m with resolved File/SizeMB/Quant.
func probeAndPrintHF(ctx context.Context, m *models.Model) error {
	c := hf.New()
	info, err := c.GetRepo(ctx, m.Repo)
	if err != nil {
		return err
	}
	files, err := c.ListFiles(ctx, m.Repo)
	if err != nil {
		return err
	}
	ggufs := hf.FindGGUF(files)
	if len(ggufs) == 0 {
		return fmt.Errorf("no .gguf files in repo (only GGUF is supported; see https://github.com/ggerganov/llama.cpp for conversion)")
	}
	fmt.Printf("  repo ok:     %s", info.ID)
	if gated, ok := info.Gated.(bool); ok && gated {
		fmt.Printf(" (gated)")
	} else if gated, ok := info.Gated.(string); ok && gated != "" && gated != "false" {
		fmt.Printf(" (gated: %s)", gated)
	}
	fmt.Println()
	fmt.Printf("  ggufs found: %d\n", len(ggufs))
	fmt.Println()
	fmt.Println("  available quants:")
	for _, f := range ggufs {
		mb := f.Size / (1024 * 1024)
		fmt.Printf("    %-60s  %d MB\n", f.Path, mb)
	}
	chosen, err := hf.PickQuant(ggufs, m.Quant)
	if err != nil {
		return err
	}
	m.File = chosen.Path
	m.SizeMB = int(chosen.Size / (1024 * 1024))
	if m.Quant == "" {
		m.Quant = inferQuantFromFilename(chosen.Path)
	}
	fmt.Println()
	fmt.Printf("  would use:   %s (%.2f GB)\n", chosen.Path, float64(m.SizeMB)/1024)
	return nil
}

// inferQuantFromFilename peels the quant tag out of a typical GGUF
// filename. Best-effort; empty string on failure. Only used when
// the user didn't specify one.
func inferQuantFromFilename(filename string) string {
	base := strings.TrimSuffix(strings.ToUpper(filepath.Base(filename)), ".GGUF")
	// Try recognisable tokens right-to-left.
	tokens := []string{"Q8_0", "Q6_K", "Q5_K_M", "Q5_K_S", "Q5_0",
		"Q4_K_M", "Q4_K_S", "Q4_0", "Q3_K_L", "Q3_K_M", "Q3_K_S", "Q2_K",
		"F16", "F32", "BF16"}
	for _, t := range tokens {
		if strings.Contains(base, t) {
			return t
		}
	}
	return ""
}

// modelUse switches the current model. Resolves the reference, persists
// it as a custom entry if it's an HF repo we haven't seen, downloads
// the model if it's not installed, and finally updates cfg.Model +
// restarts the daemon so subsequent `i` calls use the new model.
func modelUse(ctx context.Context, dirs state.Dirs, cfg *config.Config, args []string) int {
	if len(args) == 0 {
		errf("usage: i model use <id>")
		return 1
	}
	ref := args[0]
	yes := false
	for _, a := range args[1:] {
		if a == "--yes" || a == "-y" {
			yes = true
		}
	}
	cat := loadCatalog(dirs.State)
	m, err := cat.Resolve(ref)
	if err != nil {
		errf("model use: %v", err)
		return 1
	}
	// HF ref that's never been resolved: probe Hub, fill in details,
	// then persist as custom so next time it's a plain catalog hit.
	if m.File == "" && m.Repo != "" {
		fmt.Fprintln(os.Stderr, "resolving model on Hugging Face...")
		if err := probeAndPrintHF(ctx, m); err != nil {
			errf("model use: %v", err)
			return 3
		}
		if _, err := models.AddCustom(dirs.State, *m); err != nil {
			errf("model use: save custom: %v", err)
			return 3
		}
		// Reload so the new entry is in the catalog immediately.
		cat = loadCatalog(dirs.State)
		if refreshed := cat.Get(m.ID); refreshed != nil {
			m = refreshed
		}
	}

	if !models.ValidGGUFQuant(m.Quant) && m.Quant != "" {
		fmt.Fprintf(os.Stderr, "warning: quant %q is unusual; llamafile may or may not load it.\n", m.Quant)
	}

	// Download if missing.
	rt := intentruntime.New(dirs.Cache)
	file := models.ModelFilename(m)
	if !rt.HaveModel(file) {
		fmt.Fprintf(os.Stderr, "downloading %s (~%.1f GB)...\n", m.ID, float64(m.SizeMB)/1024)
		if !yes && !confirmYes("proceed?") {
			fmt.Fprintln(os.Stderr, "aborted.")
			return 1
		}
		mi := intentruntime.FromCatalog(m)
		if err := rt.EnsureModel(ctx, mi, progressCB("model")); err != nil {
			fmt.Fprintln(os.Stderr)
			errf("download: %v", err)
			return 3
		}
		fmt.Fprintln(os.Stderr)
	}

	// Persist selection.
	newCfg := *cfg
	newCfg.Model = m.ID
	if err := config.Write(dirs.ConfigPath(), &newCfg); err != nil {
		errf("model use: save config: %v", err)
		return 3
	}
	fmt.Printf("current model: %s\n", m.ID)

	// Nudge the daemon to pick up the new model. If it's running,
	// restart it; if not, leave it alone (user will start it on next
	// `i` call via ensureBackendReady).
	if pingDaemon(dirs) {
		fmt.Fprintln(os.Stderr, "restarting daemon with new model...")
		_ = daemonStop(dirs)
		time.Sleep(500 * time.Millisecond)
		if err := startDaemonAndWait(dirs); err != nil {
			errf("daemon restart: %v (run `i daemon start` manually)", err)
			return 3
		}
		fmt.Fprintln(os.Stderr, "daemon: ready.")
	}
	return 0
}

// modelPull downloads a model without changing the current selection.
// Useful for pre-fetching on a fast connection before switching.
func modelPull(ctx context.Context, dirs state.Dirs, cfg *config.Config, args []string) int {
	cat := loadCatalog(dirs.State)
	var m *models.Model
	var err error
	if len(args) == 0 {
		id := currentID(cat, cfg)
		m = cat.Get(id)
		if m == nil {
			errf("model pull: current model %q not in catalog; specify an id", id)
			return 1
		}
	} else {
		m, err = cat.Resolve(args[0])
		if err != nil {
			errf("model pull: %v", err)
			return 1
		}
		if m.File == "" && m.Repo != "" {
			fmt.Fprintln(os.Stderr, "resolving model on Hugging Face...")
			if err := probeAndPrintHF(ctx, m); err != nil {
				errf("model pull: %v", err)
				return 3
			}
		}
	}

	rt := intentruntime.New(dirs.Cache)
	if !rt.HaveLlamafile() {
		fmt.Fprintln(os.Stderr, "downloading runtime...")
		if err := rt.EnsureLlamafile(ctx, progressCB("llamafile")); err != nil {
			fmt.Fprintln(os.Stderr)
			errf("llamafile: %v", err)
			return 3
		}
		fmt.Fprintln(os.Stderr)
	}
	file := models.ModelFilename(m)
	if rt.HaveModel(file) {
		fmt.Printf("%s: already installed.\n", m.ID)
		return 0
	}
	fmt.Fprintf(os.Stderr, "downloading %s (~%.1f GB)...\n", m.ID, float64(m.SizeMB)/1024)
	mi := intentruntime.FromCatalog(m)
	if err := rt.EnsureModel(ctx, mi, progressCB("model")); err != nil {
		fmt.Fprintln(os.Stderr)
		errf("download: %v", err)
		return 3
	}
	fmt.Fprintln(os.Stderr)
	fmt.Printf("%s: installed.\n", m.ID)
	return 0
}

// modelRm deletes a custom entry (not a built-in), and optionally
// removes its GGUF file from the cache with --purge. Built-ins can
// still be purged off disk — you just can't un-register them from
// the catalog.
func modelRm(_ context.Context, dirs state.Dirs, cfg *config.Config, args []string) int {
	if len(args) == 0 {
		errf("usage: i model rm <id> [--purge]")
		return 1
	}
	id := args[0]
	purge := false
	for _, a := range args[1:] {
		if a == "--purge" {
			purge = true
		}
	}
	cat := loadCatalog(dirs.State)
	m := cat.Get(id)
	if m == nil {
		errf("model rm: unknown model %q", id)
		return 1
	}
	if id == currentID(cat, cfg) {
		errf("model rm: %q is the current model; switch first with `i model use <other>`", id)
		return 1
	}
	if !m.BuiltIn {
		if _, err := models.RemoveCustom(dirs.State, id); err != nil {
			errf("model rm: %v", err)
			return 3
		}
		fmt.Printf("removed custom entry: %s\n", id)
	} else if !purge {
		errf("model rm: %q is a built-in catalog entry; use --purge to delete its GGUF file only", id)
		return 1
	}
	if purge {
		rt := intentruntime.New(dirs.Cache)
		path := rt.ModelPath(models.ModelFilename(m))
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("no GGUF to delete at %s\n", path)
				return 0
			}
			errf("model rm: delete file: %v", err)
			return 3
		}
		fmt.Printf("deleted: %s\n", path)
	}
	return 0
}

// modelWhere prints the on-disk path to a model's GGUF. Handy for
// scripts, debugging, and checking whether a symlink trick worked.
func modelWhere(dirs state.Dirs, cfg *config.Config, args []string) int {
	cat := loadCatalog(dirs.State)
	var m *models.Model
	if len(args) == 0 {
		id := currentID(cat, cfg)
		m = cat.Get(id)
		if m == nil {
			errf("model where: current model %q not found", id)
			return 1
		}
	} else {
		var err error
		m, err = cat.Resolve(args[0])
		if err != nil {
			errf("model where: %v", err)
			return 1
		}
	}
	rt := intentruntime.New(dirs.Cache)
	fmt.Println(rt.ModelPath(models.ModelFilename(m)))
	return 0
}

func progressCB(label string) intentruntime.Progress {
	return func(downloaded, total int64) {
		if total <= 0 {
			fmt.Fprintf(os.Stderr, "\r  %s: %d MB", label, downloaded/(1024*1024))
			return
		}
		pct := float64(downloaded) / float64(total) * 100
		fmt.Fprintf(os.Stderr, "\r  %s: %.1f%% (%d / %d MB)", label, pct,
			downloaded/(1024*1024), total/(1024*1024))
	}
}
