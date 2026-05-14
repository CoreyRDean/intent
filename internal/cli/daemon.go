package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/daemon"
	"github.com/CoreyRDean/intent/internal/models"
	intentruntime "github.com/CoreyRDean/intent/internal/runtime"
	"github.com/CoreyRDean/intent/internal/state"
)

const daemonUsage = "usage: i daemon (start | stop | status | logs | install | uninstall)"

// daemonLabel is the launchd / systemd unit name. Stable across versions.
const daemonLabel = "com.coreyrdean.intent"

func cmdDaemon(ctx context.Context, args []string) int {
	if len(args) == 0 {
		errf(daemonUsage)
		return 1
	}
	dirs, err := state.Resolve()
	if err != nil {
		errf("daemon: %v", err)
		return 3
	}
	cfg, _ := config.Load(dirs.ConfigPath())

	switch args[0] {
	case "--help", "-h", "help":
		fmt.Println(daemonUsage)
		return 0
	case "start":
		return daemonStart(ctx, dirs, cfg, args[1:])
	case "stop":
		return daemonStop(dirs)
	case "status":
		return daemonStatus(dirs)
	case "logs":
		return daemonLogs(dirs)
	case "install":
		return daemonInstall(dirs)
	case "uninstall":
		return daemonUninstall(dirs)
	default:
		errf("unknown subcommand: %q", args[0])
		return 1
	}
}

// daemonStart is the user-visible `i daemon start`. By default it spawns
// itself in the background (re-execs with --foreground), waits for the
// control socket to come up, prints a one-line "started" message, and
// returns — so the user gets their prompt back in well under a second.
//
// `--foreground` (or `--attach`) keeps the process attached to the
// terminal, which is what launchd / systemd want and what `i daemon
// logs -f` style debugging needs. The env var INTENTD_FOREGROUND is
// the same switch in env form, so service files don't have to know
// about the flag.
func daemonStart(ctx context.Context, dirs state.Dirs, cfg *config.Config, args []string) int {
	foreground := os.Getenv("INTENTD_FOREGROUND") == "1"
	for _, a := range args {
		switch a {
		case "--foreground", "--attach", "-f":
			foreground = true
		case "--background", "-b":
			foreground = false
		}
	}
	if !foreground {
		return daemonSpawnDetached(dirs)
	}
	return daemonRunForeground(ctx, dirs, cfg)
}

// daemonSpawnDetached re-execs ourselves with --foreground, redirects
// the child's stdio to a log file, decouples it from our process group
// (Setsid), and returns once the control socket is responsive — or
// after a sane timeout, with the log path so the user can inspect a
// failure.
func daemonSpawnDetached(dirs state.Dirs) int {
	if err := os.MkdirAll(filepath.Join(dirs.State, "logs"), 0o700); err != nil {
		errf("daemon start: %v", err)
		return 3
	}
	logPath := filepath.Join(dirs.State, "logs", "intentd.log")
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		errf("daemon start: open log %s: %v", logPath, err)
		return 3
	}
	defer logF.Close()

	self, err := os.Executable()
	if err != nil {
		errf("daemon start: locate self: %v", err)
		return 3
	}
	cmd := exec.Command(self, "daemon", "start", "--foreground")
	cmd.Env = append(os.Environ(), "INTENTD_FOREGROUND=1")
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		errf("daemon start: spawn: %v", err)
		return 3
	}
	// Don't wait for it — we want it to outlive us.
	go func() { _ = cmd.Process.Release() }()

	// Poll the control socket for readiness. The child has 30s to come
	// up before we report failure; on a cold cache that's mostly
	// llamafile loading the model.
	deadline := time.Now().Add(30 * time.Second)
	c := daemon.NewClient(dirs.SocketPath())
	for time.Now().Before(deadline) {
		if resp, err := c.Call(daemon.Request{Op: daemon.OpPing}); err == nil && resp.OK {
			fmt.Fprintln(os.Stderr, "intentd: started in the background.")
			fmt.Fprintf(os.Stderr, "  socket: %s\n", dirs.SocketPath())
			fmt.Fprintf(os.Stderr, "  log:    %s\n", logPath)
			return 0
		}
		time.Sleep(250 * time.Millisecond)
	}
	errf("daemon start: timed out waiting for control socket; tail -f %s", logPath)
	return 3
}

func daemonRunForeground(ctx context.Context, dirs state.Dirs, cfg *config.Config) int {
	mgr := intentruntime.New(dirs.Cache)
	if !mgr.HaveLlamafile() {
		errf("daemon: llamafile runtime missing — run `i model pull` first")
		errf("  expected: %s", mgr.LlamafilePath())
		return 3
	}
	// Resolve the model through the full catalog (built-in + custom)
	// so the daemon loads exactly what `i model use` selected, even
	// for user-added HF repos that aren't in the built-in list.
	cat := loadCatalog(dirs.State)
	id := cfg.Model
	if id == "" {
		id = models.DefaultID
	}
	host, port, err := resolveLocalDaemonEndpoint(cfg)
	if err != nil {
		errf("daemon: %v", err)
		return 1
	}
	m := cat.Get(id)
	if m == nil {
		errf("daemon: current model %q not in catalog; run `i model list` and `i model use <id>`", id)
		return 1
	}
	modelPath := mgr.ModelPath(models.ModelFilename(m))
	if _, err := os.Stat(modelPath); err != nil {
		errf("daemon: model %q not installed — run `i model pull %s`", id, id)
		errf("  expected: %s", modelPath)
		return 3
	}

	logDir := filepath.Join(dirs.State, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		errf("daemon: mkdir log dir: %v", err)
		return 3
	}
	logPath := filepath.Join(logDir, "llamafile.log")
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		errf("daemon: open log: %v", err)
		return 3
	}
	defer logF.Close()

	portNum := 18080
	fmt.Sscanf(port, "%d", &portNum)

	launcher := daemon.NewLauncher(mgr.LlamafilePath(), modelPath, host, portNum)
	launcher.StdoutLog = logF
	launcher.StderrLog = io.MultiWriter(logF, os.Stderr)

	startCtx, cancelStart := context.WithTimeout(ctx, 90*time.Second)
	fmt.Fprintln(os.Stderr, "intentd: starting llamafile...")
	if err := launcher.Start(startCtx); err != nil {
		cancelStart()
		errf("daemon: start llamafile: %v", err)
		return 3
	}
	cancelStart()
	fmt.Fprintf(os.Stderr, "intentd: llamafile ready on %s (pid %d)\n",
		launcher.Endpoint(), launcher.PID())

	srv := daemon.New(dirs.SocketPath(), launcher)
	if err := srv.Listen(); err != nil {
		launcher.Stop(5 * time.Second)
		errf("daemon: listen: %v", err)
		return 3
	}
	fmt.Fprintf(os.Stderr, "intentd: control socket at %s\n", dirs.SocketPath())

	sigCtx, cancelSig := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancelSig()
	serveDone := make(chan struct{})
	go func() {
		_ = srv.Serve(sigCtx)
		close(serveDone)
	}()

	// Block until any of: an OS signal, an `i daemon stop` over the
	// socket, the parent context is canceled, or the supervised
	// llamafile gives up entirely (Wait drains).
	llamaDone := make(chan struct{})
	go func() { launcher.Wait(); close(llamaDone) }()
	select {
	case <-sigCtx.Done():
	case <-srv.Stopped():
	case <-serveDone:
	case <-llamaDone:
	}
	fmt.Fprintln(os.Stderr, "intentd: shutting down...")
	srv.SignalStop()
	launcher.Stop(10 * time.Second)
	fmt.Fprintln(os.Stderr, "intentd: stopped.")
	return 0
}

func daemonStop(dirs state.Dirs) int {
	c := daemon.NewClient(dirs.SocketPath())
	resp, err := c.Call(daemon.Request{Op: daemon.OpStop})
	if err != nil {
		errf("daemon stop: %v (is the daemon running?)", err)
		return 1
	}
	if !resp.OK {
		errf("daemon stop: %s", resp.Error)
		return 1
	}
	fmt.Println("daemon: stop requested")
	return 0
}

func daemonStatus(dirs state.Dirs) int {
	c := daemon.NewClient(dirs.SocketPath())
	resp, err := c.Call(daemon.Request{Op: daemon.OpStatus})
	if err != nil {
		fmt.Println("daemon: not running")
		fmt.Println("  socket:", dirs.SocketPath())
		fmt.Println("  installed as service:", daemon.IsInstalled(daemonLabel))
		return 1
	}
	if !resp.OK {
		errf("daemon status: %s", resp.Error)
		return 1
	}
	fmt.Println("daemon: running")
	for k, v := range resp.Data {
		fmt.Printf("  %s: %v\n", k, v)
	}
	return 0
}

func daemonLogs(dirs state.Dirs) int {
	logPath := filepath.Join(dirs.State, "logs", "llamafile.log")
	if runtime.GOOS == "linux" && daemon.IsInstalled(daemonLabel) {
		fmt.Fprintln(os.Stderr, "Tip: run `journalctl --user -u "+daemonLabel+".service -f` for the systemd-managed log.")
		fmt.Fprintln(os.Stderr, "Showing the llamafile subprocess log:", logPath)
	}
	f, err := os.Open(logPath)
	if err != nil {
		errf("logs: %v", err)
		return 1
	}
	defer f.Close()
	if _, err := io.Copy(os.Stdout, f); err != nil {
		errf("logs: %v", err)
		return 1
	}
	return 0
}

func daemonInstall(dirs state.Dirs) int {
	bin, err := os.Executable()
	if err != nil {
		errf("daemon install: locate self: %v", err)
		return 3
	}
	bin, _ = filepath.EvalSymlinks(bin)
	res, err := daemon.Install(daemon.InstallParams{
		Binary: bin,
		Label:  daemonLabel,
		LogDir: filepath.Join(dirs.State, "logs"),
		Socket: dirs.SocketPath(),
		Cache:  dirs.Cache,
		State:  dirs.State,
	})
	if err != nil {
		errf("daemon install: %v", err)
		return 3
	}
	fmt.Println("daemon installed as a system service.")
	fmt.Println("  unit:    ", res.UnitPath)
	fmt.Println("  start:   ", strJoin(res.StartCmd))
	fmt.Println("  stop:    ", strJoin(res.StopCmd))
	if res.LogPath != "" {
		fmt.Println("  log:     ", res.LogPath)
	}
	if res.Notes != "" {
		fmt.Println()
		fmt.Println(res.Notes)
	}
	return 0
}

func daemonUninstall(dirs state.Dirs) int {
	// Try a polite stop first.
	c := daemon.NewClient(dirs.SocketPath())
	_, _ = c.Call(daemon.Request{Op: daemon.OpStop})
	if err := daemon.Uninstall(daemonLabel); err != nil {
		errf("daemon uninstall: %v", err)
		return 3
	}
	fmt.Println("daemon: service uninstalled.")
	return 0
}

func strJoin(parts []string) string {
	if len(parts) == 0 {
		return "(none)"
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// modelFileFor maps a config model tag (e.g. "qwen2.5-coder-7b-instruct-q4_k_m")
// to the GGUF filename we expect on disk. v1 is hard-coded to one default;
// future versions consult a model registry.
func modelFileFor(tag string) string {
	if tag == intentruntime.DefaultModel.Name {
		return intentruntime.DefaultModel.File
	}
	// Best-effort: assume tag + ".gguf".
	if filepath.Ext(tag) == ".gguf" {
		return tag
	}
	return tag + ".gguf"
}
