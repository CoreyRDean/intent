package cli

import (
	"os"
	"reflect"
	"testing"
)

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
