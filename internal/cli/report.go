package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
	if len(prompt) == 0 {
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
	userInput := strings.Join(prompt, " ")

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

	// Preferred path: if the backend supports schema-enforced structured
	// output (llamafile, llama.cpp, OpenAI-compatible), ask for proposals
	// in a task-specific schema. The grammar constrains the model at
	// token-generation time, so even a 1.5B local model cannot escape
	// into prose. No parsing heuristics required.
	sp.SetLabel("asking model for proposals...")
	proposals, structuredErr := askProposalsStructured(ctx, be, userInput)
	rawOutput := ""

	if structuredErr != nil {
		// Fall back to the envelope path (stdout_to_user contains JSON
		// as a string) with best-effort extraction. This is for backends
		// that don't support response_format schemas, or if llamafile
		// returned schema-compliant JSON that was still empty for some
		// reason.
		sp.SetLabel("retrying without schema enforcement...")
		store, _ := cache.Open(dirs.SkillsCachePath())
		eng := engine.New(store)
		proposals, rawOutput = askForProposals(ctx, eng, be, userInput, false)
		if proposals == nil {
			sp.SetLabel("retrying with strict JSON prompt...")
			proposals, rawOutput = askForProposals(ctx, eng, be, userInput, true)
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
	anyFailed := false
	anyCreated := false

	for i, m := range matches {
		fmt.Printf("\n[%d/%d] %s\n", i+1, len(matches), m.Proposal.Title)
		if m.IsDuplicate {
			fmt.Printf("  duplicate of #%d %q (score %.2f)\n", m.BestExisting.Number, m.BestExisting.Title, m.Score)
			fmt.Printf("  → would comment with: %s\n", trim(m.Proposal.Body, 120))
			if confirmReport(yes) {
				url, err := report.CommentOnIssue(ctx, m.BestExisting.Number,
					"From `i report`:\n\n"+m.Proposal.Body)
				if err != nil {
					errf("report: comment failed: %v", err)
					anyFailed = true
					continue
				}
				anyCreated = true
				fmt.Printf("  ✓ commented: %s\n", url)
			}
		} else {
			kept, dropped := report.FilterLabels(m.Proposal.Labels, known)
			if len(dropped) > 0 {
				fmt.Printf("  (dropping labels not on repo: %v)\n", dropped)
			}
			p := m.Proposal
			p.Labels = kept
			fmt.Printf("  new issue\n")
			fmt.Printf("  labels: %v\n", p.Labels)
			fmt.Printf("  body: %s\n", trim(p.Body, 200))
			if confirmReport(yes) {
				url, err := report.CreateIssue(ctx, p)
				if err != nil {
					errf("report: create failed: %v", err)
					anyFailed = true
					continue
				}
				anyCreated = true
				fmt.Printf("  ✓ created: %s\n", url)
			}
		}
	}
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

// confirmReport is the per-proposal interactive check. Defaults to
// YES because the user explicitly invoked `i report` intending to
// file issues — asking them to type "y" for every proposal after
// they already chose to report is noise. Pressing Enter (or y / yes)
// proceeds; only an explicit n / no / anything-else declines.
func confirmReport(yes bool) bool {
	if yes {
		return true
	}
	fmt.Print("  proceed? [Y/n] ")
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" || line == "y" || line == "yes" {
		return true
	}
	return false
}

func trim(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
	sys := `You convert user feedback into GitHub issue proposals.
- One proposal per distinct bug or feature in the input.
- title: <=80 chars, imperative mood ("Add --literal flag").
- body: concise markdown describing the problem or feature.
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

// askForProposals runs a single round-trip through the engine and tries
// to extract a proposal list from the model's output. When strict=true
// we use a short, pointed prompt that survives the small-model "helpful
// prose" failure mode better than the verbose instructions.
func askForProposals(ctx context.Context, eng *engine.Engine, be model.Backend, userInput string, strict bool) ([]report.Proposal, string) {
	var sys string
	if strict {
		sys = `Respond with ONLY a raw JSON array. No prose. No markdown code fences. No preamble.
Schema: [{"title": string, "body": string, "labels": string[], "kind": "bug" | "feature" | "doc"}]
Put the array into stdout_to_user with approach=inform.`
	} else {
		// Concrete one-shot example steers small models better than a
		// field-by-field description. We keep the verbose instruction
		// short so it doesn't crowd out the example in a small ctx.
		sys = `Convert the user's input into a JSON array of GitHub issue proposals.
Set approach=inform. Put the array (and ONLY the array, with no surrounding prose) in stdout_to_user.
Each item: {"title": short <=80 chars, "body": markdown, "labels": string[], "kind": "bug"|"feature"|"doc"}.
labels: ["bug","needs-triage"] for bugs; ["enhancement","needs-triage"] for features; ["documentation"] for docs.

Example:
  User input: "The CLI crashes when I pipe empty stdin. Also we should add colour."
  stdout_to_user: [
    {"title":"CLI crashes on empty piped stdin","body":"Reproduction: ...","labels":["bug","needs-triage"],"kind":"bug"},
    {"title":"Add colour output to CLI","body":"Improve readability by ...","labels":["enhancement","needs-triage"],"kind":"feature"}
  ]`
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
// roughly reflects what they asked for.
func synthesizeTitle(userInput string) string {
	s := strings.TrimSpace(userInput)
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
