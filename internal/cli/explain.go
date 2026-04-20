package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/CoreyRDean/intent/internal/cache"
	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/engine"
	"github.com/CoreyRDean/intent/internal/state"
	"github.com/CoreyRDean/intent/internal/tui"
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
	if !ensureBackendReady(ctx, dirs, cfg) {
		return 3
	}
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

	// Progress feedback. Local-model inference can take several seconds;
	// without a spinner the CLI looks frozen. Renders to stderr only
	// and is a no-op when stderr isn't a TTY, so piped/scripted use
	// stays clean.
	style := tui.DefaultStyle()
	sp := tui.NewSpinner(style)
	if sp != nil && tui.IsTTY(os.Stderr) {
		sp.Start("Invoking...")
		defer sp.Stop()
	}

	res, err := eng.Run(tctx, prompt, engine.Options{
		Backend:      be,
		MaxToolSteps: 0,
		OnPhase: func(p string) {
			if sp != nil {
				sp.SetLabel(p)
			}
		},
	})
	// Stop the spinner before printing output so its trailing \r\x1b[K
	// can't overwrite the first line of the explanation.
	if sp != nil {
		sp.Stop()
	}
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
