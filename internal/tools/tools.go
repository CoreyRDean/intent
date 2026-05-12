// Package tools implements the read-only tool catalog the model may invoke
// during a multi-step turn. See docs/SPEC.md §2.3.
//
// Tools never mutate state and never require user confirmation. Adding a
// non-read-only tool is a spec amendment. The only exception is ask_user,
// which is read-only in the sense that intent state isn't mutated -- it
// just interrupts the turn to collect a string from the human.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/verbose"
)

// Host is the abstraction tools use to interact with the user or the
// outside world in ways the base package can't do on its own (e.g.
// asking a clarifying question on a TTY). A nil Host is valid -- tools
// that need one report a helpful error result instead of crashing so
// the model can fall back to a different approach.
type Host interface {
	// AskUser prompts the user with a question and optional choices,
	// and returns the user's response. Implementations MUST return an
	// error when no interactive TTY is available so the model can fall
	// back to `clarify`.
	AskUser(ctx context.Context, question string, choices []string) (string, error)
}

// Result is the JSON-serializable return from a tool execution.
type Result map[string]any

// Run dispatches a tool by name with the given arguments. The host may
// be nil for tools that don't need human interaction.
func Run(ctx context.Context, host Host, name string, argsRaw json.RawMessage) (Result, error) {
	switch name {
	case "list_dir":
		return listDir(argsRaw)
	case "read_file":
		return readFile(argsRaw)
	case "head_file":
		return headFile(argsRaw)
	case "which":
		return whichTool(argsRaw)
	case "stat":
		return statTool(argsRaw)
	case "env_get":
		return envGet(argsRaw)
	case "cwd":
		return cwd()
	case "os_info":
		return osInfo(ctx)
	case "git_status":
		return gitStatus(ctx)
	case "help":
		return helpTool(ctx, argsRaw)
	case "grep":
		return grepTool(ctx, argsRaw)
	case "find_files":
		return findFilesTool(ctx, argsRaw)
	case "web_fetch":
		return webFetchTool(ctx, argsRaw)
	case "ask_user":
		return askUserTool(ctx, host, argsRaw)
	default:
		return nil, fmt.Errorf("unknown tool: %q", name)
	}
}

// Names returns the canonical list of registered tool names, stable
// across calls. The model's response schema enum is derived from this
// so the schema and the dispatcher can never drift.
func Names() []string {
	return []string{
		"list_dir", "read_file", "head_file", "which", "stat",
		"env_get", "cwd", "os_info", "git_status",
		"help", "grep", "find_files", "web_fetch", "ask_user",
	}
}

func listDir(raw json.RawMessage) (Result, error) {
	var args struct {
		Path       string `json:"path"`
		Depth      int    `json:"depth"`
		MaxEntries int    `json:"max_entries"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.Depth <= 0 {
		args.Depth = 1
	}
	if args.MaxEntries <= 0 {
		args.MaxEntries = 200
	}
	root, err := filepath.Abs(args.Path)
	if err != nil {
		return Result{"error": err.Error()}, nil
	}
	out := make([]map[string]any, 0, min(args.MaxEntries, 32))
	truncated := false
	var walk func(dir string, relDir string, depth int) error
	walk = func(dir string, relDir string, depth int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if len(out) >= args.MaxEntries {
				truncated = true
				return nil
			}
			info, _ := e.Info()
			var size int64
			if info != nil {
				size = info.Size()
			}
			typ := "file"
			if e.IsDir() {
				typ = "dir"
			} else if info != nil && info.Mode()&os.ModeSymlink != 0 {
				typ = "symlink"
			}
			relPath := e.Name()
			if relDir != "" {
				relPath = filepath.Join(relDir, e.Name())
			}
			out = append(out, map[string]any{
				"name": e.Name(),
				"path": relPath,
				"type": typ,
				"size": size,
			})
			if depth > 1 && e.IsDir() {
				if err := walk(filepath.Join(dir, e.Name()), relPath, depth-1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, "", args.Depth); err != nil {
		return Result{"error": err.Error()}, nil
	}
	return Result{"entries": out, "truncated": truncated}, nil
}

func readFile(raw json.RawMessage) (Result, error) {
	var args struct {
		Path      string `json:"path"`
		MaxBytes  int    `json:"max_bytes"`
		StartLine int    `json:"start_line"` // 1-indexed inclusive; 0 = from start
		EndLine   int    `json:"end_line"`   // 1-indexed inclusive; 0 = to end
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.MaxBytes <= 0 {
		args.MaxBytes = 8192
	}
	data, err := os.ReadFile(args.Path)
	if err != nil {
		return Result{"error": err.Error()}, nil
	}

	// Line-range slicing is optional. When requested, we operate on
	// the whole file first, then cap the resulting window to MaxBytes.
	if args.StartLine > 0 || args.EndLine > 0 {
		lines := strings.Split(string(data), "\n")
		total := len(lines)
		start := args.StartLine
		if start < 1 {
			start = 1
		}
		end := args.EndLine
		if end < 1 || end > total {
			end = total
		}
		if start > total {
			return Result{"content": "", "truncated": false, "size": 0, "total_lines": total}, nil
		}
		window := strings.Join(lines[start-1:end], "\n")
		truncated := false
		if len(window) > args.MaxBytes {
			window = window[:args.MaxBytes]
			truncated = true
		}
		return Result{
			"content":     window,
			"truncated":   truncated,
			"size":        len(window),
			"total_lines": total,
			"start_line":  start,
			"end_line":    end,
		}, nil
	}

	truncated := false
	if len(data) > args.MaxBytes {
		data = data[:args.MaxBytes]
		truncated = true
	}
	return Result{"content": string(data), "truncated": truncated, "size": len(data)}, nil
}

func headFile(raw json.RawMessage) (Result, error) {
	var args struct {
		Path  string `json:"path"`
		Lines int    `json:"lines"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Lines <= 0 {
		args.Lines = 50
	}
	data, err := os.ReadFile(args.Path)
	if err != nil {
		return Result{"error": err.Error()}, nil
	}
	all := strings.SplitAfter(string(data), "\n")
	out := all
	if len(all) > args.Lines {
		out = all[:args.Lines]
	}
	return Result{"lines": strings.Join(out, ""), "total_lines": len(all)}, nil
}

func whichTool(raw json.RawMessage) (Result, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return Result{"found": false}, nil
	}
	p, err := exec.LookPath(args.Name)
	if err != nil {
		return Result{"found": false}, nil
	}
	return Result{"found": true, "path": p}, nil
}

func statTool(raw json.RawMessage) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	info, err := os.Stat(args.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{"exists": false}, nil
		}
		return Result{"error": err.Error()}, nil
	}
	typ := "file"
	if info.IsDir() {
		typ = "dir"
	} else if info.Mode()&os.ModeSymlink != 0 {
		typ = "symlink"
	}
	return Result{
		"exists": true,
		"type":   typ,
		"size":   info.Size(),
		"perms":  info.Mode().String(),
		"mtime":  info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
	}, nil
}

// secretEnvPattern matches env var names that almost certainly hold secrets.
// Returning the value of these is refused; the tool reports redacted=true.
var secretEnvPattern = regexp.MustCompile(`(?i)(token|secret|password|api[_-]?key|auth|credential|private[_-]?key|access[_-]?key)`)

func envGet(raw json.RawMessage) (Result, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return Result{"found": false}, nil
	}
	val, ok := os.LookupEnv(args.Name)
	if !ok {
		return Result{"found": false}, nil
	}
	if secretEnvPattern.MatchString(args.Name) {
		return Result{"found": true, "redacted": true}, nil
	}
	return Result{"found": true, "value": val}, nil
}

func cwd() (Result, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Result{"error": err.Error()}, nil
	}
	return Result{"path": wd}, nil
}

func osInfo(ctx context.Context) (Result, error) {
	r := Result{
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	}
	if out, err := exec.CommandContext(ctx, "uname", "-r").Output(); err == nil {
		r["kernel"] = strings.TrimSpace(string(out))
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		r["shell"] = filepath.Base(sh)
	}
	return r, nil
}

func gitStatus(ctx context.Context) (Result, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return Result{"is_repo": false}, nil
	}
	wd, _ := os.Getwd()
	branchOut, err := exec.CommandContext(ctx, "git", "-C", wd, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return Result{"is_repo": false}, nil
	}
	statusOut, _ := exec.CommandContext(ctx, "git", "-C", wd, "status", "--porcelain").Output()
	dirty := len(strings.TrimSpace(string(statusOut))) > 0
	files := 0
	for _, line := range strings.Split(strings.TrimSpace(string(statusOut)), "\n") {
		if line != "" {
			files++
		}
	}
	return Result{
		"is_repo":       true,
		"branch":        strings.TrimSpace(string(branchOut)),
		"dirty":         dirty,
		"files_changed": files,
	}, nil
}

// helpTool discovers how to use a command by trying the conventional
// help mechanisms in order. Returns the first strategy that produced
// useful output. Safe-by-construction: only invokes the target binary
// with standard help flags and the `man` viewer, each with a short
// timeout, and never inherits stdin.
func helpTool(ctx context.Context, raw json.RawMessage) (Result, error) {
	var args struct {
		Name     string `json:"name"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return Result{"error": "name is required"}, nil
	}
	if args.MaxBytes <= 0 {
		args.MaxBytes = 8192
	}
	// Defensive: refuse anything that doesn't look like a bare binary
	// name. The model shouldn't be able to smuggle in shell metachars.
	if !looksLikeBinaryName(args.Name) {
		return Result{"error": "invalid binary name"}, nil
	}
	binPath, err := exec.LookPath(args.Name)
	if err != nil {
		return Result{"found": false, "tried": []string{}}, nil
	}

	strategies := [][]string{
		{binPath, "--help"},
		{binPath, "-h"},
		{binPath, "help"},
	}
	// man -P cat so we get plain text with no pager; most but not all
	// systems have a manpage for every binary.
	if manPath, err := exec.LookPath("man"); err == nil {
		strategies = append(strategies, []string{manPath, "-P", "cat", args.Name})
	}

	tried := make([]string, 0, len(strategies))
	for _, cmdline := range strategies {
		label := strings.Join(cmdline, " ")
		tried = append(tried, label)
		out := runBoundedSubprocess(ctx, cmdline, 5*time.Second, args.MaxBytes)
		if looksLikeHelp(out) {
			return Result{
				"found":    true,
				"path":     binPath,
				"strategy": label,
				"output":   out,
				"tried":    tried,
			}, nil
		}
	}
	return Result{
		"found":    true,
		"path":     binPath,
		"strategy": "",
		"output":   "",
		"tried":    tried,
		"note":     "binary exists but no standard help mechanism produced usable output",
	}, nil
}

// grepTool searches for a regex pattern under a path. Prefers rg when
// present for speed and sensible defaults, falls back to grep -rEn.
func grepTool(ctx context.Context, raw json.RawMessage) (Result, error) {
	var args struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		MaxMatches      int    `json:"max_matches"`
		CaseInsensitive bool   `json:"case_insensitive"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Pattern == "" {
		return Result{"error": "pattern is required"}, nil
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.MaxMatches <= 0 {
		args.MaxMatches = 100
	}

	var cmdline []string
	if rgPath, err := exec.LookPath("rg"); err == nil {
		perFile := args.MaxMatches / 4
		if perFile < 1 {
			perFile = 1
		}
		cmdline = []string{rgPath, "--no-heading", "-n", "-H",
			"--max-count", itoa(perFile),
		}
		if args.CaseInsensitive {
			cmdline = append(cmdline, "-i")
		}
		cmdline = append(cmdline, "-e", args.Pattern, "--", args.Path)
	} else if grepPath, err := exec.LookPath("grep"); err == nil {
		cmdline = []string{grepPath, "-rEn"}
		if args.CaseInsensitive {
			cmdline = append(cmdline, "-i")
		}
		cmdline = append(cmdline, "-e", args.Pattern, args.Path)
	} else {
		return Result{"error": "neither rg nor grep is available on $PATH"}, nil
	}

	raw2 := runBoundedSubprocess(ctx, cmdline, 10*time.Second, 64*1024)
	lines := strings.Split(strings.TrimRight(raw2, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	truncated := false
	if len(lines) > args.MaxMatches {
		lines = lines[:args.MaxMatches]
		truncated = true
	}
	return Result{
		"matches":   lines,
		"count":     len(lines),
		"truncated": truncated,
		"tool":      cmdline[0],
	}, nil
}

// findFilesTool locates files by name pattern. Prefers fd, falls back
// to find.
func findFilesTool(ctx context.Context, raw json.RawMessage) (Result, error) {
	var args struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		MaxResults int    `json:"max_results"`
		Type       string `json:"type"` // file | dir | any
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Pattern == "" {
		return Result{"error": "pattern is required"}, nil
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 200
	}
	if args.Type == "" {
		args.Type = "any"
	}

	var cmdline []string
	if fdPath, err := exec.LookPath("fd"); err == nil {
		cmdline = []string{fdPath, "--color", "never"}
		switch args.Type {
		case "file":
			cmdline = append(cmdline, "-t", "f")
		case "dir":
			cmdline = append(cmdline, "-t", "d")
		}
		cmdline = append(cmdline, "--max-results", itoa(args.MaxResults), "--", args.Pattern, args.Path)
	} else if findPath, err := exec.LookPath("find"); err == nil {
		cmdline = []string{findPath, args.Path}
		switch args.Type {
		case "file":
			cmdline = append(cmdline, "-type", "f")
		case "dir":
			cmdline = append(cmdline, "-type", "d")
		}
		// -iname for case-insensitive glob-ish match. This isn't a
		// full regex but is closer to what "pattern" means to a user
		// when fd isn't available.
		cmdline = append(cmdline, "-iname", args.Pattern)
	} else {
		return Result{"error": "neither fd nor find is available on $PATH"}, nil
	}

	out := runBoundedSubprocess(ctx, cmdline, 10*time.Second, 64*1024)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	truncated := false
	if len(lines) > args.MaxResults {
		lines = lines[:args.MaxResults]
		truncated = true
	}
	return Result{
		"paths":     lines,
		"count":     len(lines),
		"truncated": truncated,
		"tool":      cmdline[0],
	}, nil
}

// webFetchTool performs an HTTP GET and returns the response body as
// text, bounded by a sensible max size. Refuses non-http(s) schemes and
// any host whose parse fails.
func webFetchTool(ctx context.Context, raw json.RawMessage) (Result, error) {
	var args struct {
		URL      string `json:"url"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.URL == "" {
		return Result{"error": "url is required"}, nil
	}
	if args.MaxBytes <= 0 || args.MaxBytes > 256*1024 {
		args.MaxBytes = 32768
	}
	u, err := url.Parse(args.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Result{"error": "url must be http(s)"}, nil
	}

	// Short, bounded roundtrip. The engine ctx adds a per-turn
	// timeout on top; we clip further so a single slow fetch can't
	// eat the whole budget.
	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, "GET", args.URL, nil)
	if err != nil {
		return Result{"error": err.Error()}, nil
	}
	req.Header.Set("User-Agent", "intent/agentic-fetch")
	req.Header.Set("Accept", "text/*, application/json, */*;q=0.5")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{"error": err.Error()}, nil
	}
	defer resp.Body.Close()

	buf := make([]byte, args.MaxBytes+1)
	n, _ := io.ReadFull(io.LimitReader(resp.Body, int64(args.MaxBytes+1)), buf)
	truncated := n > args.MaxBytes
	if truncated {
		n = args.MaxBytes
	}
	body := string(buf[:n])
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		body = stripHTML(body)
	}
	return Result{
		"status":       resp.StatusCode,
		"content_type": ct,
		"body":         body,
		"truncated":    truncated,
		"url":          args.URL,
	}, nil
}

// askUserTool pauses the turn to ask the human a question. Tools can't
// reach the terminal on their own, so the engine wires a Host in via
// Run().
func askUserTool(ctx context.Context, host Host, raw json.RawMessage) (Result, error) {
	var args struct {
		Question string   `json:"question"`
		Choices  []string `json:"choices"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Question == "" {
		return Result{"error": "question is required"}, nil
	}
	if host == nil {
		// Intent is running non-interactively (piped, supervised, or
		// the caller didn't wire a host). Tell the model so it can
		// fall back to the terminal `clarify` approach or just make
		// its best guess.
		return Result{
			"answered": false,
			"error":    "no interactive host available; use `clarify` approach instead",
		}, nil
	}
	ans, err := host.AskUser(ctx, args.Question, args.Choices)
	if err != nil {
		vl := verbose.FromContext(ctx)
		vl.KV("ask_user_error", err.Error())
		return Result{
			"answered": false,
			"error":    err.Error(),
		}, nil
	}
	return Result{
		"answered": true,
		"answer":   ans,
	}, nil
}

// --- helpers ---

func runBoundedSubprocess(ctx context.Context, cmdline []string, timeout time.Duration, maxBytes int) string {
	if len(cmdline) == 0 {
		return ""
	}
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(subCtx, cmdline[0], cmdline[1:]...)
	// Never inherit stdin; a help probe that somehow waits on input
	// would hang the whole engine loop otherwise.
	cmd.Stdin = nil
	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		// Many tools exit non-zero while still printing useful help
		// (-h to a tool that doesn't support it prints usage + errno).
		// We keep whatever they wrote.
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			// Process couldn't even start (not-found, permission).
			return ""
		}
	}
	if len(outBytes) > maxBytes {
		outBytes = outBytes[:maxBytes]
	}
	return string(outBytes)
}

var binaryNameRE = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)

func looksLikeBinaryName(s string) bool {
	return s != "" && len(s) <= 64 && binaryNameRE.MatchString(s)
}

// looksLikeHelp is a cheap heuristic to decide whether a --help/-h
// invocation produced something meaningful versus just an error line.
// We treat "usage", "options", "commands" or a reasonably sized body
// as useful.
func looksLikeHelp(s string) bool {
	if len(strings.TrimSpace(s)) < 30 {
		return false
	}
	lower := strings.ToLower(s)
	for _, marker := range []string{"usage:", "usage ", "options:", "commands:", "subcommand", "flags:", "-h,", "--help"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	// Fall back on size: >200 bytes of output almost certainly isn't
	// a one-liner error.
	return len(s) > 200
}

// stripHTML is a minimal "give the model some text" pass for HTML
// responses. It intentionally isn't a full parser; it removes tags and
// collapses whitespace. If the page is heavily JS-rendered the output
// will be thin, which is a correct signal ("page content not
// crawlable") rather than a lie.
var (
	htmlScriptRE = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	htmlStyleRE  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	htmlTagRE    = regexp.MustCompile(`<[^>]+>`)
	wsRE         = regexp.MustCompile(`[\t\r\f ]+`)
	nlRE         = regexp.MustCompile(`\n{3,}`)
)

func stripHTML(s string) string {
	s = htmlScriptRE.ReplaceAllString(s, "")
	s = htmlStyleRE.ReplaceAllString(s, "")
	s = htmlTagRE.ReplaceAllString(s, " ")
	s = wsRE.ReplaceAllString(s, " ")
	s = nlRE.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }
