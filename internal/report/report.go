// Package report implements `i report`: convert natural language into one or
// more GitHub issues, dedupe against existing open issues, and post comments
// instead of duplicates. v1 uses the `gh` CLI for auth (no token plumbing).
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

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
	if err := exec.CommandContext(ctx, "gh", "auth", "status").Run(); err != nil {
		return fmt.Errorf("gh not authenticated; run `gh auth login`")
	}
	return nil
}

// Search runs `gh search issues` and returns top N candidates.
func Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}
	args := []string{"search", "issues",
		"--repo", Repo,
		"--json", "number,title,url,state",
		"--limit", fmt.Sprintf("%d", limit),
		query,
	}
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("gh search: %w", err)
	}
	var results []SearchResult
	if err := json.Unmarshal(out, &results); err != nil {
		return nil, err
	}
	return results, nil
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
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CommentOnIssue adds a comment to an existing issue.
func CommentOnIssue(ctx context.Context, number int, body string) (string, error) {
	args := []string{"issue", "comment", fmt.Sprintf("%d", number),
		"--repo", Repo,
		"--body", body,
	}
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
