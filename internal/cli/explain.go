package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/cache"
	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/engine"
	"github.com/CoreyRDean/intent/internal/state"
)

// cmdExplain reverses the usual flow: given an arbitrary shell command,
// ask the model what it does. Useful as a learning tool.
func cmdExplain(ctx context.Context, args []string) int {
	if len(args) == 0 {
		errf("usage: i explain <command>")
		return 1
	}
	cmd := strings.Join(args, " ")
	dirs, err := state.Resolve()
	if err != nil {
		errf("explain: %v", err)
		return 3
	}
	cfg, _ := config.Load(dirs.ConfigPath())
	be, isFallback, err := buildBackend(cfg.Backend, cfg, "")
	if err != nil {
		errf("explain: %v", err)
		return 3
	}
	printMockFallbackBanner(isFallback)
	store, _ := cache.Open(dirs.SkillsCachePath())
	eng := engine.New(store)
	prompt := fmt.Sprintf("Explain in plain English what this shell command does. Do not run it. Set approach=inform and put your explanation in stdout_to_user.\n\nCommand:\n%s", cmd)
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := eng.Run(tctx, prompt, engine.Options{Backend: be, MaxToolSteps: 0})
	if err != nil {
		errf("explain: %v", err)
		return 3
	}
	if res.Response.StdoutToUser != "" {
		fmt.Print(res.Response.StdoutToUser)
		if !strings.HasSuffix(res.Response.StdoutToUser, "\n") {
			fmt.Println()
		}
		return 0
	}
	if res.Response.Description != "" {
		fmt.Println(res.Response.Description)
		return 0
	}
	errf("explain: model produced no explanation")
	return 1
}
