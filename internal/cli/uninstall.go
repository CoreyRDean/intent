package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/CoreyRDean/intent/internal/state"
)

func cmdUninstall(_ context.Context, args []string) int {
	yes := false
	keepState := false
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--keep-state":
			keepState = true
		}
	}

	dirs, err := state.Resolve()
	if err != nil {
		errf("uninstall: %v", err)
		return 3
	}

	binary, _ := os.Executable()
	fmt.Println("intent will be uninstalled.")
	fmt.Println("  binary:    ", binary)
	if !keepState {
		fmt.Println("  state dir: ", dirs.State, "(will be removed unless --keep-state)")
	} else {
		fmt.Println("  state dir: ", dirs.State, "(kept)")
	}
	fmt.Println("  cache dir: ", dirs.Cache, "(will be removed)")

	if !yes {
		fmt.Print("\nproceed? [y/N] ")
		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line != "y" && line != "yes" {
			fmt.Println("aborted.")
			return 2
		}
	}

	// Remove cache (always).
	if err := os.RemoveAll(dirs.Cache); err != nil {
		errf("uninstall: cache: %v", err)
	}
	if !keepState {
		if err := os.RemoveAll(dirs.State); err != nil {
			errf("uninstall: state: %v", err)
		}
	}
	if binary != "" {
		if err := os.Remove(binary); err != nil {
			errf("uninstall: binary: %v (you may need to remove it manually)", err)
		}
	}
	fmt.Println("done.")
	return 0
}
