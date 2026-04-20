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

const historyUsage = "usage: i history (list | show <id> | replay <id> [natural-language flags] | clear)"

func cmdHistory(ctx context.Context, args []string) int {
	dirs, err := state.Resolve()
	if err != nil {
		errf("history: %v", err)
		return 3
	}
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "--help", "-h", "help":
		fmt.Println(historyUsage)
		return 0
	case "list":
		return historyList(dirs.AuditPath())
	case "show":
		if len(args) < 2 {
			errf("usage: i history show <id>")
			return 1
		}
		return historyShow(dirs.AuditPath(), args[1])
	case "replay":
		if len(args) < 2 {
			errf("usage: i history replay <id> [natural-language flags]")
			return 1
		}
		return historyReplay(ctx, dirs.AuditPath(), args[1], args[2:])
	case "clear":
		return historyClear(dirs.AuditPath())
	default:
		errf("unknown subcommand: %q", sub)
		return 1
	}
}

func historyList(path string) int {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("(no history yet)")
			return 0
		}
		errf("history: %v", err)
		return 3
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*64), 1024*1024)
	for sc.Scan() {
		var e audit.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		exitStr := ""
		if e.ExitCode != nil {
			exitStr = fmt.Sprintf(" exit=%d", *e.ExitCode)
		}
		fmt.Printf("%s  %s  %s%s  %q\n",
			e.TS, e.ID[:8], e.UserDecision, exitStr, e.Prompt)
	}
	return 0
}

func historyShow(path, id string) int {
	e, err := findHistoryEntry(path, id)
	if err != nil {
		errf("history: %v", err)
		return 1
	}
	pretty, _ := json.MarshalIndent(e, "", "  ")
	fmt.Println(string(pretty))
	return 0
}

func historyReplay(ctx context.Context, path, id string, args []string) int {
	e, err := findHistoryEntry(path, id)
	if err != nil {
		errf("history: %v", err)
		return 1
	}
	if e.Prompt == "" {
		errf("history: selected entry has no prompt to replay")
		return 1
	}
	return cmdIntent(ctx, append(args, e.Prompt))
}

func historyClear(path string) int {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		errf("history: %v", err)
		return 3
	}
	return 0
}

func findHistoryEntry(path, id string) (*audit.Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*64), 1024*1024)
	for sc.Scan() {
		var e audit.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if e.ID == id || (len(id) >= 4 && len(e.ID) >= len(id) && e.ID[:len(id)] == id) {
			c := e
			return &c, nil
		}
	}
	return nil, fmt.Errorf("id not found: %q", id)
}
