package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/CoreyRDean/intent/internal/audit"
	"github.com/CoreyRDean/intent/internal/state"
)

// cmdFix re-runs the last failing intent with the prior stderr context
// folded into the prompt. v1 prints a synthesized re-run command for the
// user to invoke manually; the streaming "fix loop" is Phase 6 polish.
func cmdFix(_ context.Context, _ []string) int {
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
	exit := -1
	if last.ExitCode != nil {
		exit = *last.ExitCode
	}
	fmt.Println("last failed turn:")
	fmt.Println("  prompt: ", last.Prompt)
	fmt.Println("  exit:   ", exit)
	if last.ExecutedCommand != "" {
		fmt.Println("  command:", last.ExecutedCommand)
	}
	fmt.Println()
	fmt.Println("rerun with:")
	fmt.Printf("  i %q  # add: \"the previous run failed with exit %d, here is what was attempted: %s\"\n",
		last.Prompt, exit, last.ExecutedCommand)
	return 0
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
