// Package config loads and writes intent's TOML configuration.
// We use a tiny hand-rolled TOML reader/writer to avoid pulling in a dependency
// for what is, in v1, a flat key-value file. If config.toml grows nested
// sections we'll switch to a proper TOML library.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is the in-memory configuration.
type Config struct {
	Backend               string
	Model                 string
	AutoRun               bool
	Sandbox               bool
	MaxToolSteps          int
	Timeout               time.Duration
	UpdateChannel         string
	AutoUpdate            bool
	DaemonEnabled         bool
	DaemonIdleUnloadAfter time.Duration
	CacheEnabled          bool

	// Backend-specific blobs preserved verbatim.
	Raw map[string]string
}

// Defaults returns the project's chosen defaults.
func Defaults() *Config {
	return &Config{
		Backend: "llamafile-local",
		// Catalog short-id. See internal/models.DefaultID. Defaults to
		// the 3B model as the balanced "just works" option: strong
		// enough that `i report` doesn't routinely hit the fallback
		// parser, small enough to run on any laptop. Users can switch
		// with `i model use <id>`; legacy configs storing full GGUF
		// stems still resolve via Catalog.Get's backward-compat path.
		Model:                 "qwen2.5-coder-3b",
		AutoRun:               false,
		Sandbox:               false,
		MaxToolSteps:          12,
		Timeout:               60 * time.Second,
		UpdateChannel:         "stable",
		AutoUpdate:            false,
		DaemonEnabled:         true,
		DaemonIdleUnloadAfter: 30 * time.Minute,
		CacheEnabled:          true,
		Raw:                   map[string]string{},
	}
}

var (
	once    sync.Once
	loaded  *Config
	loadErr error
)

// Load returns the (cached) config from path. If the file is missing, defaults
// are returned and no error.
func Load(path string) (*Config, error) {
	once.Do(func() {
		loaded, loadErr = read(path)
	})
	return loaded, loadErr
}

func read(path string) (*Config, error) {
	c := Defaults()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.Trim(strings.TrimSpace(line[eq+1:]), `"`)
		c.Raw[k] = v
		switch k {
		case "backend":
			c.Backend = v
		case "model":
			c.Model = v
		case "auto_run":
			c.AutoRun = parseBool(v)
		case "sandbox":
			c.Sandbox = parseBool(v)
		case "max_tool_steps":
			if n, err := strconv.Atoi(v); err == nil {
				c.MaxToolSteps = n
			}
		case "timeout":
			if d, err := time.ParseDuration(v); err == nil {
				c.Timeout = d
			}
		case "update_channel":
			c.UpdateChannel = v
		case "auto_update":
			c.AutoUpdate = parseBool(v)
		case "daemon_enabled":
			c.DaemonEnabled = parseBool(v)
		case "daemon_idle_unload_after":
			if d, err := time.ParseDuration(v); err == nil {
				c.DaemonIdleUnloadAfter = d
			}
		case "cache_enabled":
			c.CacheEnabled = parseBool(v)
		}
	}
	return c, sc.Err()
}

func parseBool(v string) bool {
	switch strings.ToLower(v) {
	case "true", "1", "yes", "y", "on":
		return true
	}
	return false
}

// Write persists the given config to path, atomically.
func Write(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "# intent config — see docs/SPEC.md §8\n")
	fmt.Fprintf(w, "backend = %q\n", c.Backend)
	fmt.Fprintf(w, "model = %q\n", c.Model)
	fmt.Fprintf(w, "auto_run = %t\n", c.AutoRun)
	fmt.Fprintf(w, "sandbox = %t\n", c.Sandbox)
	fmt.Fprintf(w, "max_tool_steps = %d\n", c.MaxToolSteps)
	fmt.Fprintf(w, "timeout = %q\n", c.Timeout.String())
	fmt.Fprintf(w, "update_channel = %q\n", c.UpdateChannel)
	fmt.Fprintf(w, "auto_update = %t\n", c.AutoUpdate)
	fmt.Fprintf(w, "daemon_enabled = %t\n", c.DaemonEnabled)
	fmt.Fprintf(w, "daemon_idle_unload_after = %q\n", c.DaemonIdleUnloadAfter.String())
	fmt.Fprintf(w, "cache_enabled = %t\n", c.CacheEnabled)
	// Persist unknown raw keys that are not covered by the known struct fields.
	knownFields := map[string]bool{
		"backend": true, "model": true, "auto_run": true, "sandbox": true,
		"max_tool_steps": true, "timeout": true, "update_channel": true,
		"auto_update": true, "daemon_enabled": true,
		"daemon_idle_unload_after": true, "cache_enabled": true,
	}
	for k, v := range c.Raw {
		if !knownFields[k] {
			fmt.Fprintf(w, "%s = %q\n", k, v)
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
