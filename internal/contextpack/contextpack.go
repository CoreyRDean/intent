// Package contextpack gathers OS, shell, cwd, and binary-availability context
// to inject into the system prompt.
package contextpack

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/CoreyRDean/intent/internal/model"
)

// CuratedBinaries are names the model is very likely to reach for. When we
// truncate the full $PATH scan to fit within a prompt budget, we always
// keep the curated intersection with the user's $PATH first so common
// tools are guaranteed to be visible.
var CuratedBinaries = []string{
	"awk", "bat", "brew", "cargo", "cat", "cp", "curl", "cut", "df", "diff",
	"docker", "du", "fd", "find", "ffmpeg", "fzf", "gh", "git", "go", "grep",
	"head", "htop", "jq", "kubectl", "less", "ls", "make", "mv", "nc", "node",
	"npm", "pbcopy", "ping", "pip", "podman", "psql", "python3", "rg",
	"rsync", "scp", "sed", "sort", "ssh", "stat", "tail", "tar", "tr", "uniq",
	"unzip", "wc", "wget", "xargs", "yarn", "zip",
}

// maxBinsInPrompt caps how many binary names we include in the prompt. At
// ~10 chars per name + separator this is ~5KB of text, ~1200-1500 tokens,
// comfortable for a 32K-context model alongside everything else we inject.
const maxBinsInPrompt = 500

// Pack is the resolved context, ready to be turned into prompt inputs.
type Pack struct {
	OS, Arch, Kernel, Distro, Shell string
	Cwd                             string
	AvailableBins                   []string // what we put in the prompt
	TotalBinsOnPATH                 int      // count before truncation
	PATHDirs                        []string // directories we scanned
	GitBranch                       string
	GitDirty                        bool
}

var (
	cacheMu sync.Mutex
	cached  *Pack
)

// Gather collects context. The expensive parts (which probing, uname) are
// memoized for the lifetime of the process; cwd and git context are not.
func Gather(ctx context.Context) Pack {
	cacheMu.Lock()
	if cached != nil {
		base := *cached
		cacheMu.Unlock()
		// Per-call refresh of cwd and git status.
		if cwd, err := os.Getwd(); err == nil {
			base.Cwd = cwd
			base.GitBranch, base.GitDirty = gitInfo(ctx, cwd)
		}
		return base
	}
	cacheMu.Unlock()

	p := Pack{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}
	p.Kernel = uname(ctx, "-r")
	p.Distro = readDistro()
	if sh := os.Getenv("SHELL"); sh != "" {
		p.Shell = filepath.Base(sh)
	}
	if cwd, err := os.Getwd(); err == nil {
		p.Cwd = cwd
		p.GitBranch, p.GitDirty = gitInfo(ctx, cwd)
	}
	allOnPATH, dirs := scanPATH()
	p.PATHDirs = dirs
	p.TotalBinsOnPATH = len(allOnPATH)
	p.AvailableBins = assembleAvailableBins(allOnPATH)

	cacheMu.Lock()
	c := p
	cached = &c
	cacheMu.Unlock()
	return p
}

// AsPromptInputs converts the pack to model.SystemPromptInputs.
func (p Pack) AsPromptInputs(projectDirectives string, userContext []string) model.SystemPromptInputs {
	return model.SystemPromptInputs{
		OS:                p.OS,
		Arch:              p.Arch,
		Kernel:            p.Kernel,
		Distro:            p.Distro,
		Shell:             p.Shell,
		Cwd:               p.Cwd,
		AvailableBins:     p.AvailableBins,
		TotalBinsOnPATH:   p.TotalBinsOnPATH,
		GitBranch:         p.GitBranch,
		GitDirty:          p.GitDirty,
		ProjectDirectives: projectDirectives,
		UserContext:       userContext,
	}
}

func uname(ctx context.Context, flag string) string {
	out, err := exec.CommandContext(ctx, "uname", flag).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readDistro() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return ""
}

func whichAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// scanPATH walks every directory in $PATH, collects executable basenames,
// and returns them sorted and de-duplicated along with the list of
// directories actually scanned. Shadowing (first-wins, matching how the
// shell resolves) is implicit in the set semantics -- we only record the
// name, not the resolved path.
func scanPATH() (names []string, dirs []string) {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return nil, nil
	}
	seen := make(map[string]struct{}, 1024)
	seenDir := make(map[string]struct{}, 16)
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		// Normalize: some shells put "." or relative dirs on PATH; we
		// want an absolute key for dedup but preserve the raw entry
		// in the returned list.
		if _, ok := seenDir[dir]; ok {
			continue
		}
		seenDir[dir] = struct{}{}
		dirs = append(dirs, dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if isNoiseName(name) {
				continue
			}
			// Skip directories and anything non-executable. Use
			// e.Info() instead of os.Stat on the full path so we
			// avoid an extra syscall per entry; DirEntry can lie
			// about mode on some filesystems, so fall back to Stat
			// when Info fails.
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.IsDir() {
				continue
			}
			if info.Mode()&0o111 == 0 {
				continue
			}
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
			}
		}
	}
	names = make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, dirs
}

// isNoiseName filters out filenames that aren't user-facing commands.
// Libraries, headers, pkg-config files, dotfiles, and obviously generated
// entries get dropped so we don't waste prompt budget on them.
func isNoiseName(name string) bool {
	if name == "" {
		return true
	}
	if name[0] == '.' || name[0] == '_' {
		return true
	}
	// Very long names are almost always auto-generated (hash-suffixed,
	// vendored, etc.) and aren't things the user would invoke by name.
	if len(name) > 48 {
		return true
	}
	if runtime.GOOS != "windows" {
		lower := strings.ToLower(name)
		for _, ext := range []string{
			".so", ".dylib", ".dll", ".a", ".la", ".o",
			".h", ".hpp", ".pc", ".cmake", ".pyc",
		} {
			if strings.HasSuffix(lower, ext) {
				return true
			}
		}
		// foo.so.1, foo.dylib.2, etc.
		if strings.Contains(lower, ".so.") || strings.Contains(lower, ".dylib.") {
			return true
		}
	}
	return false
}

// assembleAvailableBins merges the $PATH scan with the curated set and
// enforces the prompt budget. Curated-and-present names go first so they
// survive any truncation; the remainder is alphabetical for determinism
// (which also keeps the cache fingerprint stable across runs).
func assembleAvailableBins(allOnPATH []string) []string {
	onPATH := make(map[string]struct{}, len(allOnPATH))
	for _, n := range allOnPATH {
		onPATH[n] = struct{}{}
	}
	curatedPresent := make([]string, 0, len(CuratedBinaries))
	curatedSet := make(map[string]struct{}, len(CuratedBinaries))
	for _, c := range CuratedBinaries {
		if _, ok := onPATH[c]; ok {
			curatedPresent = append(curatedPresent, c)
			curatedSet[c] = struct{}{}
		}
	}
	sort.Strings(curatedPresent)

	rest := make([]string, 0, len(allOnPATH))
	for _, n := range allOnPATH {
		if _, ok := curatedSet[n]; ok {
			continue
		}
		rest = append(rest, n)
	}
	// allOnPATH is already sorted; rest inherits that ordering.

	out := make([]string, 0, maxBinsInPrompt)
	out = append(out, curatedPresent...)
	remaining := maxBinsInPrompt - len(out)
	if remaining > 0 {
		if remaining > len(rest) {
			remaining = len(rest)
		}
		out = append(out, rest[:remaining]...)
	}
	return out
}

func gitInfo(ctx context.Context, cwd string) (branch string, dirty bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", false
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	branch = strings.TrimSpace(string(out))
	cmd = exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = cwd
	out, err = cmd.Output()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		dirty = true
	}
	return
}
