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

	"github.com/CoreyRDean/intent/internal/verbose"
	"github.com/CoreyRDean/intent/internal/version"
)

// knownSubcommands is the frozen set of top-level subcommands.
// Anything else is treated as natural language.
var knownSubcommands = map[string]commandHandler{
	"init":       cmdInit,
	"shell-init": cmdShellInit,
	"doctor":     cmdDoctor,
	"config":     cmdConfig,
	"model":      cmdModel,
	"daemon":     cmdDaemon,
	"history":    cmdHistory,
	"pin":        cmdPin,
	"run":        cmdRun,
	"explain":    cmdExplain,
	"fix":        cmdFix,
	"report":     cmdReport,
	"update":     cmdUpdate,
	"version":    cmdVersion,
	"help":       cmdHelp,
}

type commandHandler func(ctx context.Context, args []string) int

// rewriteLiteralArgs collapses everything after the first --literal flag
// into one prompt token and reports that dispatcher-level natural-language
// mode was explicitly requested.
func rewriteLiteralArgs(args []string) ([]string, bool) {
	for i, a := range args {
		if a != "--literal" {
			continue
		}
		out := append([]string{}, args[:i]...)
		if tail := strings.TrimSpace(strings.Join(args[i+1:], " ")); tail != "" {
			out = append(out, tail)
		}
		return out, true
	}
	return args, false
}

// Run is the program entrypoint. Returns the exit code.
func Run(ctx context.Context, args []string) int {
	args, forceLiteral := rewriteLiteralArgs(args)

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

	// Install a verbose logger on ctx if -v/--verbose was set. Every
	// downstream package (engine, backend wrapper, report/gh, etc.)
	// pulls it from ctx, so commands don't each need to parse the
	// flag themselves. INTENT_VERBOSE=1 is also honored so users can
	// enable tracing without editing their shell alias.
	if globalsConsumed.verbose || os.Getenv("INTENT_VERBOSE") == "1" {
		l := verbose.Default(true)
		ctx = verbose.WithLogger(ctx, l)
		l.Section("intent invocation")
		l.KV("version", version.Short())
		l.KV("pid", os.Getpid())
		l.KV("argv", strings.Join(os.Args, " "))
		l.KV("cwd", mustCwd())
	}

	if forceLiteral {
		return cmdIntent(ctx, args)
	}

	if h, ok := knownSubcommands[args[0]]; ok {
		return h(ctx, args[1:])
	}

	// Natural-language mode: everything is the prompt.
	return cmdIntent(ctx, args)
}

func mustCwd() string {
	s, err := os.Getwd()
	if err != nil {
		return "?"
	}
	return s
}

var globalsConsumed = struct {
	help    bool
	version bool
	verbose bool
}{}

// stripGlobalFlags handles --help / --version / -v / --verbose anywhere
// in the arg list. It does NOT strip natural-language-mode flags;
// those are parsed by cmdIntent. Consuming -v here means every
// subcommand (i report, i explain, i model, ...) automatically
// supports verbose mode without each command re-parsing the flag.
func stripGlobalFlags(args []string) []string {
	// Reset per-invocation so the dispatcher is re-entrant in tests.
	globalsConsumed.help = false
	globalsConsumed.version = false
	globalsConsumed.verbose = false
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--help":
			globalsConsumed.help = true
		case "--version":
			globalsConsumed.version = true
		case "-v", "--verbose":
			globalsConsumed.verbose = true
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
  i "ping google's dns"              # natural language (default)
  i list large files in this repo
  cat foo.csv | i extract emails

Tip:
  If your prompt includes apostrophes (what's, don't), wrap it in
  double quotes for reliable shell parsing across environments.

Subcommands:
  init        First-run setup (model, daemon, completions).
  shell-init  Print shell snippet to source for natural-language quoting.
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
      --literal    Treat everything after this flag as natural language.
      --sandbox    Execute under a platform sandbox.
      --ro         Cwd bind-mounted read-only (implies --sandbox).
      --json       Emit structured response on stdout.
      --raw        Emit only the generated command on stdout.
  -q, --quiet      Suppress spinner and decoration.
      --bool       Force a yes/no answer; map to exit code 0/1.
      --explain    Show plain-English breakdown without running.
      --no-cache   Don't read or write the skill cache.
      --from-intent  Treat stdin as context from another intent invocation.
      --context K=V  Add ad-hoc context for this invocation (repeatable).
      --backend X  Override backend for this call.
      --model X    Override model for this call.
      --timeout D  Hard cap (default 60s).
   -n N            Generate N alternatives.

Top-level:
  --version, -V    Print version.
  --help, -h       This help.
  -v, --verbose    Log model I/O, tool calls, and gh round-trips to stderr.
                   (also enabled by INTENT_VERBOSE=1)
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
