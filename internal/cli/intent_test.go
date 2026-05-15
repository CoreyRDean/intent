package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestApplyIntentTTYDefaults_AutoConfirmsWhenStdoutIsNotTTY(t *testing.T) {
	fl := &intentFlags{}
	applyIntentTTYDefaults(fl, false, true)
	if !fl.yes {
		t.Fatalf("expected non-TTY stdout to enable auto-confirm semantics")
	}
	if !fl.quiet {
		t.Fatalf("expected non-TTY stdout to force quiet mode")
	}
	if fl.stdoutTTY {
		t.Fatalf("expected stdoutTTY to be recorded as false")
	}
	if !fl.stdinTTY {
		t.Fatalf("expected stdinTTY to be recorded as true")
	}
}

func TestApplyIntentTTYDefaults_AutoConfirmsWhenStdinIsPiped(t *testing.T) {
	fl := &intentFlags{}
	applyIntentTTYDefaults(fl, true, false)
	if !fl.yes {
		t.Fatalf("expected piped stdin to enable auto-confirm semantics")
	}
	if !fl.stdoutTTY {
		t.Fatalf("expected stdoutTTY to be recorded as true")
	}
	if fl.stdinTTY {
		t.Fatalf("expected stdinTTY to be recorded as false")
	}
}

func TestCanPromptInteractively_RequiresFullTTYSurface(t *testing.T) {
	if !canPromptInteractively(true, true, true) {
		t.Fatalf("expected full TTY surface to allow prompting")
	}
	for _, tc := range []struct {
		name      string
		stdoutTTY bool
		stdinTTY  bool
		stderrTTY bool
	}{
		{name: "stdout piped", stdoutTTY: false, stdinTTY: true, stderrTTY: true},
		{name: "stdin piped", stdoutTTY: true, stdinTTY: false, stderrTTY: true},
		{name: "stderr redirected", stdoutTTY: true, stdinTTY: true, stderrTTY: false},
	} {
		if canPromptInteractively(tc.stdoutTTY, tc.stdinTTY, tc.stderrTTY) {
			t.Fatalf("expected %s to disable prompting", tc.name)
		}
	}
}

func TestParseIntentFlags_ContextRepeatable(t *testing.T) {
	fl, err := parseIntentFlags([]string{
		"--context", "repo=core",
		"--context=branch=dev",
		"list", "files",
	})
	if err != nil {
		t.Fatalf("parseIntentFlags returned error: %v", err)
	}
	want := []string{"repo=core", "branch=dev"}
	if !reflect.DeepEqual([]string(fl.context), want) {
		t.Fatalf("context mismatch: got %v want %v", []string(fl.context), want)
	}
}

func TestParseIntentFlags_FromIntentAutoEnablesJSON(t *testing.T) {
	t.Setenv("INTENT_PIPE_FROM", "intent")
	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })

	fl, err := parseIntentFlags([]string{"list", "files"})
	if err != nil {
		t.Fatalf("parseIntentFlags returned error: %v", err)
	}
	if !fl.fromIntent {
		t.Fatalf("from-intent should auto-enable when INTENT_PIPE_FROM=intent and stdin is piped")
	}
	if !fl.json {
		t.Fatalf("json should auto-enable when INTENT_PIPE_FROM=intent and stdin is piped")
	}
}

func TestFormatStdinForPrompt_PlainStdin(t *testing.T) {
	got := formatStdinForPrompt("hello world", false)
	if !strings.Contains(got, "[stdin contents follow]") {
		t.Fatalf("expected raw stdin framing, got: %q", got)
	}
	if !strings.Contains(got, "hello world") {
		t.Fatalf("expected stdin body in output, got: %q", got)
	}
	if strings.Contains(got, "upstream intent") {
		t.Fatalf("unexpected --from-intent framing for plain stdin: %q", got)
	}
}

func TestFormatStdinForPrompt_FromIntentNonJSON(t *testing.T) {
	got := formatStdinForPrompt("some upstream chatter", true)
	if !strings.Contains(got, "[upstream intent result follows]") {
		t.Fatalf("expected upstream-intent raw framing, got: %q", got)
	}
	if !strings.Contains(got, "some upstream chatter") {
		t.Fatalf("expected stdin body in output, got: %q", got)
	}
	if strings.Contains(got, "[stdin contents follow]") {
		t.Fatalf("should not use plain-stdin framing when --from-intent is set: %q", got)
	}
}

func TestFormatStdinForPrompt_FromIntentEnvelopeUnpacks(t *testing.T) {
	envelope := `{
		"prompt": "list files in ~/dir",
		"cwd": "/Users/coreyrdean/project",
		"intent_response": {
			"intent_summary": "Check disk usage",
			"approach": "command",
			"command": "df -h",
			"description": "Report filesystem usage",
			"risk": "safe",
			"expected_runtime": "instant",
			"confidence": "high"
		},
		"exit_code": 0,
		"stdout": "Filesystem 10% used\n"
	}`
	got := formatStdinForPrompt(envelope, true)
	if !strings.Contains(got, "[upstream intent result]") {
		t.Fatalf("expected envelope framing, got: %q", got)
	}
	if !strings.Contains(got, "prompt: list files in ~/dir") {
		t.Fatalf("expected prompt to be extracted, got: %q", got)
	}
	if !strings.Contains(got, "cwd: /Users/coreyrdean/project") {
		t.Fatalf("expected cwd to be extracted, got: %q", got)
	}
	if !strings.Contains(got, "summary: Check disk usage") {
		t.Fatalf("expected summary to be extracted, got: %q", got)
	}
	if !strings.Contains(got, "command: df -h") {
		t.Fatalf("expected command to be extracted, got: %q", got)
	}
	if !strings.Contains(got, "exit_code: 0") {
		t.Fatalf("expected exit_code to be extracted, got: %q", got)
	}
	if !strings.Contains(got, "Filesystem 10% used") {
		t.Fatalf("expected stdout to be extracted, got: %q", got)
	}
}

func TestFormatStdinForPrompt_FromIntentFallsBackOnNonEnvelopeJSON(t *testing.T) {
	got := formatStdinForPrompt(`{"unrelated":"payload"}`, true)
	if !strings.Contains(got, "[upstream intent result follows]") {
		t.Fatalf("expected fallback framing for non-envelope JSON, got: %q", got)
	}
}

func TestFormatStdinForPrompt_FromIntentEnvelopeKeepsPathContext(t *testing.T) {
	envelope := `{
		"prompt": "list files in ~/dir",
		"cwd": "/Users/coreyrdean/project",
		"intent_response": {
			"intent_summary": "List files in the requested directory.",
			"approach": "command",
			"command": "ls ~/dir",
			"description": "List the files in ~/dir.",
			"risk": "safe",
			"expected_runtime": "instant",
			"confidence": "high"
		},
		"exit_code": 0,
		"stdout": "file.md\n"
	}`
	got := formatStdinForPrompt(envelope, true)
	for _, needle := range []string{
		"prompt: list files in ~/dir",
		"cwd: /Users/coreyrdean/project",
		"command: ls ~/dir",
		"stdout: file.md",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected formatted upstream context to contain %q, got: %q", needle, got)
		}
	}
}

func TestFormatStdinForPrompt_Empty(t *testing.T) {
	if got := formatStdinForPrompt("", true); got != "" {
		t.Fatalf("expected empty string for empty stdin, got: %q", got)
	}
	if got := formatStdinForPrompt("", false); got != "" {
		t.Fatalf("expected empty string for empty stdin, got: %q", got)
	}
}
