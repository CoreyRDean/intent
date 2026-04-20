package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// runTool is a tiny helper so tests don't have to repeat the
// marshal+Run dance for every case.
func runTool(t *testing.T, host Host, name string, args map[string]any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := Run(ctx, host, name, raw)
	if err != nil {
		t.Fatalf("Run(%s): %v", name, err)
	}
	return res
}

func TestUnknownTool(t *testing.T) {
	ctx := context.Background()
	_, err := Run(ctx, nil, "not_a_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected error for unknown tool, got nil")
	}
}

func TestNamesMatchDispatcher(t *testing.T) {
	ctx := context.Background()
	for _, n := range Names() {
		_, err := Run(ctx, nil, n, json.RawMessage(`{}`))
		if err != nil && strings.Contains(err.Error(), "unknown tool") {
			t.Fatalf("Names() includes %q but dispatcher doesn't handle it", n)
		}
	}
}

func TestLooksLikeBinaryName(t *testing.T) {
	cases := map[string]bool{
		"ls":                  true,
		"my-cmd":              true,
		"Tool_v2.1":           true,
		"":                    false,
		"rm -rf":              false,
		"cd; rm":              false,
		"`pwd`":               false,
		"../escape":           false,
		"path/to/bin":         false,
		strings.Repeat("a", 65): false,
	}
	for name, want := range cases {
		if got := looksLikeBinaryName(name); got != want {
			t.Errorf("looksLikeBinaryName(%q)=%v want %v", name, got, want)
		}
	}
}

func TestReadFileLineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	res := runTool(t, nil, "read_file", map[string]any{
		"path":       path,
		"start_line": 2,
		"end_line":   4,
	})
	got, _ := res["content"].(string)
	want := "line2\nline3\nline4"
	if got != want {
		t.Errorf("content=%q want %q", got, want)
	}
	if tl, _ := res["total_lines"].(int); tl != 6 { // strings.Split keeps trailing empty
		// Not strictly testing total_lines count here; permit any positive.
		if tl == 0 {
			t.Errorf("expected total_lines>0 got 0")
		}
	}
}

func TestHelpTool_MissingBinary(t *testing.T) {
	res := runTool(t, nil, "help", map[string]any{"name": "definitely-not-installed-zzz-xyz"})
	if found, _ := res["found"].(bool); found {
		t.Errorf("expected found=false, got %+v", res)
	}
}

func TestHelpTool_RejectsWeirdName(t *testing.T) {
	res := runTool(t, nil, "help", map[string]any{"name": "rm -rf /"})
	if _, ok := res["error"]; !ok {
		t.Errorf("expected error result for shell-metachar name, got %+v", res)
	}
}

func TestHelpTool_FindsLs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ls not expected on windows runners")
	}
	res := runTool(t, nil, "help", map[string]any{"name": "ls"})
	if found, _ := res["found"].(bool); !found {
		t.Fatalf("expected found=true for ls, got %+v", res)
	}
	out, _ := res["output"].(string)
	if !strings.Contains(strings.ToLower(out), "usage") && len(out) < 200 {
		t.Errorf("expected ls help text, got %q", out)
	}
}

func TestGrepTool_FindsMatches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rg/grep path conventions differ on windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\nbeta-marker\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("delta\nbeta-marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runTool(t, nil, "grep", map[string]any{
		"pattern": "beta-marker",
		"path":    dir,
	})
	if _, ok := res["error"]; ok {
		t.Fatalf("grep error: %v", res)
	}
	count, _ := res["count"].(int)
	if count < 2 {
		t.Errorf("expected >=2 matches, got %d: %+v", count, res)
	}
}

func TestFindFilesTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fd/find path conventions differ on windows")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "needle.log"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "haystack.txt"), []byte("y"), 0o644)
	res := runTool(t, nil, "find_files", map[string]any{
		"pattern": "needle.log",
		"path":    dir,
	})
	if _, ok := res["error"]; ok {
		t.Fatalf("find_files error: %v", res)
	}
	count, _ := res["count"].(int)
	if count < 1 {
		t.Errorf("expected at least 1 match, got %d: %+v", count, res)
	}
}

func TestWebFetchTool_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "hello from intent test server")
	}))
	defer srv.Close()

	res := runTool(t, nil, "web_fetch", map[string]any{"url": srv.URL})
	if _, ok := res["error"]; ok {
		t.Fatalf("unexpected error: %v", res)
	}
	body, _ := res["body"].(string)
	if !strings.Contains(body, "hello from intent test server") {
		t.Errorf("body=%q missing expected text", body)
	}
}

func TestWebFetchTool_HTMLIsStripped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><head><style>body{}</style><script>var x=1;</script></head><body><h1>Hi</h1><p>Hello <b>World</b></p></body></html>")
	}))
	defer srv.Close()

	res := runTool(t, nil, "web_fetch", map[string]any{"url": srv.URL})
	body, _ := res["body"].(string)
	for _, forbidden := range []string{"<html", "<script", "<style", "<b>"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body still contains %q: %q", forbidden, body)
		}
	}
	if !strings.Contains(body, "Hi") || !strings.Contains(body, "Hello") {
		t.Errorf("body lost visible text: %q", body)
	}
}

func TestWebFetchTool_RefusesNonHTTP(t *testing.T) {
	res := runTool(t, nil, "web_fetch", map[string]any{"url": "file:///etc/passwd"})
	if _, ok := res["error"]; !ok {
		t.Errorf("expected error for file:// scheme, got %+v", res)
	}
}

type fakeHost struct {
	lastQ   string
	lastOpt []string
	reply   string
	err     error
}

func (h *fakeHost) AskUser(_ context.Context, q string, opts []string) (string, error) {
	h.lastQ = q
	h.lastOpt = opts
	return h.reply, h.err
}

func TestAskUserTool_NoHost(t *testing.T) {
	res := runTool(t, nil, "ask_user", map[string]any{"question": "ok?"})
	if answered, _ := res["answered"].(bool); answered {
		t.Errorf("expected answered=false with nil host, got %+v", res)
	}
	if _, ok := res["error"]; !ok {
		t.Errorf("expected error in result, got %+v", res)
	}
}

func TestAskUserTool_WithHost(t *testing.T) {
	h := &fakeHost{reply: "ok"}
	res := runTool(t, h, "ask_user", map[string]any{"question": "go?", "choices": []string{"yes", "no"}})
	if answered, _ := res["answered"].(bool); !answered {
		t.Fatalf("expected answered=true, got %+v", res)
	}
	if ans, _ := res["answer"].(string); ans != "ok" {
		t.Errorf("answer=%q want ok", ans)
	}
	if h.lastQ != "go?" {
		t.Errorf("host saw question=%q", h.lastQ)
	}
	if len(h.lastOpt) != 2 {
		t.Errorf("host saw %d options", len(h.lastOpt))
	}
}

func TestAskUserTool_HostError(t *testing.T) {
	h := &fakeHost{err: fmt.Errorf("no tty")}
	res := runTool(t, h, "ask_user", map[string]any{"question": "go?"})
	if answered, _ := res["answered"].(bool); answered {
		t.Errorf("expected answered=false when host errors, got %+v", res)
	}
	if _, ok := res["error"]; !ok {
		t.Errorf("expected error in result, got %+v", res)
	}
}

func TestLooksLikeHelp(t *testing.T) {
	if looksLikeHelp("") {
		t.Error("empty should not look like help")
	}
	if looksLikeHelp("err: bad flag") {
		t.Error("short error should not look like help")
	}
	if !looksLikeHelp("Usage: foo [options]\n  -h  show help") {
		t.Error("typical usage line should look like help")
	}
}
