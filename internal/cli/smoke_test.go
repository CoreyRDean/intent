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

func TestSafetyHardRejectDispatch(t *testing.T) {
	_, _, exitCode := run(t,
		[]string{"INTENT_FORCE_BACKEND=mock", "INTENT_MOCK_CMD=rm -rf /"},
		"--yes", "delete everything",
	)
	if exitCode != 4 {
		t.Fatalf("expected exit 4 (hard reject → refuse), got %d", exitCode)
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
