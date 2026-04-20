// Package cli implements the `intent` / `i` command-line interface.
//
// The dispatcher is hand-rolled to keep the dep tree at zero — cold-start
// matters, and the CLI surface is small enough that a 200-line dispatcher
// is preferable to pulling cobra. Each subcommand lives in its own file.
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/CoreyRDean/intent/internal/version"
)

// knownSubcommands is the frozen set of top-level subcommands.
// Anything else is treated as natural language.
var knownSubcommands = map[string]commandHandler{
	"init":    cmdInit,
	"doctor":  cmdDoctor,
	"config":  cmdConfig,
	"model":   cmdModel,
	"daemon":  cmdDaemon,
	"history": cmdHistory,
	"pin":     cmdPin,
	"run":     cmdRun,
	"explain": cmdExplain,
	"fix":     cmdFix,
	"report":  cmdReport,
	"update":  cmdUpdate,
	"version": cmdVersion,
	"help":    cmdHelp,
}

type commandHandler func(ctx context.Context, args []string) int

// Run is the program entrypoint. Returns the exit code.
func Run(ctx context.Context, args []string) int {
	// Dispatch top-level flags before stripping so that --version and
	// --help are not consumed by stripGlobalFlags before they can be matched.
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-V":
			return cmdVersion(ctx, nil)
		case "--help", "-h":
			return cmdHelp(ctx, nil)
		case "--uninstall":
			return cmdUninstall(ctx, args[1:])
		case "--update":
			return cmdUpdate(ctx, args[1:])
		}
	}

	args = stripGlobalFlags(args)
	if len(args) == 0 {
		// `i` with no args: show help.
		return cmdHelp(ctx, nil)
	}

	if h, ok := knownSubcommands[args[0]]; ok {
		return h(ctx, args[1:])
	}

	// Natural-language mode: everything is the prompt.
	return cmdIntent(ctx, args)
}

var globalsConsumed = struct {
	help    bool
	version bool
}{}

// stripGlobalFlags handles --help / --version anywhere in the arg list.
// It does NOT strip natural-language-mode flags; those are parsed by cmdIntent.
func stripGlobalFlags(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--help":
			globalsConsumed.help = true
		case "--version":
			globalsConsumed.version = true
		default:
			out = append(out, a)
		}
	}
	return out
}

// usage is the help banner.
const usage = `intent — you say what you want, the terminal does it.

Usage:
  i <natural language describing what you want>
  i <subcommand> [args]

Common:
  i ping google's dns                # natural language (default)
  i list large files in this repo
  cat foo.csv | i extract emails

Subcommands:
  init        First-run setup (model, daemon, completions).
  doctor      Diagnose installation, model, daemon, sandbox.
  config      Get/set/edit configuration.
  model       Manage local models.
  daemon      Start/stop/status the background daemon.
  history     Inspect or clear the audit log.
  pin         Promote the last accepted command to a named skill.
  run         Run a pinned skill by name.
  explain     Explain what an arbitrary shell command does.
  fix         Re-attempt the last failed run with error context.
  report      File or comment on GitHub issues from natural language.
  update      Check for / install / configure updates.
  version     Print version information.

Flags (in natural-language mode):
  -y, --yes        Auto-confirm safe and network risk levels.
      --dry        Don't execute; print what would happen.
      --sandbox    Execute under a platform sandbox.
      --ro         Cwd bind-mounted read-only (implies --sandbox).
      --json       Emit structured response on stdout.
      --raw        Emit only the generated command on stdout.
  -q, --quiet      Suppress spinner and decoration.
      --bool       Force a yes/no answer; map to exit code 0/1.
      --explain    Show plain-English breakdown without running.
      --no-cache   Don't read or write the skill cache.
      --backend X  Override backend for this call.
      --model X    Override model for this call.
      --timeout D  Hard cap (default 60s).
   -n N            Generate N alternatives.

Top-level:
  --version, -V    Print version.
  --help, -h       This help.
  --uninstall      Remove binary, daemon, and (with consent) state.
  --update         Equivalent to "update".

Read INTENT.md and docs/SPEC.md before contributing.
`

func cmdHelp(_ context.Context, _ []string) int {
	fmt.Print(usage)
	return 0
}

func cmdVersion(_ context.Context, _ []string) int {
	fmt.Println(version.Long())
	return 0
}

// errf prints to stderr.
func errf(format string, a ...any) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, format, a...)
}
