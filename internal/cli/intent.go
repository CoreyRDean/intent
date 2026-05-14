package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/audit"
	"github.com/CoreyRDean/intent/internal/cache"
	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/engine"
	xexec "github.com/CoreyRDean/intent/internal/exec"
	"github.com/CoreyRDean/intent/internal/model"
	"github.com/CoreyRDean/intent/internal/state"
	"github.com/CoreyRDean/intent/internal/tui"
	"github.com/CoreyRDean/intent/internal/verbose"
	"github.com/CoreyRDean/intent/internal/version"
)

// intentFlags is the parsed v1 flag set for natural-language mode.
type intentFlags struct {
	yes        bool
	dry        bool
	sandbox    bool
	ro         bool
	fromIntent bool
	json       bool
	raw        bool
	quiet      bool
	boolean    bool
	explain    bool
	noCache    bool
	timeout    time.Duration
	backend    string
	modelTag   string
	context    stringListFlag
	n          int
	prompt     string
}

type stringListFlag []string

func (s *stringListFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringListFlag) Set(v string) error {
	*s = append(*s, strings.TrimSpace(v))
	return nil
}

func parseIntentFlags(args []string) (*intentFlags, error) {
	fs := flag.NewFlagSet("intent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := &intentFlags{}
	fs.BoolVar(&out.yes, "yes", false, "")
	fs.BoolVar(&out.yes, "y", false, "")
	fs.BoolVar(&out.dry, "dry", false, "")
	fs.BoolVar(&out.sandbox, "sandbox", false, "")
	fs.BoolVar(&out.ro, "ro", false, "")
	fs.BoolVar(&out.fromIntent, "from-intent", false, "")
	fs.BoolVar(&out.json, "json", false, "")
	fs.BoolVar(&out.raw, "raw", false, "")
	fs.BoolVar(&out.quiet, "quiet", false, "")
	fs.BoolVar(&out.quiet, "q", false, "")
	fs.BoolVar(&out.boolean, "bool", false, "")
	fs.BoolVar(&out.explain, "explain", false, "")
	fs.BoolVar(&out.noCache, "no-cache", false, "")
	fs.DurationVar(&out.timeout, "timeout", 60*time.Second, "")
	fs.StringVar(&out.backend, "backend", "", "")
	fs.StringVar(&out.modelTag, "model", "", "")
	fs.IntVar(&out.n, "n", 1, "")
	fs.Var(&out.context, "context", "")

	// Allow flags interleaved with the natural-language prompt.
	// We pre-extract recognized flags and treat the rest as the prompt.
	known := map[string]bool{
		"--yes": true, "-y": true,
		"--dry":         true,
		"--sandbox":     true,
		"--ro":          true,
		"--from-intent": true,
		"--json":        true,
		"--raw":         true,
		"--quiet":       true, "-q": true,
		"--bool":     true,
		"--explain":  true,
		"--no-cache": true,
	}
	knownVal := map[string]bool{
		"--timeout": true,
		"--backend": true,
		"--model":   true,
		"--context": true,
		"-n":        true,
	}
	flagsToParse := []string{}
	prompt := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case known[a]:
			flagsToParse = append(flagsToParse, a)
		case knownVal[a]:
			if i+1 < len(args) {
				flagsToParse = append(flagsToParse, a, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--") && strings.Contains(a, "="):
			eq := strings.Index(a, "=")
			if known[a[:eq]] || knownVal[a[:eq]] {
				flagsToParse = append(flagsToParse, a)
			} else {
				prompt = append(prompt, a)
			}
		default:
			prompt = append(prompt, a)
		}
	}
	if err := fs.Parse(flagsToParse); err != nil {
		return nil, err
	}
	out.prompt = strings.TrimSpace(strings.Join(prompt, " "))

	// Sensible auto-defaults driven by TTY.
	stdoutTTY := tui.IsTTY(os.Stdout)
	stdinTTY := tui.IsTTY(os.Stdin)
	if !stdoutTTY {
		out.quiet = true
	}
	if out.raw {
		out.quiet = true
		// --raw never executes; it just emits the command. No confirmation
		// needed regardless of risk.
		out.yes = true
	}
	if out.dry {
		// --dry never executes; bypass confirmation gating.
		out.yes = true
	}
	if out.explain {
		out.yes = true
	}
	if out.boolean && !stdoutTTY {
		out.quiet = true
	}
	if !stdinTTY && os.Getenv("INTENT_PIPE_FROM") == "intent" {
		out.fromIntent = true
		out.json = true
	}
	// Piped stdin means there is no usable TTY to read a y/n from, so
	// the interactive confirm path would hard-fail every time. The
	// composability story in the README (`i A | i B`, `cat f | i X`)
	// only works if piped stdin implies consent for auto-run-eligible
	// risk levels. This is the same guarantee the user gets from -y:
	// safe and network auto-run, mutates/destructive/sudo still
	// refuse because implicit approval through a pipe is not enough
	// authority for those. See SPEC.md §auto-run for the policy.
	if !stdinTTY {
		out.yes = true
	}
	if out.ro {
		out.sandbox = true
	}
	return out, nil
}

func cmdIntent(ctx context.Context, args []string) int {
	fl, err := parseIntentFlags(args)
	if err != nil {
		errf("parse flags: %v", err)
		return 1
	}

	if fl.prompt == "" && tui.IsTTY(os.Stdin) {
		// No prompt and stdin is a TTY: show help.
		return cmdHelp(ctx, nil)
	}

	// Pull stdin if it actually has data. We avoid io.ReadAll on a bare,
	// keep-open stdin (which would block forever when intent is launched
	// from a non-TTY supervisor that didn't close stdin). The first-byte
	// wait is bounded by fl.timeout, so a chained `i A | i B` has room
	// for A's (possibly interactive) model call and command to finish.
	// stdinEOF tells us whether upstream has closed the pipe: if yes,
	// upstream is done writing to the shared stderr and it's safe for
	// us to animate our own spinner without clobbering its UI.
	stdinData, stdinEOF := readStdinIfPiped(fl.timeout)

	// Resolve dirs and config.
	dirs, err := state.Resolve()
	if err != nil {
		errf("resolve state dir: %v", err)
		return 3
	}
	cfg, err := config.Load(dirs.ConfigPath())
	if err != nil {
		errf("load config: %v", err)
		return 3
	}

	// Self-heal: if the backend is local-llamafile and the daemon
	// isn't reachable, offer to download the model and start it.
	// This collapses what used to be three commands the user had to
	// guess (`i model pull`, `i daemon install`, retry) into one
	// prompt or, if `--yes` is set, zero. See ensure.go.
	backendForCheck := cfg.Backend
	if fl.backend != "" {
		backendForCheck = fl.backend
	}
	if v := os.Getenv("INTENT_FORCE_BACKEND"); v != "" {
		backendForCheck = v
	}
	cfgForCheck := *cfg
	cfgForCheck.Backend = backendForCheck
	if !ensureBackendReady(ctx, dirs, &cfgForCheck) {
		return 3
	}

	// Build the prompt: include stdin as context unless --raw says otherwise.
	// When --from-intent is set (either explicitly or auto-enabled by
	// INTENT_PIPE_FROM=intent), the stdin bytes are the output of a
	// prior intent invocation rather than opaque data the downstream
	// command should operate on. Frame them accordingly so the model
	// treats them as context, not as content to manipulate; when the
	// upstream used --json we further unpack the envelope into a short
	// semantic summary.
	finalPrompt := fl.prompt + formatStdinForPrompt(stdinData, fl.fromIntent)

	// Build the backend.
	backendName := cfg.Backend
	if fl.backend != "" {
		backendName = fl.backend
	}
	be, isFallback, err := buildBackendCtx(ctx, backendName, cfg, fl.modelTag)
	if err != nil {
		errf("backend: %v", err)
		return 3
	}
	printMockFallbackBanner(isFallback)

	// Top-level verbose breadcrumbs. Safe no-op when -v is off.
	vl := verbose.FromContext(ctx)
	vl.Section("intent mode (natural language)")
	vl.KV("prompt", fl.prompt)
	vl.KV("stdin_bytes", len(stdinData))
	vl.KV("timeout", fl.timeout)
	vl.KV("flags.yes", fl.yes)
	vl.KV("flags.dry", fl.dry)
	vl.KV("flags.raw", fl.raw)
	vl.KV("flags.explain", fl.explain)
	vl.KV("flags.json", fl.json)
	vl.KV("flags.sandbox", fl.sandbox)
	vl.KV("flags.no_cache", fl.noCache)
	vl.KV("flags.from_intent", fl.fromIntent)

	// Cache & engine.
	store, _ := cache.Open(dirs.SkillsCachePath())
	eng := engine.New(store)

	style := tui.DefaultStyle()
	var sp *tui.Spinner
	// Spinner policy. Render only when stderr is a TTY AND we are sure
	// no other process is still painting on the same stderr. The only
	// case where another process might be painting is when stdin was a
	// pipe *and* we did not see EOF on it -- i.e. we timed out waiting
	// for upstream. In every other case (TTY stdin, regular-file stdin,
	// or named pipe drained to EOF) upstream has either never existed
	// or has already exited, so it's safe to animate here.
	// In verbose mode the log stream itself is the progress indicator.
	if !vl.Enabled() && tui.IsTTY(os.Stderr) && stdinEOF {
		sp = tui.NewSpinner(style)
		sp.Start("Invoking...")
		defer sp.Stop()
	}

	tctx, cancel := context.WithTimeout(ctx, fl.timeout)
	defer cancel()

	host := &cliToolHost{sp: sp}
	res, err := eng.Run(tctx, finalPrompt, engine.Options{
		Backend:      be,
		MaxToolSteps: cfg.MaxToolSteps,
		UseCache:     !fl.noCache && cfg.CacheEnabled,
		WriteCache:   !fl.noCache && cfg.CacheEnabled,
		UserContext:  []string(fl.context),
		ToolHost:     host,
		OnPhase: func(p string) {
			if sp != nil {
				sp.SetLabel(p)
			}
		},
	})
	if sp != nil {
		sp.Stop()
		sp = nil
	}
	if err != nil {
		errf("model: %v", err)
		return 3
	}
	resp := res.Response

	// Audit logger.
	logger, lerr := audit.New(dirs.AuditPath())
	auditEntry := audit.Entry{
		Version:       version.Short(),
		Backend:       be.Name(),
		Model:         cfg.Model,
		Prompt:        fl.prompt,
		ModelResponse: resp,
		GuardActions:  res.GuardResult.Actions,
	}

	// --bool short-circuit.
	if fl.boolean {
		if resp.Approach != model.ApproachInform && resp.Command == "" && resp.Script == nil {
			errf("--bool requires the model to produce a checkable command")
			return 1
		}
		runResult, code := executeForBool(tctx, resp, fl, dirs)
		auditEntry.UserDecision = "autorun"
		auditEntry.ExecutedCommand = runResult.Cmd
		auditEntry.ExitCode = &code
		if lerr == nil {
			_ = logger.Append(auditEntry)
		}
		return code
	}

	// --json short-circuit (no execution, just emit).
	if fl.json && resp.Approach != model.ApproachCommand && resp.Approach != model.ApproachScript {
		emitJSON(resp, fl.prompt, nil, 0)
		return 0
	}

	// Handle terminal approaches.
	switch resp.Approach {
	case model.ApproachInform:
		if fl.json {
			emitJSON(resp, fl.prompt, []byte(resp.StdoutToUser), 0)
		} else {
			fmt.Print(resp.StdoutToUser)
		}
		auditEntry.UserDecision = "autorun"
		zero := 0
		auditEntry.ExitCode = &zero
		if lerr == nil {
			_ = logger.Append(auditEntry)
		}
		return 0
	case model.ApproachClarify:
		errf("intent needs clarification: %s", resp.ClarifyingQuestion)
		auditEntry.UserDecision = "cancelled"
		if lerr == nil {
			_ = logger.Append(auditEntry)
		}
		return 2
	case model.ApproachRefuse:
		errf("intent refused: %s", resp.RefusalReason)
		auditEntry.UserDecision = "cancelled"
		if lerr == nil {
			_ = logger.Append(auditEntry)
		}
		return 4
	}

	// Render the proposal on stderr so the user always sees what is
	// about to run -- even when stdout is piped to the next command.
	// renderProposal writes to stderr only, so whether stdout is a
	// TTY is irrelevant here; gate on the surface the user can see.
	if !fl.explain && tui.IsTTY(os.Stderr) {
		renderProposal(resp, res.CacheHit, style)
	}

	if fl.explain {
		if fl.json {
			emitJSON(resp, fl.prompt, nil, 0)
		} else if !fl.quiet {
			renderProposal(resp, res.CacheHit, style)
			fmt.Fprintln(os.Stderr, style.Dim("  --explain: not executing."))
		} else {
			// Quiet/non-TTY: print description and command to stdout.
			fmt.Println(resp.Description)
			if resp.Approach == model.ApproachScript && resp.Script != nil {
				fmt.Println(resp.Script.Body)
			} else if resp.Command != "" {
				fmt.Println(resp.Command)
			}
		}
		if lerr == nil {
			auditEntry.UserDecision = "explain_only"
			_ = logger.Append(auditEntry)
		}
		return 0
	}

	if fl.dry {
		if !fl.quiet {
			fmt.Fprintln(os.Stderr, style.Dim("  --dry: not executing."))
		} else if fl.json {
			emitJSON(resp, fl.prompt, nil, 0)
		} else {
			// Non-TTY --dry: print the command to stdout so callers can
			// still capture it.
			if resp.Approach == model.ApproachScript && resp.Script != nil {
				fmt.Print(resp.Script.Body)
			} else {
				fmt.Println(resp.Command)
			}
		}
		auditEntry.UserDecision = "dry"
		if lerr == nil {
			_ = logger.Append(auditEntry)
		}
		return 0
	}

	// Decide: auto-confirm or prompt.
	autoConfirm := (fl.yes || cfg.AutoRun) && resp.Risk.AutoRunEligible()
	decision := tui.DecisionConfirm
	if !autoConfirm {
		if !tui.IsTTY(os.Stdin) || !tui.IsTTY(os.Stderr) {
			// We already promote piped-stdin to --yes for
			// auto-run-eligible risks. If we still end up here, the
			// risk is too high to auto-confirm through a pipe (e.g.
			// mutates/destructive/sudo). Be explicit about why.
			errf("intent: refusing to auto-run risk=%s without a TTY; re-run interactively or reduce the scope of the request", resp.Risk)
			auditEntry.UserDecision = "cancelled"
			if lerr == nil {
				_ = logger.Append(auditEntry)
			}
			return 7
		}
		// Loop on preview/edit until run or cancel.
		for {
			decision = tui.Confirm(os.Stdin, os.Stderr)
			switch decision {
			case tui.DecisionConfirm:
				goto execute
			case tui.DecisionCancel:
				auditEntry.UserDecision = "cancelled"
				if lerr == nil {
					_ = logger.Append(auditEntry)
				}
				return 2
			case tui.DecisionPreview:
				printPreview(resp, style)
			case tui.DecisionEdit:
				newCmd, _ := tui.EditLine(os.Stdin, os.Stderr, resp.Command)
				resp.Command = newCmd
				renderProposal(resp, false, style)
			}
		}
	}
execute:
	// --raw mode: emit the command and exit. Don't run.
	if fl.raw {
		if resp.Approach == model.ApproachScript && resp.Script != nil {
			fmt.Print(resp.Script.Body)
		} else {
			fmt.Println(resp.Command)
		}
		auditEntry.UserDecision = "raw"
		if lerr == nil {
			_ = logger.Append(auditEntry)
		}
		return 0
	}

	mode := xexec.ModeNormal
	if fl.sandbox && fl.ro {
		mode = xexec.ModeSandboxRO
	} else if fl.sandbox {
		mode = xexec.ModeSandbox
	}

	envExtras := []string{"INTENT_PIPE_FROM=intent"}
	var stdoutBuf, stderrBuf bytes.Buffer
	var stdout io.Writer = io.MultiWriter(os.Stdout, &stdoutBuf)
	if fl.json {
		// In --json mode stdout belongs to the envelope, so capture the
		// executed command's stdout instead of streaming it directly.
		stdout = &stdoutBuf
	}
	var stderr io.Writer = io.MultiWriter(os.Stderr, &stderrBuf)

	req := xexec.Request{
		Shell:  resp.Command,
		Mode:   mode,
		Stdin:  strings.NewReader(stdinData),
		Stdout: stdout,
		Stderr: stderr,
		Env:    envExtras,
	}
	if resp.Approach == model.ApproachScript && resp.Script != nil {
		req.Script = resp.Script.Body
		req.Interpreter = resp.Script.Interpreter
	}

	runRes, runErr := xexec.Run(tctx, req)
	if runErr != nil {
		errf("execution: %v", runErr)
	}

	if fl.json {
		emitJSON(resp, fl.prompt, stdoutBuf.Bytes(), runRes.ExitCode)
	}

	auditEntry.UserDecision = decisionLabel(autoConfirm)
	auditEntry.ExecutedCommand = runRes.Cmd
	auditEntry.ExitCode = &runRes.ExitCode
	auditEntry.DurationMS = runRes.Duration.Milliseconds()
	auditEntry.StdoutHash = audit.HashOutput(stdoutBuf.Bytes())
	auditEntry.StderrHash = audit.HashOutput(stderrBuf.Bytes())
	auditEntry.StderrExcerpt = stderrExcerpt(stderrBuf.Bytes())
	if lerr == nil {
		_ = logger.Append(auditEntry)
	}
	return runRes.ExitCode
}

func stderrExcerpt(b []byte) string {
	const limit = 4096
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n…"
}

func decisionLabel(auto bool) string {
	if auto {
		return "autorun"
	}
	return "confirmed"
}

func renderProposal(resp *model.Response, cached bool, s tui.Style) {
	cachedTag := ""
	if cached {
		cachedTag = "  " + s.Cyan("⚡ cached")
	}
	cmd := resp.Command
	if resp.Approach == model.ApproachScript && resp.Script != nil {
		cmd = "[" + resp.Script.Interpreter + " script]"
	}
	fmt.Fprintf(os.Stderr, "  %s %s%s\n", s.Bold("→"), s.Bold(cmd), cachedTag)
	if resp.Description != "" {
		fmt.Fprintf(os.Stderr, "  %s\n", s.Dim(resp.Description))
	}
	if resp.Risk != "" || resp.ExpectedRuntime != "" {
		fmt.Fprintf(os.Stderr, "  %s%s\n",
			s.Dim("risk: ")+s.RiskBadge(string(resp.Risk)),
			s.Dim("  runtime: ")+s.Dim(string(resp.ExpectedRuntime)),
		)
	}
}

func printPreview(resp *model.Response, s tui.Style) {
	fmt.Fprintln(os.Stderr, s.Dim("  --- preview ---"))
	if resp.Approach == model.ApproachScript && resp.Script != nil {
		fmt.Fprintf(os.Stderr, "  interpreter: %s\n", resp.Script.Interpreter)
		for _, line := range strings.Split(resp.Script.Body, "\n") {
			fmt.Fprintf(os.Stderr, "  | %s\n", line)
		}
	} else {
		fmt.Fprintf(os.Stderr, "  | %s\n", resp.Command)
	}
	fmt.Fprintln(os.Stderr, s.Dim("  ---"))
}

func executeForBool(ctx context.Context, resp *model.Response, fl *intentFlags, dirs state.Dirs) (xexec.Result, int) {
	req := xexec.Request{
		Shell:  resp.Command,
		Stdout: io.Discard,
		Stderr: io.Discard,
		Mode:   xexec.ModeNormal,
	}
	if resp.Approach == model.ApproachScript && resp.Script != nil {
		req.Script = resp.Script.Body
		req.Interpreter = resp.Script.Interpreter
	}
	r, _ := xexec.Run(ctx, req)
	if r.ExitCode == 0 {
		return r, 0
	}
	return r, 1
}

func emitJSON(resp *model.Response, prompt string, stdoutBytes []byte, exitCode int) {
	out := map[string]any{
		"intent_response": resp,
		"exit_code":       exitCode,
		"prompt":          prompt,
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		out["cwd"] = cwd
	}
	if stdoutBytes != nil {
		out["stdout"] = string(stdoutBytes)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}

// readStdinIfPiped reads stdin only when there is data to consume:
//   - regular file: read all
//   - named pipe / fifo: wait up to firstByteWait for the first byte, then
//     drain until EOF. The caller sizes firstByteWait to match the overall
//     command timeout so a chained `i A | i B` has enough slack while A's
//     human user reads the proposal and confirms, then A executes, then
//     flushes to the pipe. A supervisor that holds stdin open but never
//     writes still unblocks at firstByteWait and the caller proceeds with
//     empty stdin.
//   - TTY or char device: do nothing
//   - everything else (sockets, etc.): do nothing (safer than blocking)
//
// Once the first byte has arrived we clear the deadline and drain to EOF.
// The second return value is true when the read terminated in EOF (upstream
// closed the pipe, or there was no pipe to begin with). It's false when we
// bailed out on the deadline, which signals the caller that some upstream
// process is still running and may still be writing to the shared stderr.
func readStdinIfPiped(firstByteWait time.Duration) (string, bool) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", true
	}
	mode := info.Mode()
	if (mode & os.ModeCharDevice) != 0 {
		return "", true
	}
	if mode.IsRegular() {
		b, _ := io.ReadAll(os.Stdin)
		return string(b), true
	}
	if (mode & os.ModeNamedPipe) == 0 {
		return "", true
	}
	if firstByteWait <= 0 {
		firstByteWait = 60 * time.Second
	}

	// Named pipe. Wait for the first byte with a deadline; once we see
	// it, drain to EOF with no deadline.
	if err := os.Stdin.SetReadDeadline(time.Now().Add(firstByteWait)); err != nil {
		// Platforms where deadlines don't apply fall back to a
		// best-effort goroutine with the same bound. The goroutine
		// only delivers on full EOF, so a select-timeout means we
		// did *not* reach EOF.
		ch := make(chan []byte, 1)
		go func() {
			b, _ := io.ReadAll(os.Stdin)
			ch <- b
		}()
		select {
		case b := <-ch:
			return string(b), true
		case <-time.After(firstByteWait):
			return "", false
		}
	}
	defer func() { _ = os.Stdin.SetReadDeadline(time.Time{}) }()

	first := make([]byte, 1)
	n, err := os.Stdin.Read(first)
	if n == 0 {
		// Deadline exceeded before any data arrived. Upstream still
		// alive from our point of view; do not assume its UI is done.
		return "", false
	}
	// Got the first byte. Lift the deadline and drain the rest.
	_ = os.Stdin.SetReadDeadline(time.Time{})
	if err != nil {
		return string(first[:n]), false
	}
	rest, _ := io.ReadAll(os.Stdin)
	return string(first[:n]) + string(rest), true
}

func truncateForPrompt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n[... truncated " + fmt.Sprint(len(s)-max) + " bytes ...]"
}

// formatStdinForPrompt frames piped stdin for the model. When fromIntent
// is false it is labelled as raw stdin (the long-standing behavior). When
// fromIntent is true it is labelled as upstream-intent context, and if the
// payload is the JSON envelope emitted by emitJSON we unpack it into a
// short semantic summary so the downstream prompt does not have to teach
// the model how to parse the envelope. The summary includes the upstream
// natural-language prompt and cwd so chained invocations can preserve path
// context even when stdout only contains bare filenames.
func formatStdinForPrompt(stdinData string, fromIntent bool) string {
	if stdinData == "" {
		return ""
	}
	if fromIntent {
		if summary, ok := summarizeIntentEnvelope(stdinData); ok {
			return "\n\n[upstream intent result]\n" + summary
		}
		return "\n\n[upstream intent result follows]\n" + truncateForPrompt(stdinData, 8000)
	}
	return "\n\n[stdin contents follow]\n" + truncateForPrompt(stdinData, 8000)
}

// summarizeIntentEnvelope attempts to parse the JSON envelope produced by
// emitJSON and render the useful fields as a compact natural-language
// summary. Returns ok=false if the payload is not the envelope shape, so
// the caller can fall back to raw framing.
func summarizeIntentEnvelope(data string) (string, bool) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed[0] != '{' {
		return "", false
	}
	var env struct {
		IntentResponse *model.Response `json:"intent_response"`
		ExitCode       *int            `json:"exit_code"`
		Stdout         *string         `json:"stdout"`
		Prompt         string          `json:"prompt"`
		Cwd            string          `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return "", false
	}
	if env.IntentResponse == nil && env.ExitCode == nil && env.Stdout == nil && env.Prompt == "" && env.Cwd == "" {
		return "", false
	}
	var b strings.Builder
	if env.Prompt != "" {
		fmt.Fprintf(&b, "  prompt: %s\n", env.Prompt)
	}
	if env.Cwd != "" {
		fmt.Fprintf(&b, "  cwd: %s\n", env.Cwd)
	}
	if env.IntentResponse != nil {
		if env.IntentResponse.IntentSummary != "" {
			fmt.Fprintf(&b, "  summary: %s\n", env.IntentResponse.IntentSummary)
		}
		if env.IntentResponse.Command != "" {
			fmt.Fprintf(&b, "  command: %s\n", env.IntentResponse.Command)
		}
		if env.IntentResponse.Description != "" {
			fmt.Fprintf(&b, "  description: %s\n", env.IntentResponse.Description)
		}
	}
	if env.ExitCode != nil {
		fmt.Fprintf(&b, "  exit_code: %d\n", *env.ExitCode)
	}
	if env.Stdout != nil && strings.TrimSpace(*env.Stdout) != "" {
		fmt.Fprintf(&b, "  stdout: %s\n", truncateForPrompt(*env.Stdout, 2000))
	}
	return b.String(), true
}
