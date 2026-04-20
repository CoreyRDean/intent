package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/CoreyRDean/intent/internal/daemon"
	"github.com/CoreyRDean/intent/internal/state"
)

func cmdDaemon(ctx context.Context, args []string) int {
	if len(args) == 0 {
		errf("usage: i daemon (start | stop | status | logs | install | uninstall)")
		return 1
	}
	dirs, err := state.Resolve()
	if err != nil {
		errf("daemon: %v", err)
		return 3
	}
	switch args[0] {
	case "start":
		// Foreground for now; LaunchAgent/systemd integration lands in Phase 4.
		s := &daemon.Server{Socket: dirs.SocketPath()}
		if err := s.Listen(); err != nil {
			errf("daemon: %v", err)
			return 3
		}
		fmt.Println("intentd listening on", dirs.SocketPath())
		ctx2, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer cancel()
		_ = s.Serve(ctx2)
		return 0
	case "status":
		if _, err := os.Stat(dirs.SocketPath()); err == nil {
			fmt.Println("daemon: socket present at", dirs.SocketPath())
			return 0
		}
		fmt.Println("daemon: not running")
		return 1
	case "stop", "logs", "install", "uninstall":
		errf("daemon %s: not yet implemented in v1", args[0])
		return 1
	default:
		errf("unknown subcommand: %q", args[0])
		return 1
	}
}
