// Package report implements `i report`: convert natural language into one or
// more GitHub issues, dedupe against existing open issues, and post comments
// instead of duplicates. v1 uses the `gh` CLI for auth (no token plumbing).
package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/verbose"
)

// runGH runs `gh` with captured stdout AND stderr so error messages
// don't vanish. The default exec.Cmd.Output() only captures stdout,
// which means a failed `gh issue create --label bogus` returns a bare
// "exit status 1" — useless for diagnosing the real cause. We surface
// stderr in the returned error so users can see what GitHub actually
// complained about (missing label, auth, rate-limit, etc.).
func runGH(ctx context.Context, args ...string) ([]byte, error) {
	vl := verbose.FromContext(ctx)
	vl.Section("gh call")
	vl.KV("argv", "gh "+strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	t0 := time.Now()
	err := cmd.Run()
	elapsed := time.Since(t0).Round(time.Millisecond)

	vl.KV("elapsed", elapsed)
	vl.KV("exit_code", cmd.ProcessState.ExitCode())
	if stdout.Len() > 0 {
		vl.RawBytes("stdout", stdout.Bytes())
	}
	if stderr.Len() > 0 {
		vl.RawBytes("stderr", stderr.Bytes())
	}

	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

const Repo = "CoreyRDean/intent"

// Proposal is one issue the model wants to file.
type Proposal struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels"`
	Kind   string   `json:"kind"` // "bug" | "feature" | "doc"
}

// SearchResult is one of the candidate existing issues.
type SearchResult struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

// Available reports whether `gh` is installed and authenticated.
func Available(ctx context.Context) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not installed; install from https://cli.github.com")
	}
	if _, err := runGH(ctx, "auth", "status"); err != nil {
		return fmt.Errorf("gh not authenticated; run `gh auth login` (%v)", err)
	}
	return nil
}

// Search runs `gh search issues` and returns top N candidates.
func Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}
	out, err := runGH(ctx, "search", "issues",
		"--repo", Repo,
		"--json", "number,title,url,state",
		"--limit", fmt.Sprintf("%d", limit),
		query,
	)
	if err != nil {
		return nil, err
	}
	var results []SearchResult
	if err := json.Unmarshal(out, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// RepoLabels returns the set of label names configured on the repo.
// Used to filter proposal labels so we don't fail the whole create
// call because the model hallucinated a label that doesn't exist.
func RepoLabels(ctx context.Context) (map[string]bool, error) {
	out, err := runGH(ctx, "label", "list",
		"--repo", Repo,
		"--json", "name",
		"--limit", "100",
	)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(rows))
	for _, r := range rows {
		set[r.Name] = true
	}
	return set, nil
}

// FilterLabels returns the subset of proposed labels that actually
// exist on the repo, and the dropped ones separately so the caller
// can warn. A nil known map means "no filtering" (caller lacked
// access to repo label list).
func FilterLabels(proposed []string, known map[string]bool) (kept, dropped []string) {
	if known == nil {
		return proposed, nil
	}
	for _, l := range proposed {
		if known[l] {
			kept = append(kept, l)
		} else {
			dropped = append(dropped, l)
		}
	}
	return kept, dropped
}

// Similarity returns a 0..1 token-set ratio. Used for dedupe.
func Similarity(a, b string) float64 {
	at := tokens(a)
	bt := tokens(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}
	set := map[string]int{}
	for _, t := range at {
		set[t] |= 1
	}
	for _, t := range bt {
		set[t] |= 2
	}
	intersect, union := 0, 0
	for _, v := range set {
		if v == 3 {
			intersect++
		}
		union++
	}
	return float64(intersect) / float64(union)
}

func tokens(s string) []string {
	s = strings.ToLower(s)
	out := []string{}
	cur := ""
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur += string(r)
		} else if cur != "" {
			out = append(out, cur)
			cur = ""
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	sort.Strings(out)
	return dedupe(out)
}

func dedupe(s []string) []string {
	out := s[:0]
	prev := ""
	for _, v := range s {
		if v != prev {
			out = append(out, v)
		}
		prev = v
	}
	return out
}

// Match represents a proposal paired with its closest existing issue (if any).
type Match struct {
	Proposal     Proposal
	BestExisting *SearchResult
	Score        float64
	IsDuplicate  bool
}

// MatchProposals searches for each proposal and identifies likely duplicates.
const DuplicateThreshold = 0.55

func MatchProposals(ctx context.Context, proposals []Proposal) ([]Match, error) {
	out := make([]Match, 0, len(proposals))
	for _, p := range proposals {
		results, err := Search(ctx, p.Title, 5)
		if err != nil {
			return nil, err
		}
		var best *SearchResult
		bestScore := 0.0
		for i := range results {
			if results[i].State != "open" {
				continue
			}
			s := Similarity(p.Title, results[i].Title)
			if s > bestScore {
				bestScore = s
				best = &results[i]
			}
		}
		out = append(out, Match{
			Proposal:     p,
			BestExisting: best,
			Score:        bestScore,
			IsDuplicate:  best != nil && bestScore >= DuplicateThreshold,
		})
	}
	return out, nil
}

// CreateIssue creates a new issue. Returns the URL.
func CreateIssue(ctx context.Context, p Proposal) (string, error) {
	args := []string{"issue", "create",
		"--repo", Repo,
		"--title", p.Title,
		"--body", p.Body,
	}
	for _, l := range p.Labels {
		args = append(args, "--label", l)
	}
	out, err := runGH(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CommentOnIssue adds a comment to an existing issue.
func CommentOnIssue(ctx context.Context, number int, body string) (string, error) {
	out, err := runGH(ctx, "issue", "comment", fmt.Sprintf("%d", number),
		"--repo", Repo,
		"--body", body,
	)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
