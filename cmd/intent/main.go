// Command intent is the natural-language command interpreter binary.
//
// Symlinking this binary to "i" gives the short alias.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/CoreyRDean/intent/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	os.Exit(cli.Run(ctx, os.Args[1:]))
}
