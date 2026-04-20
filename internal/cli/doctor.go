package cli

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/CoreyRDean/intent/internal/config"
	intentruntime "github.com/CoreyRDean/intent/internal/runtime"
	"github.com/CoreyRDean/intent/internal/state"
	"github.com/CoreyRDean/intent/internal/version"
)

func cmdDoctor(_ context.Context, _ []string) int {
	ok := true
	check := func(name, status string, good bool) {
		mark := "✓"
		if !good {
			mark = "✗"
			ok = false
		}
		fmt.Printf("  %s %-22s %s\n", mark, name, status)
	}

	fmt.Println("intent doctor")
	fmt.Println()
	fmt.Println(version.Long())
	fmt.Println()

	dirs, err := state.Resolve()
	if err != nil {
		check("state directory", err.Error(), false)
	} else {
		check("state directory", dirs.State, true)
		check("cache directory", dirs.Cache, true)
	}

	cfg, _ := config.Load(dirs.ConfigPath())
	if cfg != nil {
		check("config", fmt.Sprintf("backend=%s model=%s", cfg.Backend, cfg.Model), true)
	}

	rt := intentruntime.New(dirs.Cache)
	check("llamafile runtime",
		fmt.Sprintf("expected at %s", rt.LlamafilePath()),
		rt.HaveLlamafile())
	check("default model",
		fmt.Sprintf("expected at %s", rt.ModelPath(intentruntime.DefaultModel.File)),
		rt.HaveModel(intentruntime.DefaultModel.File))

	// Sandbox tooling.
	switch runtime.GOOS {
	case "linux":
		_, err := exec.LookPath("bwrap")
		check("sandbox: bwrap", okStr(err), err == nil)
	case "darwin":
		_, err := exec.LookPath("sandbox-exec")
		check("sandbox: sandbox-exec", okStr(err), err == nil)
	}

	// Useful binaries.
	for _, n := range []string{"git", "gh", "curl", "jq"} {
		_, err := exec.LookPath(n)
		check(fmt.Sprintf("binary: %s", n), okStr(err), err == nil)
	}

	if ok {
		fmt.Println("\nAll good.")
		return 0
	}
	fmt.Println("\nSome checks failed. Run `i init` and `i model pull` to set up missing pieces.")
	return 1
}

func okStr(err error) string {
	if err == nil {
		return "found"
	}
	return "missing"
}
