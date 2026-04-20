package verbose

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestNilLoggerIsNoOp(t *testing.T) {
	var l *Logger
	l.Section("nope")
	l.KV("k", "v")
	l.JSON("t", map[string]int{"a": 1})
	l.RawBytes("t", []byte("hi"))
	l.Printf("x=%d", 7)
	if l.Enabled() {
		t.Fatalf("nil Logger should not be Enabled")
	}
	if got := l.Elapsed(); got != 0 {
		t.Fatalf("nil Logger Elapsed should be 0, got %v", got)
	}
}

func TestLoggerWritesPrefixedLines(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, false)
	l.Section("title")
	l.KV("backend", "llamafile")
	l.JSON("payload", map[string]any{"a": 1})
	got := buf.String()
	if !strings.Contains(got, "title") {
		t.Fatalf("missing section title: %q", got)
	}
	if !strings.Contains(got, "backend: llamafile") {
		t.Fatalf("missing KV: %q", got)
	}
	if !strings.Contains(got, "\"a\": 1") {
		t.Fatalf("missing JSON body: %q", got)
	}
	// Every non-empty output line must carry the verbose prefix.
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "[v ") {
			t.Fatalf("line missing verbose prefix: %q", line)
		}
	}
}

func TestContextRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, false)
	ctx := WithLogger(context.Background(), l)
	if got := FromContext(ctx); got != l {
		t.Fatalf("FromContext: want %p, got %p", l, got)
	}
	if got := FromContext(context.Background()); got != nil {
		t.Fatalf("FromContext on empty ctx should be nil, got %v", got)
	}
	// WithLogger(ctx, nil) must not bury a previously-stored logger.
	ctx2 := WithLogger(ctx, nil)
	if got := FromContext(ctx2); got != l {
		t.Fatalf("WithLogger(nil) must not overwrite existing logger")
	}
}
