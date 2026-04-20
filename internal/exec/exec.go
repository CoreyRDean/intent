// Package exec runs the model-produced command or script and captures the
// result. It optionally wraps execution in a sandbox.
package exec

import (
	"context"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"runtime"
	"time"
)

// Mode controls execution flavor.
type Mode int

const (
	ModeNormal Mode = iota
	ModeDry
	ModeSandbox
	ModeSandboxRO
)

// Request describes one execution.
type Request struct {
	Shell       string // a shell command line (single line)
	Script      string // a script body (multi-line); takes precedence over Shell
	Interpreter string // for scripts; e.g. "bash"
	Mode        Mode
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Env         []string // appended to os.Environ()
}

// Result is the outcome.
type Result struct {
	ExitCode int
	Duration time.Duration
	Cmd      string // the actual command we invoked, for the audit log
	Sandbox  string // "" | "sandbox-exec" | "bwrap"
}

// Run executes the request.
func Run(ctx context.Context, req Request) (Result, error) {
	if req.Mode == ModeDry {
		return Result{ExitCode: 0}, nil
	}

	interp := req.Interpreter
	if interp == "" {
		interp = "bash"
		if _, err := osexec.LookPath(interp); err != nil {
			interp = "sh"
		}
	}

	args, sandboxName, err := buildCommand(interp, req)
	if err != nil {
		return Result{}, err
	}

	cmd := osexec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = req.Stdin
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	cmd.Env = append(os.Environ(), req.Env...)

	start := time.Now()
	err = cmd.Run()
	dur := time.Since(start)

	res := Result{
		Duration: dur,
		Cmd:      humanize(args),
		Sandbox:  sandboxName,
	}
	if err != nil {
		var ee *osexec.ExitError
		if asExit(err, &ee) {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		res.ExitCode = -1
		return res, err
	}
	return res, nil
}

func asExit(err error, into **osexec.ExitError) bool {
	if err == nil {
		return false
	}
	ee, ok := err.(*osexec.ExitError)
	if !ok {
		return false
	}
	*into = ee
	return true
}

func buildCommand(interp string, req Request) ([]string, string, error) {
	body := req.Script
	useC := body == ""
	var args []string
	if useC {
		args = []string{interp, "-c", req.Shell}
	} else {
		args = []string{interp, "-c", body}
	}

	switch req.Mode {
	case ModeSandbox, ModeSandboxRO:
		switch runtime.GOOS {
		case "linux":
			if _, err := osexec.LookPath("bwrap"); err != nil {
				return nil, "", fmt.Errorf("--sandbox requires bwrap; install bubblewrap")
			}
			cwd, _ := os.Getwd()
			bwrap := []string{"bwrap",
				"--ro-bind", "/", "/",
				"--proc", "/proc",
				"--dev", "/dev",
				"--tmpfs", "/tmp",
				"--die-with-parent",
				"--new-session",
			}
			if req.Mode == ModeSandboxRO {
				bwrap = append(bwrap, "--ro-bind", cwd, cwd)
			} else {
				bwrap = append(bwrap, "--bind", cwd, cwd)
			}
			return append(bwrap, args...), "bwrap", nil
		case "darwin":
			if _, err := osexec.LookPath("sandbox-exec"); err != nil {
				return nil, "", fmt.Errorf("--sandbox requires sandbox-exec")
			}
			profile := macSandboxProfile(req.Mode == ModeSandboxRO)
			return append([]string{"sandbox-exec", "-p", profile}, args...), "sandbox-exec", nil
		default:
			return nil, "", fmt.Errorf("--sandbox not supported on %s yet", runtime.GOOS)
		}
	}
	return args, "", nil
}

func macSandboxProfile(ro bool) string {
	// Minimal sandbox-exec SBPL profile. Allows reads everywhere, writes only
	// to cwd and /tmp (or denies writes entirely if ro).
	cwd, _ := os.Getwd()
	prof := `(version 1)
(deny default)
(allow process-fork)
(allow process-exec*)
(allow signal (target self))
(allow file-read*)
(allow sysctl-read)
(allow mach-lookup)
(allow ipc-posix-shm)
(allow network*)
`
	if !ro {
		prof += `(allow file-write* (subpath "` + cwd + `"))
(allow file-write* (subpath "/tmp"))
(allow file-write* (subpath "/private/tmp"))
(allow file-write* (subpath "/private/var/folders"))
`
	}
	return prof
}

func humanize(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
