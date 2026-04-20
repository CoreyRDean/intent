package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/cache"
	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/engine"
	"github.com/CoreyRDean/intent/internal/model"
	"github.com/CoreyRDean/intent/internal/report"
	"github.com/CoreyRDean/intent/internal/state"
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
	be, _, err := buildBackend(cfg.Backend, cfg, "")
	if err != nil {
		errf("report: %v", err)
		return 3
	}
	if isMockBackend(be) {
		errf("i report requires a real backend — run 'i doctor' to diagnose")
		return 3
	}
	store, _ := cache.Open(dirs.SkillsCachePath())
	eng := engine.New(store)

	sysAddendum := `Convert the user's input into one or more concise GitHub issue proposals.
Set approach=inform and put a JSON array of {title, body, labels[], kind} into stdout_to_user.
Do not include surrounding text. Each title is short (~80 char). Body is markdown.
labels: ["bug","needs-triage"] for bug reports; ["enhancement","needs-triage"] for features; ["documentation"] for doc.
kind in {bug, feature, doc}.`
	prompt2 := sysAddendum + "\n\nUser input:\n" + strings.Join(prompt, " ")
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := eng.Run(tctx, prompt2, engine.Options{Backend: be, MaxToolSteps: 0})
	if err != nil {
		errf("report: %v", err)
		return 3
	}
	if res.Response.Approach != model.ApproachInform || res.Response.StdoutToUser == "" {
		errf("report: model did not produce an issue list")
		return 1
	}
	var proposals []report.Proposal
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Response.StdoutToUser)), &proposals); err != nil {
		errf("report: model output not parseable as proposals: %v", err)
		fmt.Fprintln(os.Stderr, res.Response.StdoutToUser)
		return 1
	}

	matches, err := report.MatchProposals(ctx, proposals)
	if err != nil {
		errf("report: %v", err)
		return 3
	}

	for i, m := range matches {
		fmt.Printf("\n[%d/%d] %s\n", i+1, len(matches), m.Proposal.Title)
		if m.IsDuplicate {
			fmt.Printf("  duplicate of #%d %q (score %.2f)\n", m.BestExisting.Number, m.BestExisting.Title, m.Score)
			fmt.Printf("  → would comment with: %s\n", trim(m.Proposal.Body, 120))
			if confirmReport(yes) {
				url, err := report.CommentOnIssue(ctx, m.BestExisting.Number,
					"From `i report`:\n\n"+m.Proposal.Body)
				if err != nil {
					errf("report: %v", err)
					continue
				}
				fmt.Printf("  ✓ commented: %s\n", url)
			}
		} else {
			fmt.Printf("  new issue\n")
			fmt.Printf("  body: %s\n", trim(m.Proposal.Body, 200))
			if confirmReport(yes) {
				url, err := report.CreateIssue(ctx, m.Proposal)
				if err != nil {
					errf("report: %v", err)
					continue
				}
				fmt.Printf("  ✓ created: %s\n", url)
			}
		}
	}
	return 0
}

func confirmReport(yes bool) bool {
	if yes {
		return true
	}
	fmt.Print("  proceed? [y/N] ")
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

func trim(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
