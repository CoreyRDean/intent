package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/CoreyRDean/intent/internal/config"
	"github.com/CoreyRDean/intent/internal/state"
)

func cmdConfig(_ context.Context, args []string) int {
	if len(args) == 0 {
		errf("usage: i config (get <key> | set <key> <value> | edit | path)")
		return 1
	}
	dirs, err := state.Resolve()
	if err != nil {
		errf("config: %v", err)
		return 3
	}
	switch args[0] {
	case "path":
		fmt.Println(dirs.ConfigPath())
		return 0
	case "edit":
		ed := os.Getenv("EDITOR")
		if ed == "" {
			ed = "vi"
		}
		cmd := exec.Command(ed, dirs.ConfigPath())
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			errf("editor: %v", err)
			return 1
		}
		return 0
	case "get":
		if len(args) < 2 {
			errf("usage: i config get <key>")
			return 1
		}
		cfg, err := config.Load(dirs.ConfigPath())
		if err != nil {
			errf("config: %v", err)
			return 3
		}
		v := cfg.Raw[args[1]]
		if v == "" {
			// fall back to known struct fields
			v = lookupKnown(cfg, args[1])
		}
		fmt.Println(v)
		return 0
	case "set":
		if len(args) < 3 {
			errf("usage: i config set <key> <value>")
			return 1
		}
		cfg, err := config.Load(dirs.ConfigPath())
		if err != nil {
			errf("config: %v", err)
			return 3
		}
		setKnown(cfg, args[1], args[2])
		cfg.Raw[args[1]] = args[2]
		if err := config.Write(dirs.ConfigPath(), cfg); err != nil {
			errf("config: %v", err)
			return 3
		}
		return 0
	default:
		errf("unknown subcommand: %q", args[0])
		return 1
	}
}

func lookupKnown(c *config.Config, key string) string {
	switch key {
	case "backend":
		return c.Backend
	case "model":
		return c.Model
	case "auto_run":
		return fmt.Sprintf("%t", c.AutoRun)
	case "update_channel":
		return c.UpdateChannel
	case "auto_update":
		return fmt.Sprintf("%t", c.AutoUpdate)
	}
	return ""
}

func setKnown(c *config.Config, key, value string) {
	switch key {
	case "backend":
		c.Backend = value
	case "model":
		c.Model = value
	case "auto_run":
		c.AutoRun = value == "true" || value == "yes"
	case "update_channel":
		c.UpdateChannel = value
	case "auto_update":
		c.AutoUpdate = value == "true" || value == "yes"
	}
}
