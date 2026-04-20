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
	"github.com/CoreyRDean/intent/internal/version"
)

// intentFlags is the parsed v1 flag set for natural-language mode.
type intentFlags struct {
	yes      bool
	dry      bool
	sandbox  bool
	ro       bool
	json     bool
	raw      bool
	quiet    bool
	boolean  bool
	explain  bool
	noCache  bool
	timeout  time.Duration
	backend  string
	modelTag string
	n        int
	prompt   string
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

	// Allow flags interleaved with the natural-language prompt.
	// We pre-extract recognized flags and treat the rest as the prompt.
	known := map[string]bool{
		"--yes": true, "-y": true,
		"--dry":     true,
		"--sandbox": true,
		"--ro":      true,
		"--json":    true,
		"--raw":     true,
		"--quiet":   true, "-q": true,
		"--bool":     true,
		"--explain":  true,
		"--no-cache": true,
	}
	knownVal := map[string]bool{
		"--timeout": true,
		"--backend": true,
		"--model":   true,
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
		out.json = true
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
	// from a non-TTY supervisor that didn't close stdin).
	stdinData := readStdinIfPiped()

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

	// Build the prompt: include stdin as context unless --raw says otherwise.
	finalPrompt := fl.prompt
	if stdinData != "" {
		finalPrompt = fl.prompt + "\n\n[stdin contents follow]\n" + truncateForPrompt(stdinData, 8000)
	}

	// Build the backend.
	backendName := cfg.Backend
	if fl.backend != "" {
		backendName = fl.backend
	}
	be, isFallback, err := buildBackend(backendName, cfg, fl.modelTag)
	if err != nil {
		errf("backend: %v", err)
		return 3
	}
	printMockFallbackBanner(isFallback)

	// Cache & engine.
	store, _ := cache.Open(dirs.SkillsCachePath())
	eng := engine.New(store)

	style := tui.DefaultStyle()
	var sp *tui.Spinner
	if !fl.quiet && tui.IsTTY(os.Stderr) {
		sp = tui.NewSpinner(style)
		sp.Start("Invoking...")
		defer sp.Stop()
	}

	tctx, cancel := context.WithTimeout(ctx, fl.timeout)
	defer cancel()

	res, err := eng.Run(tctx, finalPrompt, engine.Options{
		Backend:      be,
		MaxToolSteps: cfg.MaxToolSteps,
		UseCache:     !fl.noCache && cfg.CacheEnabled,
		WriteCache:   !fl.noCache && cfg.CacheEnabled,
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
		emitJSON(resp, nil, 0)
		return 0
	}

	// Handle terminal approaches.
	switch resp.Approach {
	case model.ApproachInform:
		if fl.json {
			emitJSON(resp, []byte(resp.StdoutToUser), 0)
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

	// Render the proposal and decide.
	if !fl.quiet && !fl.explain {
		renderProposal(resp, res.CacheHit, style)
	}

	if fl.explain {
		if fl.json {
			emitJSON(resp, nil, 0)
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
			emitJSON(resp, nil, 0)
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
			errf("intent: confirmation required (risk=%s) but no TTY available; pass --yes for safe/network or run interactively", resp.Risk)
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
	var stdout io.Writer = os.Stdout
	var stderr io.Writer = os.Stderr
	if fl.json {
		stdout = io.MultiWriter(os.Stdout, &stdoutBuf)
		stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	}

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
		emitJSON(resp, stdoutBuf.Bytes(), runRes.ExitCode)
	}

	auditEntry.UserDecision = decisionLabel(autoConfirm)
	auditEntry.ExecutedCommand = runRes.Cmd
	auditEntry.ExitCode = &runRes.ExitCode
	auditEntry.DurationMS = runRes.Duration.Milliseconds()
	auditEntry.StdoutHash = audit.HashOutput(stdoutBuf.Bytes())
	auditEntry.StderrHash = audit.HashOutput(stderrBuf.Bytes())
	if lerr == nil {
		_ = logger.Append(auditEntry)
	}
	return runRes.ExitCode
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

func emitJSON(resp *model.Response, stdoutBytes []byte, exitCode int) {
	out := map[string]any{
		"intent_response": resp,
		"exit_code":       exitCode,
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
//   - named pipe / fifo: poll with a short deadline; read what is available
//   - TTY or char device: do nothing
//   - everything else: do nothing (safer than blocking)
//
// This makes `i hello` instantly return when launched under a supervisor
// (e.g. a CI runner, an editor's task runner) that holds stdin open but
// never writes to it.
func readStdinIfPiped() string {
	info, err := os.Stdin.Stat()
	if err != nil {
		return ""
	}
	mode := info.Mode()
	if (mode & os.ModeCharDevice) != 0 {
		return ""
	}
	if mode.IsRegular() {
		b, _ := io.ReadAll(os.Stdin)
		return string(b)
	}
	if (mode & os.ModeNamedPipe) != 0 {
		// Try to drain with a short deadline. We don't have a portable
		// non-blocking read; spawn a goroutine and time-bound the wait.
		ch := make(chan []byte, 1)
		go func() {
			b, _ := io.ReadAll(os.Stdin)
			ch <- b
		}()
		select {
		case b := <-ch:
			return string(b)
		case <-time.After(200 * time.Millisecond):
			// No data available; treat as empty stdin.
			return ""
		}
	}
	return ""
}

func truncateForPrompt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n[... truncated " + fmt.Sprint(len(s)-max) + " bytes ...]"
}
