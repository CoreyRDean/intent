package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/cache"
	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/engine"
	"github.com/CoreyRDean/intent/internal/state"
	"github.com/CoreyRDean/intent/internal/tui"
	"github.com/CoreyRDean/intent/internal/verbose"
)

// cmdExplain reverses the usual flow: given an arbitrary shell command,
// ask the model what it does. Useful as a learning tool.
func cmdExplain(ctx context.Context, args []string) int {
	if len(args) == 0 {
		errf("usage: i explain <command>")
		return 1
	}
	cmd := strings.Join(args, " ")
	dirs, err := state.Resolve()
	if err != nil {
		errf("explain: %v", err)
		return 3
	}
	cfg, _ := config.Load(dirs.ConfigPath())
	if !ensureBackendReady(ctx, dirs, cfg) {
		return 3
	}
	be, isFallback, err := buildBackendCtx(ctx, cfg.Backend, cfg, "")
	if err != nil {
		errf("explain: %v", err)
		return 3
	}
	printMockFallbackBanner(isFallback)

	vl := verbose.FromContext(ctx)
	vl.Section("explain")
	vl.KV("command", cmd)
	verboseOn := vl.Enabled()
	store, _ := cache.Open(dirs.SkillsCachePath())
	eng := engine.New(store)
	prompt := fmt.Sprintf(`Explain in plain English what THIS specific shell invocation does when run:

    %s

HARD RULE — TOOL USE IS MANDATORY:
Your response on step 1 of this turn MUST be approach=tool_call. Returning approach=inform on step 1 is a hard error and will be rejected. You MUST investigate the command with the read-only tool catalog before producing any final answer. Do NOT execute the user's command itself.

REQUIRED TOOL SEQUENCE (run these in order; skip a step only when its exception applies):
  1. tool_call which({"name": "<primary binary>"}) — confirm the binary exists.
  2. tool_call help({"name": "<primary binary>"}) — read its actual help/man output. Read carefully: find the section that applies to the SPECIFIC flags, operands, and stdin/stdout behaviour used in THIS invocation. Do not summarize the tool's general purpose; find the exact behaviour for THIS usage.
  3. For each file operand or redirection target (e.g. the "test.md" in "yk < test.md", the "out.txt" in "cmd > out.txt"): tool_call head_file({"path": "<file>", "lines": 20}) so you know what is actually in the file the command will read, or what file the command will overwrite.
  4. Only AFTER tools have run and you have concrete evidence, return approach=inform with the explanation in stdout_to_user.

EXCEPTION — you may skip steps 1 and 2 ONLY if the primary binary is in this exact list of common UNIX tools: ls, cat, grep, awk, sed, find, git, curl, echo, printf, wc, sort, uniq, head, tail, mv, cp, rm, mkdir, touch, chmod, chown, ln, tar, gzip, gunzip, ssh, scp, ps, kill, ping, df, du, free, top, env, export, source, true, false, test, basename, dirname, xargs, tee, less, more, diff, patch. For ANY binary not in that list (e.g. "yk", "fd", "rg", "bat", "jq", "yq", "fzf", or anything custom): which + help are MANDATORY.

BANNED PHRASES IN YOUR FINAL ANSWER (these are tells that you guessed instead of investigating; if you find yourself wanting to write one, go back and run more tools):
- "according to its implementation"
- "depending on the tool's functionality"
- "may include ... or other data"
- "indicates whether the operation was successful"
- "processes it according to"
- "X is a Y tool that does Z" (state what THIS invocation does, not what the binary does)

FINAL ANSWER SHAPE (after tools have run):
- 1–4 sentences. Concrete. Specific to THIS invocation.
- Open with a verb describing what THIS invocation does (e.g. "Pipes the contents of test.md into yk's stdin, which pushes that text onto yk's clipboard stack."). Not "X is a tool that ...".
- Account for EVERY operand, flag, redirection, and pipe in the command. Nothing unexplained.
- Where the command reads a file, briefly note what's in the file based on head_file.
- Cite specific behaviour from the help output (e.g. "per yk --help, when invoked with no flags it reads stdin and pushes it to the stack").
- Name the observable result: what gets printed, what changes on disk, what exit code is meaningful.

Command to explain (verbatim):
%s`, cmd, cmd)
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Progress feedback. Local-model inference can take several seconds;
	// without a spinner the CLI looks frozen. Renders to stderr only
	// and is a no-op when stderr isn't a TTY, so piped/scripted use
	// stays clean.
	style := tui.DefaultStyle()
	var sp *tui.Spinner
	// In verbose mode the log stream itself is the progress indicator,
	// so don't animate a spinner on the same stderr.
	if !verboseOn {
		sp = tui.NewSpinner(style)
		if sp != nil && tui.IsTTY(os.Stderr) {
			sp.Start("Invoking...")
			defer sp.Stop()
		}
	}

	res, err := eng.Run(tctx, prompt, engine.Options{
		Backend:      be,
		MaxToolSteps: 0,
		OnPhase: func(p string) {
			if sp != nil {
				sp.SetLabel(p)
			}
		},
	})
	// Stop the spinner before printing output so its trailing \r\x1b[K
	// can't overwrite the first line of the explanation.
	if sp != nil {
		sp.Stop()
	}
	if err != nil {
		errf("explain: %v", err)
		return 3
	}
	if res.Response.StdoutToUser != "" {
		fmt.Print(res.Response.StdoutToUser)
		if !strings.HasSuffix(res.Response.StdoutToUser, "\n") {
			fmt.Println()
		}
		return 0
	}
	if res.Response.Description != "" {
		fmt.Println(res.Response.Description)
		return 0
	}
	errf("explain: model produced no explanation")
	return 1
}
