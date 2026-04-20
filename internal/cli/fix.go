package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/CoreyRDean/intent/internal/audit"
	"github.com/CoreyRDean/intent/internal/state"
)

const fixUsage = "usage: i fix [natural-language flags]"

// cmdFix re-runs the last failing intent with the prior stderr context
// folded into the prompt.
func cmdFix(ctx context.Context, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "--help", "-h", "help":
			fmt.Println(fixUsage)
			return 0
		}
	}
	dirs, err := state.Resolve()
	if err != nil {
		errf("fix: %v", err)
		return 3
	}
	last, err := lastFailed(dirs.AuditPath())
	if err != nil {
		errf("fix: %v", err)
		return 1
	}
	return cmdIntent(ctx, append(args, buildFixPrompt(last)))
}

func lastFailed(path string) (*audit.Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*64), 1024*1024)
	var last *audit.Entry
	for sc.Scan() {
		var e audit.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if e.ExitCode != nil && *e.ExitCode != 0 {
			c := e
			last = &c
		}
	}
	if last == nil {
		return nil, fmt.Errorf("no failed turn in audit log")
	}
	return last, nil
}

func buildFixPrompt(last *audit.Entry) string {
	if last == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(last.Prompt))
	b.WriteString("\n\nThe previous attempt failed. Keep the same user goal, but adjust the approach so it succeeds instead of repeating the failure.")
	if last.ExecutedCommand != "" {
		b.WriteString("\nPreviously executed command:\n")
		b.WriteString(last.ExecutedCommand)
	}
	if last.ExitCode != nil {
		fmt.Fprintf(&b, "\nExit code: %d", *last.ExitCode)
	}
	if last.StderrExcerpt != "" {
		b.WriteString("\nStderr excerpt:\n")
		b.WriteString(strings.TrimSpace(last.StderrExcerpt))
	}
	return b.String()
}
