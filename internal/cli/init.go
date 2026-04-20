package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/state"
)

func cmdInit(_ context.Context, args []string) int {
	autoYes := false
	for _, a := range args {
		if a == "--yes" || a == "-y" {
			autoYes = true
		}
	}

	dirs, err := state.Resolve()
	if err != nil {
		errf("init: %v", err)
		return 3
	}

	cfg, err := config.Load(dirs.ConfigPath())
	if err != nil {
		errf("init: load config: %v", err)
		return 3
	}

	fmt.Println("intent — first-run setup")
	fmt.Printf("  state dir: %s\n", dirs.State)
	fmt.Printf("  cache dir: %s\n", dirs.Cache)
	fmt.Println()

	// Daemon prompt — default Yes, per D-004.
	fmt.Print("Keep intent warm in the background so it never has to load? [Y/n] ")
	answer := "y"
	if !autoYes {
		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			line = "y"
		}
		answer = line
	}
	cfg.DaemonEnabled = answer == "y" || answer == "yes"

	// Write the config.
	if err := config.Write(dirs.ConfigPath(), cfg); err != nil {
		errf("init: write config: %v", err)
		return 3
	}

	fmt.Println()
	fmt.Println("Wrote", dirs.ConfigPath())
	if cfg.DaemonEnabled {
		fmt.Println("Daemon: enabled. Install with `i daemon install` (next: not yet implemented in v1).")
	} else {
		fmt.Println("Daemon: disabled. Each invocation will cold-load the model.")
	}
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  i model pull         # download the default local model (~4.7 GB)")
	fmt.Println("  i hello              # try it")
	return 0
}
