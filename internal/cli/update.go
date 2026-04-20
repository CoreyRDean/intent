package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/state"
	"github.com/CoreyRDean/intent/internal/update"
	"github.com/CoreyRDean/intent/internal/version"
)

func cmdUpdate(ctx context.Context, args []string) int {
	sub := "check"
	if len(args) > 0 {
		sub = args[0]
	}
	dirs, _ := state.Resolve()
	cfg, _ := config.Load(dirs.ConfigPath())
	switch sub {
	case "check":
		return updateCheck(ctx, update.Channel(cfg.UpdateChannel))
	case "now":
		return updateNow(ctx, update.Channel(cfg.UpdateChannel))
	case "auto":
		cfg.AutoUpdate = true
		if err := config.Write(dirs.ConfigPath(), cfg); err != nil {
			errf("update: %v", err)
			return 3
		}
		fmt.Println("auto-update: on")
		return 0
	case "off":
		cfg.AutoUpdate = false
		cfg.UpdateChannel = string(update.ChannelOff)
		if err := config.Write(dirs.ConfigPath(), cfg); err != nil {
			errf("update: %v", err)
			return 3
		}
		fmt.Println("auto-update: off; channel: off")
		return 0
	default:
		errf("unknown subcommand: %q", sub)
		return 1
	}
}

func updateCheck(ctx context.Context, ch update.Channel) int {
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cur := strings.TrimPrefix(version.Short(), "v")
	r, err := update.Check(tctx, ch, cur)
	if err != nil {
		errf("update: %v", err)
		return 3
	}
	if r.Latest == nil {
		fmt.Printf("no releases found on channel %q\n", ch)
		return 0
	}
	if r.Available {
		fmt.Printf("update available: %s → %s\n  %s\n",
			version.Short(), r.Latest.TagName, r.Latest.HTMLURL)
		return 0
	}
	fmt.Printf("up to date (%s)\n", version.Short())
	return 0
}

func updateNow(ctx context.Context, ch update.Channel) int {
	errf("update now: in v1, install updates via `brew upgrade intent` or rerun the install script.")
	errf("self-replacing updates land in Phase 5; tracking https://github.com/CoreyRDean/intent/issues")
	return 1
}
