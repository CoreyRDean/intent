// Package verbose implements the -v / --verbose observability layer.
//
// When enabled, a Logger writes section-prefixed, pretty-printed events
// to stderr: the raw model request, the raw model response, tool calls,
// phases, external command invocations, and anything else a caller
// chooses to emit. When disabled (the common case), every method on a
// nil Logger is a cheap no-op, so callers can write unconditional
// logging statements without guarding each one. This is intentional —
// it keeps the instrumentation dense in the hot paths without
// conditional clutter at every site.
//
// Design notes:
//   - The Logger is retrieved from context via FromContext, so wiring
//     is cheap and avoids threading an extra parameter through every
//     internal signature.
//   - Output goes to stderr only. Normal stdout (the model response,
//     proposed command, explanation, etc.) is never touched, so piping
//     `i -v <prompt> | pbcopy` still captures exactly what the user
//     would capture without -v.
//   - Color defaults to ON when stderr is a TTY and NO_COLOR is unset.
package verbose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Logger is a stderr-only observability sink. A nil *Logger is valid
// and is a no-op for every method; callers never need to nil-check.
type Logger struct {
	w     io.Writer
	mu    sync.Mutex
	color bool
	seq   atomic.Int64
	start time.Time
}

// New creates a Logger that writes to w. Pass os.Stderr in practice.
// When color is true the per-line prefix is rendered dim via ANSI so
// verbose output is visually distinct from the real program output.
// Passing a nil writer yields a nil Logger (no-op).
func New(w io.Writer, color bool) *Logger {
	if w == nil {
		return nil
	}
	return &Logger{
		w:     w,
		color: color,
		start: time.Now(),
	}
}

// Default constructs a Logger appropriate for the current process when
// enabled is true. Returns nil when enabled is false so callers can
// unconditionally pass the result to WithLogger / FromContext.
func Default(enabled bool) *Logger {
	if !enabled {
		return nil
	}
	color := os.Getenv("NO_COLOR") == "" && isTTY(os.Stderr)
	return New(os.Stderr, color)
}

// Enabled reports whether this Logger will write. A nil Logger is not
// enabled.
func (l *Logger) Enabled() bool { return l != nil }

type ctxKey struct{}

// WithLogger stores l on ctx. Passing a nil Logger is a no-op that
// still returns a usable context, matching the behaviour callers want.
func WithLogger(ctx context.Context, l *Logger) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext retrieves a Logger from ctx, or returns nil when no
// logger is present. Nil is a valid no-op Logger.
func FromContext(ctx context.Context) *Logger {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(ctxKey{}).(*Logger)
	return v
}

// Section prints a titled separator so logs are easy to scan when the
// caller is about to emit several related lines.
func (l *Logger) Section(title string) {
	if l == nil {
		return
	}
	l.write(fmt.Sprintf("─── %s ───", title))
}

// KV prints a single `key: value` line. Used for short, atomic facts
// (backend name, temperature, elapsed, etc).
func (l *Logger) KV(key string, value any) {
	if l == nil {
		return
	}
	l.write(fmt.Sprintf("%s: %v", key, value))
}

// JSON pretty-prints v as indented JSON with a tag. Intended for
// structured payloads (messages arrays, response envelopes, tool
// results). Marshal errors are surfaced inline rather than dropped.
func (l *Logger) JSON(tag string, v any) {
	if l == nil {
		return
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		l.write(fmt.Sprintf("%s: <marshal error: %v>", tag, err))
		return
	}
	l.write(fmt.Sprintf("%s:\n%s", tag, string(b)))
}

// RawBytes prints raw bytes assumed to be UTF-8 text, with a tag and
// a byte count. Used for pre-formatted JSON schemas and raw HTTP
// bodies where re-marshalling would be lossy.
func (l *Logger) RawBytes(tag string, b []byte) {
	if l == nil {
		return
	}
	l.write(fmt.Sprintf("%s (%d bytes):\n%s", tag, len(b), string(b)))
}

// Printf is the catch-all for arbitrary formatted lines. Prefer KV
// or JSON when the data is structured.
func (l *Logger) Printf(format string, a ...any) {
	if l == nil {
		return
	}
	l.write(fmt.Sprintf(format, a...))
}

// Elapsed returns the wall-clock time since the Logger was created,
// rounded to ms. Handy for callers that want a consistent time basis
// across multiple log sites.
func (l *Logger) Elapsed() time.Duration {
	if l == nil {
		return 0
	}
	return time.Since(l.start).Round(time.Millisecond)
}

func (l *Logger) write(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := time.Since(l.start).Round(time.Millisecond)
	n := l.seq.Add(1)
	prefix := fmt.Sprintf("[v %04d %9s]", n, t)
	if l.color {
		prefix = "\x1b[2m" + prefix + "\x1b[0m"
	}
	for _, line := range strings.Split(msg, "\n") {
		fmt.Fprintf(l.w, "%s %s\n", prefix, line)
	}
}

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
