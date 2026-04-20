// Package tools implements the read-only tool catalog the model may invoke
// during a multi-step turn. See docs/SPEC.md §2.3.
//
// Tools never mutate state and never require user confirmation. Adding a
// non-read-only tool is a spec amendment.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Result is the JSON-serializable return from a tool execution.
type Result map[string]any

// Run dispatches a tool by name with the given arguments. Returns a result
// and a synthesized message ready to append to the conversation.
func Run(ctx context.Context, name string, argsRaw json.RawMessage) (Result, error) {
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
	default:
		return nil, fmt.Errorf("unknown tool: %q", name)
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
	if args.MaxEntries <= 0 {
		args.MaxEntries = 200
	}
	entries, err := os.ReadDir(args.Path)
	if err != nil {
		return Result{"error": err.Error()}, nil
	}
	out := make([]map[string]any, 0, len(entries))
	for i, e := range entries {
		if i >= args.MaxEntries {
			break
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
		out = append(out, map[string]any{
			"name": e.Name(),
			"type": typ,
			"size": size,
		})
	}
	return Result{"entries": out, "truncated": len(entries) > args.MaxEntries}, nil
}

func readFile(raw json.RawMessage) (Result, error) {
	var args struct {
		Path     string `json:"path"`
		MaxBytes int    `json:"max_bytes"`
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
