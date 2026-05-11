package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/cache"
	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/engine"
	"github.com/CoreyRDean/intent/internal/model"
	"github.com/CoreyRDean/intent/internal/report"
	"github.com/CoreyRDean/intent/internal/state"
	"github.com/CoreyRDean/intent/internal/tui"
	"github.com/CoreyRDean/intent/internal/verbose"
)

func cmdReport(ctx context.Context, args []string) int {
	yes := false
	prompt := []string{}
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		default:
			prompt = append(prompt, a)
		}
	}
	stdinData, _ := readStdinIfPiped(reportStdinWait(ctx))
	userInput := buildReportUserInput(prompt, stdinData)
	if userInput == "" {
		errf("usage: i report <natural language describing one or more bugs/features>")
		return 1
	}
	if err := report.Available(ctx); err != nil {
		errf("report: %v", err)
		return 3
	}

	dirs, _ := state.Resolve()
	cfg, _ := config.Load(dirs.ConfigPath())
	if !ensureBackendReady(ctx, dirs, cfg) {
		return 3
	}
	be, _, err := buildBackendCtx(ctx, cfg.Backend, cfg, "")
	if err != nil {
		errf("report: %v", err)
		return 3
	}
	if isMockBackend(be) {
		errf("i report requires a real backend — run 'i doctor' to diagnose")
		return 3
	}

	vl := verbose.FromContext(ctx)
	vl.Section("report")
	vl.KV("user_input", userInput)
	vl.KV("repo", report.Repo)

	// Progress feedback: local model inference routinely takes 5-30s,
	// and gh API calls add another second or two. Without a spinner
	// the CLI looks frozen. We render to stderr so piping output
	// through e.g. `| pbcopy` still works cleanly. The spinner is a
	// no-op when stderr isn't a TTY (scripts, CI), so there's no
	// visual noise outside interactive use. In verbose mode the log
	// stream itself is the progress indicator, so we skip the
	// spinner to avoid garbling stderr.
	style := tui.DefaultStyle()
	var sp *tui.Spinner
	if !vl.Enabled() {
		sp = tui.NewSpinner(style)
		sp.Start("preparing proposals...")
	}
	// Belt-and-braces stop — every return path below also stops the
	// spinner explicitly before printing user-visible output, but
	// this catches any early error path we miss.
	defer sp.Stop()

	// Agentic preflight: let the model investigate the user's report
	// with read-only tools (read_file, grep, find_files, list_dir,
	// git_status, head_file, stat, which, help) and return a concise
	// evidence summary. This grounds proposal titles/bodies in real
	// file paths, error strings, and repo state instead of letting the
	// small local model hallucinate generic descriptions.
	//
	// We do this BEFORE the structured call because schema-constrained
	// sampling can't host tool calls — the model is forced to emit the
	// final shape immediately. So we gather evidence first (free-form,
	// agentic) and feed it into the structured call as augmented input.
	store, _ := cache.Open(dirs.SkillsCachePath())
	eng := engine.New(store)
	sp.SetLabel("investigating with tools...")
	evidence := gatherReportEvidence(ctx, eng, be, userInput, sp)
	augmented := userInput
	if evidence != "" {
		augmented = userInput + "\n\nEvidence gathered from a read-only investigation of the workspace:\n" + evidence
	}

	// Preferred path: if the backend supports schema-enforced structured
	// output (llamafile, llama.cpp, OpenAI-compatible), ask for proposals
	// in a task-specific schema. The grammar constrains the model at
	// token-generation time, so even a 1.5B local model cannot escape
	// into prose. No parsing heuristics required.
	sp.SetLabel("asking model for proposals...")
	proposals, structuredErr := askProposalsStructured(ctx, be, augmented)
	rawOutput := ""

	if structuredErr != nil {
		// Fall back to the envelope path (stdout_to_user contains JSON
		// as a string) with best-effort extraction. This is for backends
		// that don't support response_format schemas, or if llamafile
		// returned schema-compliant JSON that was still empty for some
		// reason. The fallback uses the same engine instance (and so
		// the same tool catalog) on the augmented input.
		sp.SetLabel("retrying without schema enforcement...")
		proposals, rawOutput = askForProposals(ctx, eng, be, augmented, false)
		if proposals == nil {
			sp.SetLabel("retrying with strict JSON prompt...")
			proposals, rawOutput = askForProposals(ctx, eng, be, augmented, true)
		}
	}
	if proposals == nil {
		// Stop the spinner — the synthesized-proposal path prints a
		// prompt and reads from stdin, which must not race the
		// spinner's stderr writes.
		sp.Stop()
		proposals = offerSynthesizedProposal(userInput, rawOutput, yes)
		if proposals == nil {
			return 1
		}
		// Re-arm for the remaining GitHub round-trips.
		if !vl.Enabled() {
			sp = tui.NewSpinner(style)
			sp.Start("continuing...")
		}
	}

	sp.SetLabel("checking GitHub for duplicates...")
	matches, err := report.MatchProposals(ctx, proposals)
	if err != nil {
		sp.Stop()
		errf("report: %v", err)
		return 3
	}

	// Fetch the repo's actual label set once so we can filter out labels
	// the model hallucinated (e.g. "needs-triage" when the repo has no
	// such label). Without this the whole `gh issue create` call fails
	// and we surface a confusing "exit status 1" to the user. If we
	// can't fetch labels for any reason we don't block — we'll just let
	// gh itself reject bad labels with the real error now that stderr
	// is captured.
	sp.SetLabel("fetching repo labels...")
	known, knownErr := report.RepoLabels(ctx)
	// Done with background work; stop the spinner now so the per-
	// proposal output below is the first thing the user sees.
	sp.Stop()
	if knownErr != nil {
		errf("report: could not fetch repo labels (%v); will pass through as-is", knownErr)
	}

	// Track the worst-case outcome so the overall command exit code
	// reflects reality. Previously cmdReport always returned 0 even
	// when every create failed, which hid failures from scripts.
	anyCreated, anyFailed := applyReportMatches(
		ctx,
		os.Stdout,
		matches,
		known,
		yes,
		report.CreateIssue,
		report.CommentOnIssue,
	)
	switch {
	case anyFailed:
		// At least one op failed; propagate. Use 3 to match the
		// pattern other commands here use for "backend/network" errors.
		return 3
	case !anyCreated:
		// No errors but nothing happened either (user declined every
		// prompt, or no proposals). Treat as 0 — declining is a
		// legitimate no-op.
		return 0
	}
	return 0
}

type reportCreateIssueFunc func(context.Context, report.Proposal) (string, error)
type reportCommentIssueFunc func(context.Context, int, string) (string, error)

func applyReportMatches(
	ctx context.Context,
	out io.Writer,
	matches []report.Match,
	known map[string]bool,
	yes bool,
	createIssue reportCreateIssueFunc,
	commentOnIssue reportCommentIssueFunc,
) (anyCreated, anyFailed bool) {
	for i, m := range matches {
		fmt.Fprintf(out, "\n[%d/%d] %s\n", i+1, len(matches), m.Proposal.Title)
		if m.IsDuplicate {
			fmt.Fprintf(out, "  duplicate of #%d %q (score %.2f)\n", m.BestExisting.Number, m.BestExisting.Title, m.Score)
			fmt.Fprintf(out, "  → would comment with: %s\n", trim(m.Proposal.Body, 120))
			if !yes {
				fmt.Fprintln(out, "  → dry run only; pass --yes to post the comment")
				continue
			}
			url, err := commentOnIssue(ctx, m.BestExisting.Number, "From `i report`:\n\n"+m.Proposal.Body)
			if err != nil {
				errf("report: comment failed: %v", err)
				anyFailed = true
				continue
			}
			anyCreated = true
			fmt.Fprintf(out, "  ✓ commented: %s\n", url)
			continue
		}

		kept, dropped := report.FilterLabels(m.Proposal.Labels, known)
		if len(dropped) > 0 {
			fmt.Fprintf(out, "  (dropping labels not on repo: %v)\n", dropped)
		}
		p := m.Proposal
		p.Labels = kept
		fmt.Fprintln(out, "  new issue")
		fmt.Fprintf(out, "  labels: %v\n", p.Labels)
		fmt.Fprintf(out, "  body: %s\n", trim(p.Body, 200))
		if !yes {
			fmt.Fprintln(out, "  → would create a new issue (pass --yes to execute)")
			continue
		}
		url, err := createIssue(ctx, p)
		if err != nil {
			errf("report: create failed: %v", err)
			anyFailed = true
			continue
		}
		anyCreated = true
		fmt.Fprintf(out, "  ✓ created: %s\n", url)
	}
	return anyCreated, anyFailed
}

func trim(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func reportStdinWait(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		if wait := time.Until(deadline); wait > 0 {
			return wait
		}
	}
	return 60 * time.Second
}

func buildReportUserInput(promptArgs []string, stdinData string) string {
	argText := strings.TrimSpace(strings.Join(promptArgs, " "))
	stdinText := strings.TrimSpace(stdinData)
	switch {
	case argText == "":
		return stdinText
	case stdinText == "":
		return argText
	default:
		return argText + "\n\n" + stdinText
	}
}

// reportSchemaJSON is the JSON schema the model's output must conform
// to when we go through the structured path. The backend's grammar/
// response_format enforces this at token generation, so the model
// cannot emit prose even if it wants to — it's constrained to produce
// exactly this shape. This is what the user meant by "use the runtime
// properly": constrain the output where it actually matters.
const reportSchemaJSON = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["proposals"],
  "properties": {
    "proposals": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["title", "body", "labels", "kind"],
        "properties": {
          "title":  { "type": "string", "minLength": 1, "maxLength": 80 },
          "body":   { "type": "string", "minLength": 1 },
          "labels": {
            "type": "array",
            "minItems": 1,
            "items": { "type": "string", "minLength": 1 }
          },
          "kind":   { "type": "string", "enum": ["bug", "feature", "doc"] }
        }
      }
    }
  }
}`

// askProposalsStructured uses the backend's schema-enforced output
// capability (if available) to get a proposals list that cannot fail
// to parse by construction. Returns (proposals, nil) on success, or
// (nil, err) if the backend can't do structured output or the call
// itself failed.
func askProposalsStructured(ctx context.Context, be model.Backend, userInput string) ([]report.Proposal, error) {
	sb, ok := be.(model.StructuredBackend)
	if !ok {
		return nil, model.ErrStructuredUnsupported
	}
	// Short system prompt is fine here — the grammar is doing the heavy
	// lifting. We don't need an example because the model literally
	// cannot produce anything but the target shape.
	//
	// The hard part the small model tends to get wrong: when the user
	// pastes a failure transcript (their prior prompt + the CLI's bad
	// output + a narrative about what should have happened), the model
	// latches onto the embedded prompt as a fresh feature request and
	// files issues to *implement* it. The correct read is that the
	// embedded prompt + output are evidence of a CLI behavior bug, and
	// the issue should be about `intent` / `i` itself. The scope
	// paragraph below is load-bearing; do not trim it casually.
	sys := `You convert user feedback about the intent CLI (invoked as 'i' or 'intent') into GitHub issue proposals for its own repository.

SCOPE (critical): Every proposal describes what 'intent' / 'i' should do differently. Proposals NEVER describe the tasks the user was trying to accomplish WITH 'i'.
If the feedback embeds a prior 'i' invocation — the prompt the user gave it, a preview of its output, or a script it produced — treat those as EVIDENCE of CLI behavior, not as features to implement. The issue is about how 'i' handled (or should have handled) that input.
Phrases like "failure case", "failure report", "expected X, got Y", a quoted 'i ...' invocation, or an indented output preview are strong signals that the input is a meta-report. In that case produce ONE issue describing the CLI behavior bug, not one issue per topic mentioned inside the transcript.

FORMAT:
- One proposal per distinct CLI behavior bug or feature in the feedback.
- title: <=80 chars, imperative mood, about the CLI ("Ground alias edits in actual ~/.zshrc contents").
- body: concise markdown. For failure transcripts include: observed behavior, expected behavior, and the quoted user prompt + quoted CLI output as evidence.
- labels: for bugs ["bug","needs-triage"]; for features ["enhancement","needs-triage"]; for docs ["documentation"].
- kind: "bug" | "feature" | "doc".`
	messages := []model.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: userInput},
	}
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	raw, err := sb.CompleteStructured(tctx, model.StructuredRequest{
		Messages:    messages,
		SchemaJSON:  []byte(reportSchemaJSON),
		Temperature: 0.1, // low — we want determinism on structured tasks.
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Proposals []report.Proposal `json:"proposals"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		// Should be impossible given schema enforcement, but guard
		// against backend implementations that don't actually constrain.
		return nil, fmt.Errorf("structured output did not match report schema: %w", err)
	}
	if len(out.Proposals) == 0 {
		return nil, fmt.Errorf("structured output had zero proposals")
	}
	return out.Proposals, nil
}

// gatherReportEvidence runs an agentic, read-only investigation of the
// user's bug report against the workspace. The result is a free-form
// evidence summary suitable for prepending to the user's input before
// the structured proposal call.
//
// We do this as a separate engine pass — not folded into the structured
// call — because schema-constrained sampling forecloses tool_call: the
// model is forced to emit the final shape immediately. Splitting the
// turn lets us keep small-model shape guarantees AND ground the output
// in real files and error strings.
//
// On any failure (no tools used, model refuses, timeout) we return ""
// and let the caller fall through to the un-augmented structured call.
// Evidence gathering is opportunistic, not load-bearing.
func gatherReportEvidence(ctx context.Context, eng *engine.Engine, be model.Backend, userInput string, sp *tui.Spinner) string {
	sys := `You are gathering evidence for a GitHub issue report against the 'intent' CLI (invoked as 'i' or 'intent'). The workspace IS the 'intent' source repo.

The user's feedback is below. It is feedback ABOUT the CLI's behavior, not a request to implement something. If it embeds a prior 'i' invocation (the prompt the user gave, a preview of the output, a script 'i' generated), those are symptoms of the CLI's behavior — ground your evidence in the CLI's source, not in whatever task the embedded prompt was about.

You MUST investigate before answering — start with at least one tool call. Default first move: git_status() to confirm the repo, then find_files / grep to locate the CLI code path responsible for the observed behavior. Then read_file / head_file the relevant region.

Examples of what to gather:
- If the user describes a bug in a specific subcommand ('i report', 'i explain', natural-language 'i'), find the implementation in internal/cli/ and read the relevant region.
- If the user quotes an error or log line, grep the codebase for that string to locate the source.
- If the user describes a behavior gap ("I expected 'i' to read ~/.zshrc but it didn't"), find where 'i' builds its model context or chooses tools, and quote the offending lines.
- If the user mentions a config value, look it up in the config loader (internal/config/).
Do NOT investigate the embedded task itself (don't go read the user's ~/.zshrc, don't research whatever external tool they mentioned). That is not the subject of the issue.

When you have enough evidence, return approach=inform with a concise evidence summary in stdout_to_user. Plain text or short bullets, not JSON. Cite file paths with line numbers (file.go:123) where possible. Stay under ~30 lines.

Only return approach=inform on step 1 (skipping all tools) if the user's input is purely a feature request for the CLI with no existing code to ground against (e.g. "add a new 'i X' subcommand that does Y"). Otherwise: tools first, answer second.`
	prompt := sys + "\n\nUser input:\n" + userInput
	tctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := eng.Run(tctx, prompt, engine.Options{
		Backend:      be,
		MaxToolSteps: 0, // engine bumps to default 12
		OnPhase: func(p string) {
			if sp == nil {
				return
			}
			// Wrap the engine's generic labels in our outer-phase
			// context so the spinner doesn't sit at "Understanding..."
			// for 30s during local-model inference. The engine emits
			// "Understanding..." before the first model call and
			// "Reading context (toolname)..." for each tool call.
			switch p {
			case "Understanding...":
				sp.SetLabel("investigating workspace...")
			default:
				sp.SetLabel("investigating: " + p)
			}
		},
	})
	if err != nil || res == nil || res.Response == nil {
		return ""
	}
	return strings.TrimSpace(res.Response.StdoutToUser)
}

// askForProposals runs a single round-trip through the engine and tries
// to extract a proposal list from the model's output. When strict=true
// we use a short, pointed prompt that survives the small-model "helpful
// prose" failure mode better than the verbose instructions.
func askForProposals(ctx context.Context, eng *engine.Engine, be model.Backend, userInput string, strict bool) ([]report.Proposal, string) {
	var sys string
	if strict {
		sys = `Respond with ONLY a raw JSON array. No prose. No markdown code fences. No preamble.

SCOPE: Each proposal is about the 'intent' CLI ('i') itself — how it should behave differently. NOT about tasks the user embedded in the feedback. If the input quotes a prior 'i' invocation or its output, treat that as EVIDENCE of a CLI behavior, not as a feature to implement.

Schema: [{"title": string, "body": string, "labels": string[], "kind": "bug" | "feature" | "doc"}]
Put the array into stdout_to_user with approach=inform.

You MAY use read-only tools (read_file, head_file, list_dir, grep, find_files,
git_status, which, help) BEFORE the final answer to ground titles and bodies in
real file paths, error messages, or repo state. The FINAL response must still
be approach=inform with the JSON array — tools are for evidence gathering, not
the final shape.`
	} else {
		// Concrete one-shot example steers small models better than a
		// field-by-field description. We keep the verbose instruction
		// short so it doesn't crowd out the example in a small ctx.
		// The failure-transcript example is load-bearing: without it
		// the small model files issues about the user's embedded task
		// instead of the CLI behavior bug being reported.
		sys = `Convert the user's feedback about the 'intent' CLI into a JSON array of GitHub issue proposals for its own repo.

SCOPE: Every proposal is about 'intent' / 'i' behavior. If the feedback embeds a prior 'i' invocation — the user's prompt, a preview, a generated script — that is evidence of a CLI bug, not a feature request to implement. One failure transcript = one CLI issue, not one issue per topic mentioned inside it.

Set approach=inform. Put the array (and ONLY the array, with no surrounding prose) in stdout_to_user.
Each item: {"title": short <=80 chars, "body": markdown, "labels": string[], "kind": "bug"|"feature"|"doc"}.
labels: ["bug","needs-triage"] for bugs; ["enhancement","needs-triage"] for features; ["documentation"] for docs.

You MAY use read-only tools (read_file, head_file, list_dir, grep, find_files,
git_status, which, help) BEFORE producing the final answer to ground titles and
bodies in real evidence — file paths, error strings, line numbers, repo state.
The FINAL response must still be approach=inform with the JSON array.

Example A (direct feedback):
  User input: "The CLI crashes when I pipe empty stdin. Also we should add colour."
  stdout_to_user: [
    {"title":"CLI crashes on empty piped stdin","body":"Reproduction: ...","labels":["bug","needs-triage"],"kind":"bug"},
    {"title":"Add colour output to CLI","body":"Improve readability by ...","labels":["enhancement","needs-triage"],"kind":"feature"}
  ]

Example B (failure transcript):
  User input: "Failure Case Report: I expected 'i' to read my ~/.zshrc before editing it. Instead it emitted a bash snippet in a quote block. Below: i \"update my agi alias...\" -> [zsh script preview]"
  stdout_to_user: [
    {"title":"Ground file-edit generations in the actual file contents","body":"Observed: 'i' produced a zsh snippet for editing ~/.zshrc without first reading the file. Expected: 'i' should read_file(~/.zshrc), then produce an editing command that modifies the real content.\n\nEvidence (quoted from report):\n> i \"update my agi alias...\" -> [zsh script preview]","labels":["bug","needs-triage"],"kind":"bug"}
  ]
  (Note: we do NOT propose "Add --profile support to the AGI alias" — that was the user's task, not a CLI bug.)`
	}
	prompt := sys + "\n\nUser input:\n" + userInput
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := eng.Run(tctx, prompt, engine.Options{Backend: be, MaxToolSteps: 0})
	if err != nil {
		return nil, ""
	}
	raw := res.Response.StdoutToUser
	if res.Response.Approach != model.ApproachInform || raw == "" {
		return nil, raw
	}
	return extractProposals(raw), raw
}

// extractProposals tries to pull a JSON proposal list out of free-form
// model output in four descending orders of strictness:
//
//  1. The trimmed string parses as a JSON array directly.
//  2. The string contains a ```json … ``` (or plain ``` … ```) fence
//     whose body parses as an array.
//  3. The first '[' through its matching ']' parses as an array.
//  4. The first '{' through its matching '}' parses as a single object
//     that we wrap into a one-element array.
//
// Returns nil if none of these succeed.
func extractProposals(raw string) []report.Proposal {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if p := tryUnmarshalArray(s); p != nil {
		return p
	}
	if body := extractFenced(s); body != "" {
		if p := tryUnmarshalArray(body); p != nil {
			return p
		}
		if p := tryUnmarshalObject(body); p != nil {
			return p
		}
	}
	if span := balancedSpan(s, '[', ']'); span != "" {
		if p := tryUnmarshalArray(span); p != nil {
			return p
		}
	}
	if span := balancedSpan(s, '{', '}'); span != "" {
		if p := tryUnmarshalObject(span); p != nil {
			return p
		}
	}
	return nil
}

func tryUnmarshalArray(s string) []report.Proposal {
	var p []report.Proposal
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return nil
	}
	return p
}

func tryUnmarshalObject(s string) []report.Proposal {
	var one report.Proposal
	if err := json.Unmarshal([]byte(s), &one); err != nil {
		return nil
	}
	if one.Title == "" {
		return nil
	}
	return []report.Proposal{one}
}

// fencedRe matches ``` or ```json opening fences. The body is captured
// greedily up to the next ``` on its own line-ish position.
var fencedRe = regexp.MustCompile("(?s)```(?:json|JSON)?\\s*\\n(.*?)\\n```")

func extractFenced(s string) string {
	m := fencedRe.FindStringSubmatch(s)
	if len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// balancedSpan returns the substring from the first `open` to the
// matching `close` at the same nesting depth. Handles quoted strings
// (including escaped quotes) so a '{' inside a JSON string doesn't
// confuse the matcher. Returns "" if no balanced span exists.
func balancedSpan(s string, open, close byte) string {
	start := strings.IndexByte(s, open)
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if esc {
			esc = false
			continue
		}
		if inStr {
			switch c {
			case '\\':
				esc = true
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// offerSynthesizedProposal is the escape hatch when the model twice
// refused to emit parseable JSON. We build a single proposal from the
// user's own natural-language input so they still land a usable issue.
// The model's raw output — which is usually a decent expansion — goes
// into the body as "AI-suggested context", clearly labelled.
//
// Returns nil if the user declines or we're non-interactive and can't
// confirm safely.
func offerSynthesizedProposal(userInput, rawModelOutput string, autoYes bool) []report.Proposal {
	errf("report: local model didn't return parseable JSON (common with small models).")
	if strings.TrimSpace(rawModelOutput) != "" {
		fmt.Fprintln(os.Stderr, "  raw model output (kept as issue context):")
		fmt.Fprintln(os.Stderr, indent(trim(rawModelOutput, 400), "    "))
	}

	title := synthesizeTitle(userInput)
	body := userInput
	if strings.TrimSpace(rawModelOutput) != "" {
		body += "\n\n---\n\n<details><summary>AI-suggested context (unstructured)</summary>\n\n" +
			rawModelOutput + "\n\n</details>"
	}
	labels := []string{"enhancement", "needs-triage"}
	kind := "feature"
	// Rough heuristic: presence of "bug", "crash", "broken", "regression"
	// nudges kind; everything else stays feature. False positives are
	// cheap — the user can edit after creation.
	lc := strings.ToLower(userInput)
	switch {
	case strings.Contains(lc, "bug"),
		strings.Contains(lc, "crash"),
		strings.Contains(lc, "broken"),
		strings.Contains(lc, "regression"),
		strings.Contains(lc, "panic"),
		strings.Contains(lc, "error"):
		labels = []string{"bug", "needs-triage"}
		kind = "bug"
	case strings.Contains(lc, "docs"),
		strings.Contains(lc, "documentation"),
		strings.Contains(lc, "readme"):
		labels = []string{"documentation"}
		kind = "doc"
	}
	p := report.Proposal{Title: title, Body: body, Labels: labels, Kind: kind}

	fmt.Fprintln(os.Stderr, "  proposed single issue:")
	fmt.Fprintf(os.Stderr, "    title: %s\n", p.Title)
	fmt.Fprintf(os.Stderr, "    labels: %v\n", p.Labels)
	fmt.Fprintln(os.Stderr, "  (body will be your original input plus the raw model context.)")

	if !autoYes {
		// Default Y: the user explicitly asked to report, and declining
		// this fallback means they get nothing. If the degraded shape
		// isn't acceptable they can still cancel by typing 'n'.
		fmt.Fprint(os.Stderr, "  create this single issue instead? [Y/n] ")
		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line != "" && line != "y" && line != "yes" {
			return nil
		}
	}
	return []report.Proposal{p}
}

// synthesizeTitle takes the first sentence-ish span of user input and
// trims it to ~80 chars so the synthesized issue has a title that
// roughly reflects what they asked for. Strips common meta-prefixes
// ("Failure Case Report:", "Bug Report:", etc.) up front so the
// resulting title describes the subject, not the form.
func synthesizeTitle(userInput string) string {
	s := strings.TrimSpace(userInput)
	// Strip meta-framing prefixes the user often adds when reporting a
	// CLI failure. These describe what the input IS, not what it's
	// ABOUT, and they eat title real estate. Case-insensitive, one
	// pass — we only care about the outer layer.
	for _, prefix := range []string{
		"failure case report:",
		"failure report:",
		"bug report:",
		"feature request:",
		"feedback:",
	} {
		if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			break
		}
	}
	// Prefer the first sentence boundary.
	for _, sep := range []string{". ", "? ", "! ", "\n"} {
		if i := strings.Index(s, sep); i > 0 && i < 120 {
			s = s[:i]
			break
		}
	}
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}

func indent(s, prefix string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
