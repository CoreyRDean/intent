package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/installmeta"
	intentruntime "github.com/CoreyRDean/intent/internal/runtime"
	"github.com/CoreyRDean/intent/internal/state"
	"github.com/CoreyRDean/intent/internal/version"
)

func cmdInit(ctx context.Context, args []string) int {
	if len(args) > 0 && args[0] == "record-install" {
		return cmdInitRecordInstall(args[1:])
	}
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

	// Shell integration prompt — default Yes. Without it, zsh users
	// hit "no matches found" the first time they type a prompt with
	// a literal `?` in it, which is a brutal first impression.
	fmt.Print("Install shell hook so prompts with ? * [ ] don't get glob-eaten? [Y/n] ")
	hookAnswer := "y"
	if !autoYes {
		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			line = "y"
		}
		hookAnswer = line
	}
	installHook := hookAnswer == "y" || hookAnswer == "yes"

	if err := config.Write(dirs.ConfigPath(), cfg); err != nil {
		errf("init: write config: %v", err)
		return 3
	}

	fmt.Println()
	fmt.Println("Wrote", dirs.ConfigPath())
	if cfg.DaemonEnabled {
		fmt.Println("Daemon: enabled. Run `i daemon install` to register it as a launchd/systemd service.")
	} else {
		fmt.Println("Daemon: disabled. Each invocation will cold-load the model.")
	}

	if installHook {
		writeShellHook()
	} else {
		fmt.Println("Shell hook: skipped. If you're on zsh, you'll need to quote prompts containing")
		fmt.Println("            ? * [ ] characters, or run `i shell-init zsh >> ~/.zshrc` later.")
	}

	// Model pull + daemon start. This is the difference between
	// "config written, now go figure out three more commands" and
	// "open a new shell and you're working." Default Yes.
	mgr := intentruntime.New(dirs.Cache)
	haveLF := mgr.HaveLlamafile()
	haveModel := mgr.HaveModel(intentruntime.DefaultModel.File)
	if !haveLF || !haveModel {
		fmt.Println()
		fmt.Printf("Download the default local model now? (~%d MB) [Y/n] ",
			intentruntime.DefaultModel.SizeMB)
		pullAnswer := "y"
		if !autoYes {
			r := bufio.NewReader(os.Stdin)
			line, _ := r.ReadString('\n')
			line = strings.TrimSpace(strings.ToLower(line))
			if line == "" {
				line = "y"
			}
			pullAnswer = line
		}
		if pullAnswer == "y" || pullAnswer == "yes" {
			if !haveLF {
				fmt.Println("downloading runtime...")
				if err := mgr.EnsureLlamafile(ctx, progressCB("llamafile")); err != nil {
					fmt.Println()
					errf("init: download runtime: %v", err)
					fmt.Println("you can retry with `i model pull`.")
					return 0
				}
				fmt.Println()
			}
			if !haveModel {
				fmt.Printf("downloading model (~%d MB)...\n", intentruntime.DefaultModel.SizeMB)
				if err := mgr.EnsureModel(ctx, intentruntime.DefaultModel, progressCB("model")); err != nil {
					fmt.Println()
					errf("init: download model: %v", err)
					fmt.Println("you can retry with `i model pull`.")
					return 0
				}
				fmt.Println()
			}
		} else {
			fmt.Println("Skipped. Run `i model pull` later (or just run any prompt — we'll offer again).")
		}
	} else {
		fmt.Println()
		fmt.Println("Model:       already installed.")
	}

	if cfg.DaemonEnabled {
		fmt.Println("Starting daemon...")
		if err := startDaemonAndWait(dirs); err != nil {
			errf("init: %v", err)
			fmt.Println("you can retry with `i daemon start` (and inspect logs at",
				filepath.Join(dirs.State, "logs", "intentd.log")+").")
		} else {
			fmt.Println("Daemon:      running.")
		}
	}

	fmt.Println()
	fmt.Println("All set. Try:")
	fmt.Println("  i hello              # smoke test")
	fmt.Println("  i list large files in this repo")
	return 0
}

// writeShellHook appends the eval line for the user's shell to their
// rc file, idempotently. Matching by sentinel comment so re-running
// `i init` is safe.
//
// The hook is small (one line) and looks like:
//
//	eval "$(intent shell-init zsh)"  # intent shell hook
func writeShellHook() {
	shell := detectParentShell()
	var rcPath, line string
	switch shell {
	case "zsh":
		rcPath = filepath.Join(os.Getenv("HOME"), ".zshrc")
		line = `eval "$(intent shell-init zsh)"  # intent shell hook`
	case "bash":
		rcPath = filepath.Join(os.Getenv("HOME"), ".bashrc")
		line = `eval "$(intent shell-init bash)"  # intent shell hook`
	case "fish":
		rcPath = filepath.Join(os.Getenv("HOME"), ".config", "fish", "config.fish")
		line = `intent shell-init fish | source  # intent shell hook`
	default:
		fmt.Println("Shell hook: couldn't detect your shell ($SHELL is", os.Getenv("SHELL")+");")
		fmt.Println("            run one of:")
		fmt.Println("              eval \"$(intent shell-init zsh)\"   >> ~/.zshrc")
		fmt.Println("              eval \"$(intent shell-init bash)\"  >> ~/.bashrc")
		return
	}

	existing, _ := os.ReadFile(rcPath)
	if strings.Contains(string(existing), "# intent shell hook") {
		fmt.Println("Shell hook: already present in", rcPath)
		return
	}

	if err := os.MkdirAll(filepath.Dir(rcPath), 0o755); err != nil {
		fmt.Println("Shell hook: could not create", filepath.Dir(rcPath)+":", err)
		return
	}
	f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Println("Shell hook: could not open", rcPath+":", err)
		fmt.Println("            run manually:", line)
		return
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, "\n"+line); err != nil {
		fmt.Println("Shell hook: write failed:", err)
		return
	}
	fmt.Println("Shell hook: appended to", rcPath)
	fmt.Println("            open a new shell (or `source", rcPath+"`) to activate.")
}

// cmdInitRecordInstall is the hidden subcommand install.sh and the
// Homebrew formula's post_install both call to record how they
// installed this binary. Without this marker, `i update now` has to
// fall back to a path-based heuristic.
//
// Usage (not user-facing): i init record-install --method (brew|script|go) [--channel CHANNEL]
func cmdInitRecordInstall(args []string) int {
	method := installmeta.MethodUnknown
	channel := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--method":
			if i+1 >= len(args) {
				errf("record-install: --method requires a value")
				return 2
			}
			method = installmeta.Method(args[i+1])
			i++
		case "--channel":
			if i+1 >= len(args) {
				errf("record-install: --channel requires a value")
				return 2
			}
			channel = args[i+1]
			i++
		}
	}
	switch method {
	case installmeta.MethodBrew, installmeta.MethodScript, installmeta.MethodGo,
		installmeta.MethodManual, installmeta.MethodPackage, installmeta.MethodUnknown:
	default:
		errf("record-install: unknown method %q", method)
		return 2
	}
	dirs, err := state.Resolve()
	if err != nil {
		errf("record-install: %v", err)
		return 3
	}
	bin, _ := os.Executable()
	if err := installmeta.Write(dirs.State, installmeta.Marker{
		Method:     method,
		Version:    version.Short(),
		BinaryPath: bin,
		Channel:    channel,
	}); err != nil {
		errf("record-install: %v", err)
		return 3
	}
	return 0
}
