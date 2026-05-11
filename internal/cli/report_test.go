package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/CoreyRDean/intent/internal/report"
)

// TestExtractProposals covers the real-world failure modes small local
// models have produced when asked to emit issue-proposal JSON.
func TestExtractProposals(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantN   int
		wantTop string // first proposal's title, or "" if n=0
	}{
		{
			name:    "strict_array",
			in:      `[{"title":"Add -l flag","body":"why","labels":["enhancement"],"kind":"feature"}]`,
			wantN:   1,
			wantTop: "Add -l flag",
		},
		{
			name: "markdown_json_fence",
			in: "Sure, here you go:\n\n```json\n" +
				`[{"title":"Add -l flag","body":"why","labels":["enhancement"],"kind":"feature"}]` +
				"\n```\nHope that helps!",
			wantN:   1,
			wantTop: "Add -l flag",
		},
		{
			name:    "prose_around_array",
			in:      "To solve this we can propose:\n[{\"title\":\"Add -l flag\",\"body\":\"why\",\"labels\":[\"enhancement\"],\"kind\":\"feature\"}]\nThanks.",
			wantN:   1,
			wantTop: "Add -l flag",
		},
		{
			name:    "single_object_not_array",
			in:      `{"title":"Add -l flag","body":"why","labels":["enhancement"],"kind":"feature"}`,
			wantN:   1,
			wantTop: "Add -l flag",
		},
		{
			name:    "pure_prose_no_json",
			in:      "To address this requirement, we can propose the following GitHub issue proposal:\n\n## Proposal\n\n### Description\n\nWe need to add a -l flag.",
			wantN:   0,
			wantTop: "",
		},
		{
			name:    "nested_brackets_in_body",
			in:      `[{"title":"x","body":"see [docs](a) and {y}","labels":["bug"],"kind":"bug"}]`,
			wantN:   1,
			wantTop: "x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractProposals(tc.in)
			if len(got) != tc.wantN {
				t.Fatalf("len=%d want %d (got=%+v)", len(got), tc.wantN, got)
			}
			if tc.wantN > 0 && got[0].Title != tc.wantTop {
				t.Fatalf("first title=%q want %q", got[0].Title, tc.wantTop)
			}
		})
	}
}

func TestSynthesizeTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{
			in:   "We need to add a -l flag. It forces natural language parsing.",
			want: "We need to add a -l flag",
		},
		{
			in:   "CLI crashes on empty pipe",
			want: "CLI crashes on empty pipe",
		},
		{
			in:   "Really, really, really long description that keeps going and going far past the eighty-character soft title limit we enforce",
			want: "Really, really, really long description that keeps going and going far past t...",
		},
		// Prefix stripping: when the user frames their feedback as a
		// failure/bug report, the prefix is noise that eats title real
		// estate. These cases cover the common variants.
		{
			in:   "Failure Case Report: i did not read my ~/.zshrc before editing it",
			want: "i did not read my ~/.zshrc before editing it",
		},
		{
			in:   "failure report: crash on startup",
			want: "crash on startup",
		},
		{
			in:   "Bug Report: exit code is 0 even when the backend fails",
			want: "exit code is 0 even when the backend fails",
		},
		{
			in:   "Feature request: add colour to the proposal preview",
			want: "add colour to the proposal preview",
		},
	}
	for _, tc := range cases {
		got := synthesizeTitle(tc.in)
		if got != tc.want {
			t.Errorf("synthesizeTitle(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildReportUserInput(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{
			name: "args_only",
			args: []string{"first", "natural", "language"},
			want: "first natural language",
		},
		{
			name:  "stdin_only",
			stdin: "second natural language\n",
			want:  "second natural language",
		},
		{
			name:  "args_then_stdin",
			args:  []string{"first", "natural", "language"},
			stdin: "second natural language\n",
			want:  "first natural language\n\nsecond natural language",
		},
		{
			name:  "ignores_blank_stdin",
			args:  []string{"first", "natural", "language"},
			stdin: "\n\n",
			want:  "first natural language",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildReportUserInput(tc.args, tc.stdin); got != tc.want {
				t.Fatalf("buildReportUserInput(%q, %q)=%q want %q", tc.args, tc.stdin, got, tc.want)
			}
		})
	}
}

func TestApplyReportMatches_DryRunSkipsWrites(t *testing.T) {
	t.Parallel()

	matches := []report.Match{
		{
			Proposal: report.Proposal{
				Title: "Existing bug",
				Body:  "comment body",
			},
			BestExisting: &report.SearchResult{
				Number: 42,
				Title:  "Existing bug",
			},
			Score:       0.91,
			IsDuplicate: true,
		},
		{
			Proposal: report.Proposal{
				Title:  "New bug",
				Body:   "new issue body",
				Labels: []string{"bug", "needs-triage"},
			},
		},
	}

	var out bytes.Buffer
	createCalls := 0
	commentCalls := 0
	anyCreated, anyFailed := applyReportMatches(
		context.Background(),
		&out,
		matches,
		map[string]bool{"bug": true},
		false,
		func(context.Context, report.Proposal) (string, error) {
			createCalls++
			return "", nil
		},
		func(context.Context, int, string) (string, error) {
			commentCalls++
			return "", nil
		},
	)

	if anyCreated {
		t.Fatal("dry run should not report created work")
	}
	if anyFailed {
		t.Fatal("dry run should not report failures")
	}
	if createCalls != 0 || commentCalls != 0 {
		t.Fatalf("dry run should skip writes, got create=%d comment=%d", createCalls, commentCalls)
	}

	got := out.String()
	for _, want := range []string{
		`duplicate of #42 "Existing bug"`,
		"dry run only; pass --yes to post the comment",
		"(dropping labels not on repo: [needs-triage])",
		"labels: [bug]",
		"would create a new issue (pass --yes to execute)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

func TestApplyReportMatches_YesExecutesWrites(t *testing.T) {
	t.Parallel()

	matches := []report.Match{
		{
			Proposal: report.Proposal{
				Title: "Existing bug",
				Body:  "comment body",
			},
			BestExisting: &report.SearchResult{
				Number: 42,
				Title:  "Existing bug",
			},
			Score:       0.91,
			IsDuplicate: true,
		},
		{
			Proposal: report.Proposal{
				Title:  "New bug",
				Body:   "new issue body",
				Labels: []string{"bug"},
			},
		},
	}

	var out bytes.Buffer
	createCalls := 0
	commentCalls := 0
	anyCreated, anyFailed := applyReportMatches(
		context.Background(),
		&out,
		matches,
		map[string]bool{"bug": true},
		true,
		func(_ context.Context, p report.Proposal) (string, error) {
			createCalls++
			if p.Title != "New bug" {
				t.Fatalf("unexpected issue title %q", p.Title)
			}
			return "https://github.com/CoreyRDean/intent/issues/99", nil
		},
		func(_ context.Context, number int, body string) (string, error) {
			commentCalls++
			if number != 42 {
				t.Fatalf("unexpected comment target %d", number)
			}
			if !strings.Contains(body, "comment body") {
				t.Fatalf("unexpected comment body %q", body)
			}
			return "https://github.com/CoreyRDean/intent/issues/42#issuecomment-1", nil
		},
	)

	if !anyCreated {
		t.Fatal("write mode should report created work")
	}
	if anyFailed {
		t.Fatal("write mode should not report failures")
	}
	if createCalls != 1 || commentCalls != 1 {
		t.Fatalf("write mode should execute both actions, got create=%d comment=%d", createCalls, commentCalls)
	}

	got := out.String()
	for _, want := range []string{
		"✓ commented: https://github.com/CoreyRDean/intent/issues/42#issuecomment-1",
		"✓ created: https://github.com/CoreyRDean/intent/issues/99",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}
