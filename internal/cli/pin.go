package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/CoreyRDean/intent/internal/audit"
	"github.com/CoreyRDean/intent/internal/cache"
	xexec "github.com/CoreyRDean/intent/internal/exec"
	"github.com/CoreyRDean/intent/internal/model"
	"github.com/CoreyRDean/intent/internal/state"
)

// cmdPin promotes the most recent confirmed/autorun entry to a named skill.
func cmdPin(_ context.Context, args []string) int {
	if len(args) < 1 {
		errf("usage: i pin <name>")
		return 1
	}
	name := args[0]
	dirs, err := state.Resolve()
	if err != nil {
		errf("pin: %v", err)
		return 3
	}
	entry, err := lastSuccessful(dirs.AuditPath())
	if err != nil {
		errf("pin: %v", err)
		return 1
	}
	store, err := cache.Open(dirs.SkillsCachePath())
	if err != nil {
		errf("pin: %v", err)
		return 3
	}
	all := store.All()
	for _, e := range all {
		if e.Prompt == entry.Prompt {
			if err := store.Pin(e.Key, name); err != nil {
				errf("pin: %v", err)
				return 1
			}
			fmt.Printf("pinned %q → %s\n", name, e.Prompt)
			return 0
		}
	}
	errf("pin: no cached entry found for the last accepted prompt; run it once with caching enabled first")
	return 1
}

// cmdRun runs a pinned skill by name (deterministically, no model call).
func cmdRun(ctx context.Context, args []string) int {
	if len(args) < 1 {
		errf("usage: i run <name>")
		return 1
	}
	name := args[0]
	dirs, err := state.Resolve()
	if err != nil {
		errf("run: %v", err)
		return 3
	}
	store, err := cache.Open(dirs.SkillsCachePath())
	if err != nil {
		errf("run: %v", err)
		return 3
	}
	for _, e := range store.All() {
		if e.PinnedName == name && e.Response != nil {
			req := xexec.Request{
				Stdin:  os.Stdin,
				Stdout: os.Stdout,
				Stderr: os.Stderr,
			}
			switch e.Response.Approach {
			case model.ApproachCommand:
				req.Shell = e.Response.Command
			case model.ApproachScript:
				req.Script = e.Response.Script.Body
				req.Interpreter = e.Response.Script.Interpreter
			default:
				errf("run: pinned skill %q is not executable (approach=%s)", name, e.Response.Approach)
				return 1
			}
			r, _ := xexec.Run(ctx, req)
			return r.ExitCode
		}
	}
	errf("run: no pinned skill named %q (see `i pin`)", name)
	return 1
}

func lastSuccessful(path string) (*audit.Entry, error) {
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
		if !strings.EqualFold(e.UserDecision, "confirmed") && !strings.EqualFold(e.UserDecision, "autorun") {
			continue
		}
		if e.ExitCode != nil && *e.ExitCode != 0 {
			continue
		}
		c := e
		last = &c
	}
	if last == nil {
		return nil, fmt.Errorf("no successful entry found")
	}
	return last, nil
}
