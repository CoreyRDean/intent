package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/installmeta"
	"github.com/CoreyRDean/intent/internal/state"
	"github.com/CoreyRDean/intent/internal/update"
	"github.com/CoreyRDean/intent/internal/version"
)

func cmdUpdate(ctx context.Context, args []string) int {
	sub := "check"
	if len(args) > 0 {
		sub = args[0]
	}
	dirs, _ := state.Resolve()
	cfg, _ := config.Load(dirs.ConfigPath())
	switch sub {
	case "check":
		return updateCheck(ctx, update.Channel(cfg.UpdateChannel))
	case "now":
		return updateNow(ctx, dirs, update.Channel(cfg.UpdateChannel))
	case "auto":
		cfg.AutoUpdate = true
		if err := config.Write(dirs.ConfigPath(), cfg); err != nil {
			errf("update: %v", err)
			return 3
		}
		fmt.Println("auto-update: on")
		return 0
	case "off":
		cfg.AutoUpdate = false
		cfg.UpdateChannel = string(update.ChannelOff)
		if err := config.Write(dirs.ConfigPath(), cfg); err != nil {
			errf("update: %v", err)
			return 3
		}
		fmt.Println("auto-update: off; channel: off")
		return 0
	default:
		errf("unknown subcommand: %q", sub)
		return 1
	}
}

func updateCheck(ctx context.Context, ch update.Channel) int {
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cur := strings.TrimPrefix(version.Short(), "v")
	r, err := update.Check(tctx, ch, cur)
	if err != nil {
		errf("update: %v", err)
		return 3
	}
	if r.Latest == nil {
		fmt.Printf("no releases found on channel %q\n", ch)
		return 0
	}
	if r.Available {
		fmt.Printf("update available: %s → %s\n  %s\n",
			version.Short(), r.Latest.TagName, r.Latest.HTMLURL)
		return 0
	}
	fmt.Printf("up to date (%s)\n", version.Short())
	return 0
}

// updateNow dispatches based on the recorded install method, so users
// who installed via Homebrew get `brew upgrade intent` and users who
// curl|bashed the install script get the install script re-run. We
// never invent a path the user didn't choose.
func updateNow(ctx context.Context, dirs state.Dirs, ch update.Channel) int {
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cur := strings.TrimPrefix(version.Short(), "v")
	r, err := update.Check(tctx, ch, cur)
	if err != nil {
		errf("update: cannot reach release server: %v", err)
		return 3
	}
	if r.Latest == nil {
		fmt.Printf("no releases found on channel %q\n", ch)
		return 0
	}
	if !r.Available {
		fmt.Printf("up to date (%s)\n", version.Short())
		return 0
	}

	marker, _ := installmeta.Read(dirs.State)
	fmt.Printf("update available: %s → %s\n", version.Short(), r.Latest.TagName)
	fmt.Printf("install method:   %s\n", marker.Method.HumanName())

	switch marker.Method {
	case installmeta.MethodBrew:
		return runUpdater("brew", []string{"update"}, []string{"upgrade", "intent"})

	case installmeta.MethodScript, installmeta.MethodManual, installmeta.MethodUnknown:
		// The install script is idempotent and verifies a SHA256
		// before overwriting, so re-running it is the cleanest update.
		// We refuse to silently overwrite a binary the user dropped
		// in by hand, though — that's the one case where we just print.
		if marker.Method == installmeta.MethodManual {
			fmt.Println()
			fmt.Println("This binary appears to have been installed manually (built from source")
			fmt.Println("or dropped in by hand). Refusing to self-update; choose one:")
			fmt.Println("  brew install CoreyRDean/tap/intent     # switch to the brew channel")
			fmt.Println("  curl -fsSL https://raw.githubusercontent.com/CoreyRDean/intent/main/install.sh | bash")
			return 1
		}
		return runScriptInstaller(ch)

	case installmeta.MethodGo:
		return runUpdater("go", []string{"install", "github.com/CoreyRDean/intent/cmd/intent@latest"})

	case installmeta.MethodPackage:
		fmt.Println()
		fmt.Println("This binary was installed via your distro's package manager.")
		fmt.Println("Update via the same channel (apt/dnf/pacman/etc.).")
		return 1

	default:
		fmt.Println()
		fmt.Printf("Unrecognised install method (%q). Update via:\n", marker.Method)
		fmt.Println("  brew upgrade intent       # if installed via Homebrew")
		fmt.Println("  rerun install.sh          # if installed via the install script")
		return 1
	}
}

// runUpdater executes a sequence of commands using the same binary
// (e.g. `brew update`, then `brew upgrade intent`). Stops on first failure.
func runUpdater(bin string, argSets ...[]string) int {
	full, err := exec.LookPath(bin)
	if err != nil {
		errf("update: %s not found on $PATH", bin)
		return 3
	}
	for _, args := range argSets {
		fmt.Printf("\n$ %s %s\n", bin, strings.Join(args, " "))
		cmd := exec.Command(full, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			errf("update: %s %s failed: %v", bin, strings.Join(args, " "), err)
			return 1
		}
	}
	fmt.Println("\nupdate complete.")
	return 0
}

// runScriptInstaller pipes the install.sh script through bash. We use
// the channel from config so users who opted into nightly stay on it.
func runScriptInstaller(ch update.Channel) int {
	if _, err := exec.LookPath("bash"); err != nil {
		errf("update: bash required to re-run the install script")
		return 3
	}
	if _, err := exec.LookPath("curl"); err != nil {
		errf("update: curl required to re-run the install script")
		return 3
	}
	args := []string{"-c", `set -euo pipefail
URL="https://raw.githubusercontent.com/CoreyRDean/intent/main/install.sh"
if [ -n "${1:-}" ]; then
  curl -fsSL "$URL" | bash -s -- --channel "$1"
else
  curl -fsSL "$URL" | bash
fi`, "intent-self-update"}
	if ch != "" && ch != update.ChannelOff {
		args = append(args, string(ch))
	}
	fmt.Printf("\nre-running install.sh (channel: %s)...\n\n", ch)
	cmd := exec.Command("bash", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		errf("update: install.sh failed: %v", err)
		return 1
	}
	fmt.Println("\nupdate complete.")
	return 0
}
