// Package tui contains terminal rendering: spinners, the confirm prompt,
// and the response renderer. Everything here respects the IsTTY decision —
// no decoration is emitted when stdout is a pipe.
package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// IsTTY reports whether the given file is a terminal.
// Implemented with the standard library to avoid a dep.
func IsTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// Color helpers. Disabled when stdout is not a TTY or NO_COLOR is set.
type Style struct {
	Color  bool
	Stderr io.Writer // where status lines go (always stderr in TTY mode)
}

func DefaultStyle() Style {
	return Style{
		Color:  IsTTY(os.Stdout) && os.Getenv("NO_COLOR") == "",
		Stderr: os.Stderr,
	}
}

func (s Style) c(code, str string) string {
	if !s.Color {
		return str
	}
	return "\x1b[" + code + "m" + str + "\x1b[0m"
}

func (s Style) Dim(t string) string    { return s.c("2", t) }
func (s Style) Bold(t string) string   { return s.c("1", t) }
func (s Style) Green(t string) string  { return s.c("32", t) }
func (s Style) Yellow(t string) string { return s.c("33", t) }
func (s Style) Red(t string) string    { return s.c("31", t) }
func (s Style) Cyan(t string) string   { return s.c("36", t) }

// Spinner is a single-line, stderr-only progress indicator.
// All methods are safe to call on a nil spinner (no-op).
type Spinner struct {
	style    Style
	frames   []string
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	label    atomic.Pointer[string]
	started  atomic.Bool
}

// NewSpinner returns a spinner that renders to stderr if and only if stderr
// is a TTY. Otherwise it returns nil — all subsequent calls become no-ops.
func NewSpinner(style Style) *Spinner {
	if !IsTTY(os.Stderr) {
		return nil
	}
	return &Spinner{
		style:  style,
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Start begins animating with the initial label.
func (s *Spinner) Start(label string) {
	if s == nil {
		return
	}
	if s.started.Swap(true) {
		s.SetLabel(label)
		return
	}
	l := label
	s.label.Store(&l)
	go s.loop()
}

// SetLabel updates the visible text without restarting.
func (s *Spinner) SetLabel(label string) {
	if s == nil {
		return
	}
	l := label
	s.label.Store(&l)
}

// Stop halts the spinner and clears its line. Idempotent — safe to
// call from both an explicit cleanup site and a `defer` on the same
// path, which is the natural pattern when a function might exit early.
func (s *Spinner) Stop() {
	if s == nil || !s.started.Load() {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stop)
		<-s.done
		fmt.Fprint(s.style.Stderr, "\r\x1b[K")
	})
}

// Suspend temporarily halts animation and returns a func that restarts
// it on the same label. Used by interactive tools (e.g. ask_user) that
// need uncluttered stderr while they prompt the user.
//
// Unlike Stop, Suspend is reversible: the returned function spawns a
// fresh animation goroutine without needing a new Spinner allocation.
// If the spinner never started, Suspend is a no-op and the returned
// func does nothing.
func (s *Spinner) Suspend() func() {
	if s == nil || !s.started.Load() {
		return func() {}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Capture the last label so we can resume on the same text.
	var label string
	if l := s.label.Load(); l != nil {
		label = *l
	}
	// Close the current stop channel and wait for the loop to exit
	// and clear the line. We do this without going through stopOnce
	// because this is a pause, not a terminal stop -- Stop() must
	// still be callable afterwards.
	close(s.stop)
	<-s.done
	fmt.Fprint(s.style.Stderr, "\r\x1b[K")
	s.started.Store(false)

	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Re-arm the channels and flip started back on.
		s.stop = make(chan struct{})
		s.done = make(chan struct{})
		s.stopOnce = sync.Once{}
		s.started.Store(true)
		l := label
		s.label.Store(&l)
		go s.loop()
	}
}

func (s *Spinner) loop() {
	defer close(s.done)
	t := time.NewTicker(80 * time.Millisecond)
	defer t.Stop()
	i := 0
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			label := ""
			if l := s.label.Load(); l != nil {
				label = *l
			}
			frame := s.frames[i%len(s.frames)]
			fmt.Fprintf(s.style.Stderr, "\r\x1b[K%s %s",
				s.style.Dim(frame), s.style.Dim(label))
			i++
		}
	}
}

// RiskBadge returns a colored short label for a risk level.
func (s Style) RiskBadge(risk string) string {
	switch risk {
	case "safe":
		return s.Green("safe")
	case "network":
		return s.Cyan("network")
	case "mutates":
		return s.Yellow("mutates")
	case "destructive":
		return s.Red("destructive")
	case "sudo":
		return s.Red("sudo")
	default:
		return risk
	}
}

// PromptDecision is the user's choice from the confirm prompt.
type PromptDecision int

const (
	DecisionConfirm PromptDecision = iota
	DecisionPreview
	DecisionEdit
	DecisionCancel
)

// Confirm reads one keystroke (line) from stdin and maps it to a decision.
// Falls back to line-based input where raw keystrokes aren't available.
func Confirm(in io.Reader, out io.Writer) PromptDecision {
	fmt.Fprint(out, "  [Enter] run · [p] preview · [e] edit · [n] cancel: ")
	r := bufio.NewReader(in)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	switch line {
	case "", "y", "yes", "r", "run":
		return DecisionConfirm
	case "p", "preview":
		return DecisionPreview
	case "e", "edit":
		return DecisionEdit
	default:
		return DecisionCancel
	}
}

// EditLine reads a single edited line from the user, prefilled with `initial`.
// On terminals that don't support readline-style editing we just print the
// initial line and read the user's replacement.
func EditLine(in io.Reader, out io.Writer, initial string) (string, error) {
	fmt.Fprintf(out, "  edit > %s\n  > ", initial)
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return initial, nil
	}
	return line, nil
}
