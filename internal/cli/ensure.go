package cli

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/daemon"
	intentruntime "github.com/CoreyRDean/intent/internal/runtime"
	"github.com/CoreyRDean/intent/internal/state"
	"github.com/CoreyRDean/intent/internal/tui"
)

// ensureBackendReady is the self-healing precondition for any subcommand
// that wants to talk to the local model. It checks (in order):
//
//  1. The daemon is reachable. If yes, we're done.
//  2. The runtime + model are present on disk.
//     - If not and stdin is a TTY: ask permission, then download.
//     - If not and we're non-interactive: fail with a clear, copyable
//     command that fixes it.
//  3. With files in place but no daemon, start one in the background.
//  4. Wait briefly for the daemon's control socket to come up.
//
// Returns true if the call site should proceed, false if it should
// bail out (we already printed the failure reason).
//
// Backend-name guard: this only fires for the local llamafile backend.
// Users on `openai`, `ollama`, or `mock` get no prompts and no startup
// attempts — we're not their package manager.
func ensureBackendReady(ctx context.Context, dirs state.Dirs, cfg *config.Config) bool {
	if cfg.Backend != "" && cfg.Backend != "llamafile-local" {
		return true
	}

	// (1) Daemon already up?
	if pingDaemon(dirs) {
		return true
	}

	mgr := intentruntime.New(dirs.Cache)
	haveLF := mgr.HaveLlamafile()
	haveModel := mgr.HaveModel(cfgModelFile(cfg))
	interactive := tui.IsTTY(os.Stdin) && tui.IsTTY(os.Stderr)

	// (2) Missing artifacts.
	if !haveLF || !haveModel {
		if !interactive {
			fmt.Fprintln(os.Stderr, "intent: local model isn't installed yet.")
			fmt.Fprintln(os.Stderr, "  run: i model pull")
			return false
		}
		fmt.Fprintln(os.Stderr, "intent: the local model isn't installed yet.")
		if !haveLF {
			fmt.Fprintln(os.Stderr, "  missing runtime: llamafile-"+intentruntime.LlamafileVersion)
		}
		if !haveModel {
			fmt.Fprintf(os.Stderr, "  missing model:   %s (~%d MB)\n",
				intentruntime.DefaultModel.Name, intentruntime.DefaultModel.SizeMB)
		}
		if !confirmYes("Download now?") {
			fmt.Fprintln(os.Stderr, "intent: skipped. Run `i model pull` later.")
			return false
		}
		if !haveLF {
			fmt.Fprintln(os.Stderr, "downloading runtime...")
			if err := mgr.EnsureLlamafile(ctx, progressCB("llamafile")); err != nil {
				fmt.Fprintln(os.Stderr)
				errf("runtime: %v", err)
				return false
			}
			fmt.Fprintln(os.Stderr)
		}
		if !haveModel {
			fmt.Fprintf(os.Stderr, "downloading model (~%d MB)...\n", intentruntime.DefaultModel.SizeMB)
			if err := mgr.EnsureModel(ctx, intentruntime.DefaultModel, progressCB("model")); err != nil {
				fmt.Fprintln(os.Stderr)
				errf("model: %v", err)
				return false
			}
			fmt.Fprintln(os.Stderr)
		}
	}

	// (3) Start the daemon. We use the same `i daemon start` code path
	// as the user would, so behaviour matches and bugs are shared.
	fmt.Fprintln(os.Stderr, "intent: starting daemon in the background...")
	if rc := daemonSpawnDetached(dirs); rc != 0 {
		fmt.Fprintln(os.Stderr, "intent: daemon failed to start; falling back to mock.")
		return false
	}

	// (4) Confirm it's actually responsive (daemonSpawnDetached already
	// polls, but be defensive — the socket might be ready while
	// llamafile is still warming up its first inference).
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if pingDaemon(dirs) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "intent: daemon started but isn't responding yet; try again in a few seconds.")
	return false
}

// pingDaemon checks both that the control socket exists and that the
// daemon answers a ping. Either an unreachable socket or a sad daemon
// returns false.
func pingDaemon(dirs state.Dirs) bool {
	if _, err := os.Stat(dirs.SocketPath()); err != nil {
		return false
	}
	c, err := net.DialTimeout("unix", dirs.SocketPath(), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	cli := daemon.NewClient(dirs.SocketPath())
	resp, err := cli.Call(daemon.Request{Op: daemon.OpPing})
	return err == nil && resp.OK
}

// cfgModelFile turns the configured model tag into a GGUF filename.
// Mirrors the daemon-side mapping so the two stay in lockstep.
func cfgModelFile(cfg *config.Config) string {
	tag := cfg.Model
	if tag == "" {
		return intentruntime.DefaultModel.File
	}
	if filepath.Ext(tag) == ".gguf" {
		return tag
	}
	return tag + ".gguf"
}

// confirmYes reads a Y/n answer from stdin, defaulting to yes.
// Returns false on EOF or anything starting with 'n'.
func confirmYes(prompt string) bool {
	fmt.Fprintf(os.Stderr, "  %s [Y/n] ", prompt)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "" || line == "y" || line == "yes"
}

// startDaemonAndWait is a small helper used by `i init` after a model
// pull, to bring the daemon up without making the user run a third
// command. It mirrors ensureBackendReady's daemon-startup half but
// with louder logging since this is an explicit setup step.
func startDaemonAndWait(dirs state.Dirs) error {
	if pingDaemon(dirs) {
		return nil
	}
	if rc := daemonSpawnDetached(dirs); rc != 0 {
		return fmt.Errorf("daemon failed to start (see %s)",
			filepath.Join(dirs.State, "logs", "intentd.log"))
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if pingDaemon(dirs) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("daemon started but didn't become responsive in 60s")
}
