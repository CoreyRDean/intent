package cli

import (
	"context"
	"fmt"
	"os"

	intentruntime "github.com/CoreyRDean/intent/internal/runtime"
	"github.com/CoreyRDean/intent/internal/state"
)

func cmdModel(ctx context.Context, args []string) int {
	if len(args) == 0 {
		errf("usage: i model (list | pull | use <name> | rm <name>)")
		return 1
	}
	dirs, err := state.Resolve()
	if err != nil {
		errf("model: %v", err)
		return 3
	}
	rt := intentruntime.New(dirs.Cache)
	switch args[0] {
	case "list":
		fmt.Printf("default: %s\n", intentruntime.DefaultModel.Name)
		if rt.HaveModel(intentruntime.DefaultModel.File) {
			fmt.Println("  installed: yes")
		} else {
			fmt.Println("  installed: no")
		}
		fmt.Printf("runtime:  llamafile-%s ", intentruntime.LlamafileVersion)
		if rt.HaveLlamafile() {
			fmt.Println("(installed)")
		} else {
			fmt.Println("(not installed)")
		}
		return 0
	case "pull":
		fmt.Println("downloading runtime...")
		if err := rt.EnsureLlamafile(ctx, progressCB("llamafile")); err != nil {
			errf("\nllamafile: %v", err)
			return 3
		}
		fmt.Println("\ndownloading model (~4.7 GB)...")
		if err := rt.EnsureModel(ctx, intentruntime.DefaultModel, progressCB("model")); err != nil {
			errf("\nmodel: %v", err)
			return 3
		}
		fmt.Println("\ndone.")
		return 0
	case "use":
		errf("model use: not yet implemented in v1; only the default model is supported")
		return 1
	case "rm":
		errf("model rm: not yet implemented in v1")
		return 1
	default:
		errf("unknown subcommand: %q", args[0])
		return 1
	}
}

func progressCB(label string) intentruntime.Progress {
	return func(downloaded, total int64) {
		if total <= 0 {
			fmt.Fprintf(os.Stderr, "\r  %s: %d MB", label, downloaded/(1024*1024))
			return
		}
		pct := float64(downloaded) / float64(total) * 100
		fmt.Fprintf(os.Stderr, "\r  %s: %.1f%% (%d / %d MB)", label, pct,
			downloaded/(1024*1024), total/(1024*1024))
	}
}
