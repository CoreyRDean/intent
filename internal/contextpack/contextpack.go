// Package contextpack gathers OS, shell, cwd, and binary-availability context
// to inject into the system prompt.
package contextpack

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/CoreyRDean/intent/internal/model"
)

// CuratedBinaries are the names we probe with `which` so the model knows
// what's available without being given a full $PATH dump. Stable, deliberately
// small, covers the long tail of one-liner intents.
var CuratedBinaries = []string{
	"awk", "bat", "brew", "cargo", "cat", "cp", "curl", "cut", "df", "diff",
	"docker", "du", "fd", "find", "ffmpeg", "fzf", "gh", "git", "go", "grep",
	"head", "htop", "jq", "kubectl", "less", "ls", "make", "mv", "nc", "node",
	"npm", "pbcopy", "ping", "pip", "podman", "psql", "python3", "rg",
	"rsync", "scp", "sed", "sort", "ssh", "stat", "tail", "tar", "tr", "uniq",
	"unzip", "wc", "wget", "xargs", "yarn", "zip",
}

// Pack is the resolved context, ready to be turned into prompt inputs.
type Pack struct {
	OS, Arch, Kernel, Distro, Shell string
	Cwd                             string
	AvailableBins                   []string
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
	p.AvailableBins = whichAll(CuratedBinaries)

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
