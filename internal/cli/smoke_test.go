package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var testBinary string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "intent-bin-*")
	if err != nil {
		log.Fatal(err)
	}
	testBinary = filepath.Join(tmp, "intent")
	out, err := exec.Command("go", "build", "-o", testBinary, "github.com/CoreyRDean/intent/cmd/intent").CombinedOutput()
	if err != nil {
		os.RemoveAll(tmp)
		log.Fatalf("build: %v\n%s", err, out)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

func run(t *testing.T, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	stateDir := t.TempDir()
	cacheDir := t.TempDir()
	return runWithDirs(t, stateDir, cacheDir, env, args...)
}

func runWithDirs(t *testing.T, stateDir, cacheDir string, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(testBinary, args...)
	baseEnv := []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
		"INTENT_STATE_DIR=" + stateDir,
		"INTENT_CACHE_DIR=" + cacheDir,
	}
	cmd.Env = append(baseEnv, env...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()
	exitCode = 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return
}

func TestVersionFlagLong(t *testing.T) {
	stdout, _, exitCode := run(t, nil, "--version")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("expected non-empty stdout from --version")
	}
}

func TestVersionFlagShort(t *testing.T) {
	stdout, _, exitCode := run(t, nil, "-V")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("expected non-empty stdout from -V")
	}
}

func TestVersionSubcommand(t *testing.T) {
	stdout, _, exitCode := run(t, nil, "version")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("expected non-empty stdout from version subcommand")
	}
}

func TestHelpFlag(t *testing.T) {
	stdout, _, exitCode := run(t, nil, "--help")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Fatalf("expected stdout to contain 'Usage:', got: %q", stdout)
	}
	if !strings.Contains(stdout, `i "ping google's dns"`) {
		t.Fatalf("expected help to include quoted apostrophe example, got: %q", stdout)
	}
	if !strings.Contains(stdout, "--context K=V") {
		t.Fatalf("expected help to include --context flag docs, got: %q", stdout)
	}
	if !strings.Contains(stdout, "--from-intent") {
		t.Fatalf("expected help to include --from-intent flag docs, got: %q", stdout)
	}
}

func TestNoArgs(t *testing.T) {
	stdout, _, exitCode := run(t, nil)
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Fatalf("expected stdout to contain 'Usage:', got: %q", stdout)
	}
}

func TestMockHello(t *testing.T) {
	stdout, _, exitCode := run(t, []string{"INTENT_FORCE_BACKEND=mock"}, "hello")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "hello") {
		t.Fatalf("expected stdout to contain 'hello', got: %q", stdout)
	}
}

func TestMockDryJSON(t *testing.T) {
	stdout, _, exitCode := run(t, []string{"INTENT_FORCE_BACKEND=mock"}, "--dry", "--json", "list", "files")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("expected valid JSON, got: %q; err: %v", stdout, err)
	}
	if _, ok := result["intent_response"]; !ok {
		t.Fatalf("expected JSON key 'intent_response', got keys: %v", jsonKeys(result))
	}
	if _, ok := result["exit_code"]; !ok {
		t.Fatalf("expected JSON key 'exit_code', got keys: %v", jsonKeys(result))
	}
}

func TestMockExecuteJSON(t *testing.T) {
	stdout, _, exitCode := run(t, []string{"INTENT_FORCE_BACKEND=mock"}, "--yes", "--json", "list", "files")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("expected valid JSON, got: %q; err: %v", stdout, err)
	}
	gotStdout, _ := result["stdout"].(string)
	if strings.TrimSpace(gotStdout) == "" {
		t.Fatalf("expected JSON field stdout to contain executed command output, got: %#v", result)
	}
	gotPrompt, _ := result["prompt"].(string)
	if gotPrompt != "list files" {
		t.Fatalf("expected JSON field prompt to preserve the original request, got %#v", result)
	}
	gotCWD, _ := result["cwd"].(string)
	if strings.TrimSpace(gotCWD) == "" {
		t.Fatalf("expected JSON field cwd to preserve the execution cwd, got %#v", result)
	}
}

func TestSafetyHardRejectDispatch(t *testing.T) {
	_, _, exitCode := run(t,
		[]string{"INTENT_FORCE_BACKEND=mock", "INTENT_MOCK_CMD=rm -rf /"},
		"--yes", "delete everything",
	)
	if exitCode != 4 {
		t.Fatalf("expected exit 4 (hard reject → refuse), got %d", exitCode)
	}
}

func TestFixReattemptsLastFailureUsingStoredStderr(t *testing.T) {
	stateDir := t.TempDir()
	cacheDir := t.TempDir()
	auditPath := filepath.Join(stateDir, "intent", "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o700); err != nil {
		t.Fatalf("mkdir audit dir: %v", err)
	}
	entry := map[string]any{
		"ts":               "2026-04-20T00:00:00Z",
		"id":               "deadbeefdeadbeef",
		"version":          "dev",
		"backend":          "mock",
		"model":            "mock",
		"prompt":           "list files",
		"user_decision":    "autorun",
		"executed_command": "bash -c printf 'boom\\n' >&2; exit 17",
		"exit_code":        17,
		"stderr_hash":      "sha256:test",
		"stderr_excerpt":   "boom\n",
	}
	row, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal audit entry: %v", err)
	}
	if err := os.WriteFile(auditPath, append(row, '\n'), 0o600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}

	stdout, _, exitCode := runWithDirs(
		t,
		stateDir,
		cacheDir,
		[]string{"INTENT_FORCE_BACKEND=mock"},
		"fix", "--yes", "--json",
	)
	if exitCode != 0 {
		t.Fatalf("expected fix to succeed, got %d", exitCode)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("expected fix output to be valid JSON, got %q; err: %v", stdout, err)
	}
	gotStdout, _ := result["stdout"].(string)
	if strings.TrimSpace(gotStdout) == "" {
		t.Fatalf("expected fix JSON stdout to contain rerun command output, got %#v", result)
	}
}

func TestHistoryReplayReexecutesPromptByID(t *testing.T) {
	stateDir := t.TempDir()
	cacheDir := t.TempDir()
	auditPath := filepath.Join(stateDir, "intent", "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o700); err != nil {
		t.Fatalf("mkdir audit dir: %v", err)
	}
	entry := map[string]any{
		"ts":            "2026-04-20T00:00:00Z",
		"id":            "cafebabecafebabe",
		"version":       "dev",
		"backend":       "mock",
		"model":         "mock",
		"prompt":        "list files",
		"user_decision": "autorun",
		"exit_code":     0,
	}
	row, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal audit entry: %v", err)
	}
	if err := os.WriteFile(auditPath, append(row, '\n'), 0o600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}

	stdout, _, exitCode := runWithDirs(
		t,
		stateDir,
		cacheDir,
		[]string{"INTENT_FORCE_BACKEND=mock"},
		"history", "replay", "cafeba", "--yes", "--json",
	)
	if exitCode != 0 {
		t.Fatalf("expected history replay to succeed, got %d", exitCode)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("expected replay output to be valid JSON, got %q; err: %v", stdout, err)
	}
	gotStdout, _ := result["stdout"].(string)
	if strings.TrimSpace(gotStdout) == "" {
		t.Fatalf("expected replay JSON stdout to contain rerun command output, got %#v", result)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	cacheDir := t.TempDir()
	baseEnv := []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
		"INTENT_STATE_DIR=" + stateDir,
		"INTENT_CACHE_DIR=" + cacheDir,
	}

	// set
	cmd1 := exec.Command(testBinary, "config", "set", "foo", "bar")
	cmd1.Env = baseEnv
	if out, err := cmd1.CombinedOutput(); err != nil {
		t.Fatalf("config set: %v\n%s", err, out)
	}

	// get
	cmd2 := exec.Command(testBinary, "config", "get", "foo")
	cmd2.Env = baseEnv
	out, err := cmd2.Output()
	if err != nil {
		t.Fatalf("config get: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "bar" {
		t.Fatalf("config get foo: got %q, want %q", got, "bar")
	}
}

func TestConfigPath(t *testing.T) {
	stdout, _, exitCode := run(t, nil, "config", "path")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("expected non-empty stdout from config path")
	}
}

func jsonKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
