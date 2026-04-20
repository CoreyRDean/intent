package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Launcher supervises a `llamafile --server` subprocess. It exposes the
// HTTP endpoint llamafile is bound to so the CLI can dial it directly.
//
// Restart policy: if llamafile exits with a non-zero code or is killed
// by anything other than us, we restart it up to MaxRestarts times within
// RestartWindow. Beyond that we give up; the user gets an honest "daemon
// died, see the logs" rather than a thrashing supervisor.
type Launcher struct {
	BinaryPath    string        // path to llamafile-VERSION
	ModelPath     string        // path to .gguf
	Host          string        // 127.0.0.1
	Port          int           // 18080
	ContextSize   int           // -c, 0 = llamafile default
	GPULayers     int           // -ngl, -1 = let llamafile decide
	StdoutLog     io.Writer     // where llamafile's stdout goes
	StderrLog     io.Writer     // where llamafile's stderr goes
	MaxRestarts   int           // default 5
	RestartWindow time.Duration // default 60s
	StartupGrace  time.Duration // how long to wait for /v1/models to respond

	mu        sync.Mutex
	cmd       *exec.Cmd
	pid       int32
	restarts  atomic.Int32
	stopped   atomic.Bool
	doneCh    chan struct{}
	restartTs []time.Time
}

// NewLauncher constructs a Launcher with sensible defaults.
func NewLauncher(binary, model string, host string, port int) *Launcher {
	return &Launcher{
		BinaryPath:    binary,
		ModelPath:     model,
		Host:          host,
		Port:          port,
		StdoutLog:     io.Discard,
		StderrLog:     os.Stderr,
		MaxRestarts:   5,
		RestartWindow: 60 * time.Second,
		StartupGrace:  60 * time.Second,
		GPULayers:     -1,
		doneCh:        make(chan struct{}),
	}
}

// Endpoint returns the http://host:port the supervised llamafile listens on.
func (l *Launcher) Endpoint() string {
	return fmt.Sprintf("http://%s:%d", l.Host, l.Port)
}

// PID returns the current llamafile PID (0 if not running).
func (l *Launcher) PID() int { return int(atomic.LoadInt32(&l.pid)) }

// Running reports whether the subprocess is alive.
func (l *Launcher) Running() bool { return l.PID() != 0 }

// Restarts returns the cumulative restart count.
func (l *Launcher) Restarts() int { return int(l.restarts.Load()) }

// Start launches llamafile and blocks until either:
//   - the HTTP /v1/models endpoint answers (success), OR
//   - StartupGrace expires (failure), OR
//   - llamafile exits before becoming ready (failure)
//
// On success, the Launcher's supervise goroutine is also running.
func (l *Launcher) Start(ctx context.Context) error {
	if err := l.spawn(ctx); err != nil {
		return err
	}
	if err := l.waitReady(ctx); err != nil {
		l.stop(syscall.SIGTERM)
		return fmt.Errorf("llamafile did not become ready: %w", err)
	}
	go l.supervise(ctx)
	return nil
}

// Wait blocks until the launcher's supervise loop exits.
func (l *Launcher) Wait() { <-l.doneCh }

// Stop signals the launcher to terminate and waits for it. Idempotent.
func (l *Launcher) Stop(timeout time.Duration) {
	if !l.stopped.CompareAndSwap(false, true) {
		return
	}
	l.stop(syscall.SIGTERM)
	select {
	case <-l.doneCh:
	case <-time.After(timeout):
		l.stop(syscall.SIGKILL)
		<-l.doneCh
	}
}

func (l *Launcher) spawn(ctx context.Context) error {
	args := []string{
		"--server",
		"-m", l.ModelPath,
		"--host", l.Host,
		"--port", fmt.Sprintf("%d", l.Port),
	}
	if l.ContextSize > 0 {
		args = append(args, "-c", fmt.Sprintf("%d", l.ContextSize))
	}
	if l.GPULayers >= 0 {
		args = append(args, "-ngl", fmt.Sprintf("%d", l.GPULayers))
	}

	// llamafile is an Actually Portable Executable (APE). On macOS the
	// kernel rejects APE binaries directly with "exec format error" —
	// the file's leading shell-script trampoline only fires when the
	// shell loads it. So we run it via /bin/sh on every Unix to keep the
	// invocation consistent and let the shell pick the right loader.
	//
	// Note: not exec.CommandContext — we manage lifecycle explicitly so
	// that a CLI command's ctx cancellation doesn't kill the daemon's
	// supervised subprocess.
	shArgs := append([]string{l.BinaryPath}, args...)
	cmd := exec.Command("/bin/sh", "-c", quoteShellArgs(shArgs), "intentd-llamafile")
	cmd.Stdout = l.StdoutLog
	cmd.Stderr = l.StderrLog
	// New process group so llamafile doesn't catch terminal signals
	// directed at the daemon.
	cmd.SysProcAttr = procAttrNewGroup()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", l.BinaryPath, err)
	}
	l.mu.Lock()
	l.cmd = cmd
	atomic.StoreInt32(&l.pid, int32(cmd.Process.Pid))
	l.mu.Unlock()
	return nil
}

func (l *Launcher) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(l.StartupGrace)
	url := l.Endpoint() + "/v1/models"
	cli := &http.Client{Timeout: 1 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		// If the subprocess died on us, fail fast.
		if !l.processAlive() {
			return fmt.Errorf("subprocess exited before ready")
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		resp, err := cli.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", l.StartupGrace)
}

func (l *Launcher) processAlive() bool {
	l.mu.Lock()
	cmd := l.cmd
	l.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return false
	}
	// Signal 0: existence check, no actual signal delivered.
	return cmd.Process.Signal(syscall.Signal(0)) == nil
}

func (l *Launcher) supervise(ctx context.Context) {
	defer close(l.doneCh)
	for {
		l.mu.Lock()
		cmd := l.cmd
		l.mu.Unlock()
		if cmd == nil {
			return
		}
		err := cmd.Wait()
		atomic.StoreInt32(&l.pid, 0)

		if l.stopped.Load() || ctx.Err() != nil {
			return
		}

		// Crash. Decide whether to restart.
		if err != nil {
			fmt.Fprintf(l.StderrLog, "intentd: llamafile exited: %v\n", err)
		}
		if !l.shouldRestart() {
			fmt.Fprintf(l.StderrLog, "intentd: too many restarts in %s; giving up\n", l.RestartWindow)
			return
		}
		l.restarts.Add(1)
		fmt.Fprintf(l.StderrLog, "intentd: restarting llamafile (attempt %d)\n", l.restarts.Load())
		// Brief backoff so we don't hot-loop.
		time.Sleep(time.Second)
		if err := l.spawn(ctx); err != nil {
			fmt.Fprintf(l.StderrLog, "intentd: respawn failed: %v\n", err)
			return
		}
		if err := l.waitReady(ctx); err != nil {
			fmt.Fprintf(l.StderrLog, "intentd: respawn not ready: %v\n", err)
			l.stop(syscall.SIGTERM)
			return
		}
	}
}

// shouldRestart returns true if we are within the restart budget.
func (l *Launcher) shouldRestart() bool {
	now := time.Now()
	cutoff := now.Add(-l.RestartWindow)
	kept := l.restartTs[:0]
	for _, t := range l.restartTs {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.restartTs = append(kept, now)
	return len(l.restartTs) <= l.MaxRestarts
}

// quoteShellArgs renders argv as a single shell command string with
// each argument single-quoted. We never embed user-supplied unescaped
// strings here, but doing it correctly is cheap insurance.
func quoteShellArgs(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		// Replace each ' with '\''.
		escaped := ""
		for _, r := range a {
			if r == '\'' {
				escaped += `'\''`
			} else {
				escaped += string(r)
			}
		}
		out += "'" + escaped + "'"
	}
	return out
}

func (l *Launcher) stop(sig syscall.Signal) {
	l.mu.Lock()
	cmd := l.cmd
	l.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid

	// llamafile is an Actually Portable Executable. APE binaries on
	// macOS work like this: the bytes are simultaneously a PE header
	// (rejected by the kernel) and a shell script (interpreted by sh
	// when execve fails). The script then mmaps a temp-extracted
	// Mach-O and re-execs into it via `posix_spawn`, which in practice
	// FORKS off a worker process whose parent becomes our intent
	// daemon (the original sh wrapper exits). So:
	//
	// - cmd.Process.Pid points at the long-dead sh wrapper.
	// - The actual llamafile is reparented to our daemon (os.Getpid()).
	// - It also lives in its own process group via setsid.
	//
	// We therefore signal four populations to be sure:
	//   1) the original spawned PID (no-op if already gone),
	//   2) the spawned PID's process group (no-op if separated),
	//   3) every descendant of *us* that runs our llamafile binary,
	//   4) every such descendant's own process group.
	_ = cmd.Process.Signal(sig)
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid != 0 && pgid != os.Getpid() {
		_ = syscall.Kill(-pgid, sig)
	}
	for _, p := range descendantsRunning(os.Getpid(), l.BinaryPath) {
		_ = syscall.Kill(p, sig)
		if pgid, err := syscall.Getpgid(p); err == nil && pgid != 0 && pgid != os.Getpid() {
			_ = syscall.Kill(-pgid, sig)
		}
	}
}

// descendantsRunning returns every descendant of root whose command
// line contains needle. We filter on needle so that signaling does
// not accidentally hit unrelated processes that happen to share the
// daemon as an ancestor (e.g. user shells launched from `i daemon
// start` in a terminal).
func descendantsRunning(root int, needle string) []int {
	if _, err := exec.LookPath("pgrep"); err != nil {
		return nil
	}
	candidates := allDescendants(root)
	if len(candidates) == 0 || needle == "" {
		return candidates
	}
	out := candidates[:0]
	for _, p := range candidates {
		// `ps -o command= -p PID` prints just the command line.
		b, err := exec.Command("ps", "-o", "command=", "-p", fmt.Sprintf("%d", p)).Output()
		if err != nil {
			continue
		}
		if bytesContains(b, needle) {
			out = append(out, p)
		}
	}
	return out
}

func allDescendants(root int) []int {
	seen := map[int]struct{}{root: {}}
	queue := []int{root}
	var out []int
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		b, err := exec.Command("pgrep", "-P", fmt.Sprintf("%d", p)).Output()
		if err != nil {
			continue
		}
		for _, line := range bytesLines(b) {
			var child int
			_, _ = fmt.Sscanf(line, "%d", &child)
			if child <= 0 {
				continue
			}
			if _, ok := seen[child]; ok {
				continue
			}
			seen[child] = struct{}{}
			out = append(out, child)
			queue = append(queue, child)
		}
	}
	return out
}

func bytesContains(b []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	n := []byte(needle)
	for i := 0; i+len(n) <= len(b); i++ {
		if string(b[i:i+len(n)]) == needle {
			return true
		}
	}
	return false
}

func bytesLines(b []byte) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, string(b[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}
