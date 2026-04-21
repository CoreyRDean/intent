package cli

import "testing"

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
