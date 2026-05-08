package cli

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/daemon"
	intentruntime "github.com/CoreyRDean/intent/internal/runtime"
	"github.com/CoreyRDean/intent/internal/state"
	"github.com/CoreyRDean/intent/internal/version"
)

type daemonStatusCaller interface {
	Call(req daemon.Request) (*daemon.Response, error)
}

var newDaemonStatusClient = func(socket string) daemonStatusCaller {
	return daemon.NewClient(socket)
}

var daemonServiceInstalled = daemon.IsInstalled

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

	if err == nil {
		cfg, _ := config.Load(dirs.ConfigPath())
		if cfg != nil {
			check("config", fmt.Sprintf("backend=%s model=%s", cfg.Backend, cfg.Model), true)
		}

		rt := intentruntime.New(dirs.Cache)
		check("llamafile runtime",
			fmt.Sprintf("expected at %s", rt.LlamafilePath()),
			rt.HaveLlamafile())

		modelFile, modelStatus := resolveModelCheck(cfg)
		check("model", fmt.Sprintf("%s — %s", modelStatus, rt.ModelPath(modelFile)), rt.HaveModel(modelFile))

		daemonStatus, daemonOK := doctorDaemonStatus(dirs)
		check("daemon", daemonStatus, daemonOK)
	}

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

// resolveModelCheck returns the GGUF filename and a human-readable label
// describing which model doctor is checking. When cfg is nil or has no model
// set, it falls back to the default and says so explicitly.
func resolveModelCheck(cfg *config.Config) (file, label string) {
	if cfg != nil && cfg.Model != "" {
		file = intentruntime.ModelFileForName(cfg.Model)
		return file, fmt.Sprintf("checking: %s", file)
	}
	file = intentruntime.DefaultModel.File
	return file, fmt.Sprintf("no model configured, checking default: %s", file)
}

func okStr(err error) string {
	if err == nil {
		return "found"
	}
	return "missing"
}

func doctorDaemonStatus(dirs state.Dirs) (string, bool) {
	installed := daemonServiceInstalled(daemonLabel)
	resp, err := newDaemonStatusClient(dirs.SocketPath()).Call(daemon.Request{Op: daemon.OpStatus})
	if err != nil {
		if installed {
			return "installed but not responding", false
		}
		return "not running (optional)", true
	}
	if !resp.OK {
		return "unhealthy: " + resp.Error, false
	}

	serviceState := "no"
	if installed {
		serviceState = "yes"
	}
	if endpoint, _ := resp.Data["llamafile_endpoint"].(string); endpoint != "" {
		return fmt.Sprintf("running (service installed: %s, endpoint: %s)", serviceState, endpoint), true
	}
	return fmt.Sprintf("running (service installed: %s)", serviceState), true
}
