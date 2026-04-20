// Package state resolves the OS-appropriate state and cache directories.
// See docs/SPEC.md §4.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Dirs holds resolved on-disk locations.
type Dirs struct {
	State string // config, audit log, skills, daemon socket
	Cache string // models, runtime binaries, context cache
}

// Resolve returns the platform-appropriate Dirs, creating them if missing.
func Resolve() (Dirs, error) {
	stateRoot, err := stateRoot()
	if err != nil {
		return Dirs{}, err
	}
	cacheRoot, err := cacheRoot()
	if err != nil {
		return Dirs{}, err
	}
	d := Dirs{
		State: filepath.Join(stateRoot, "intent"),
		Cache: filepath.Join(cacheRoot, "intent"),
	}
	for _, p := range []string{
		d.State,
		d.Cache,
		filepath.Join(d.State, "skills"),
		filepath.Join(d.State, "history"),
		filepath.Join(d.Cache, "runtime"),
		filepath.Join(d.Cache, "models"),
		filepath.Join(d.Cache, "context"),
	} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return Dirs{}, fmt.Errorf("mkdir %s: %w", p, err)
		}
	}
	return d, nil
}

func stateRoot() (string, error) {
	if x := os.Getenv("INTENT_STATE_DIR"); x != "" {
		return x, nil
	}
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	case "linux":
		if x := os.Getenv("XDG_STATE_HOME"); x != "" {
			return x, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state"), nil
	case "windows":
		if x := os.Getenv("LOCALAPPDATA"); x != "" {
			return x, nil
		}
		return "", fmt.Errorf("LOCALAPPDATA not set on windows")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state"), nil
	}
}

func cacheRoot() (string, error) {
	if x := os.Getenv("INTENT_CACHE_DIR"); x != "" {
		return x, nil
	}
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Caches"), nil
	case "linux":
		if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
			return x, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".cache"), nil
	case "windows":
		if x := os.Getenv("LOCALAPPDATA"); x != "" {
			return filepath.Join(x, "Cache"), nil
		}
		return "", fmt.Errorf("LOCALAPPDATA not set on windows")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".cache"), nil
	}
}

// SocketPath returns the daemon's unix socket path. Linux/macOS only.
func (d Dirs) SocketPath() string {
	return filepath.Join(d.State, "daemon.sock")
}

// ConfigPath returns the path to config.toml.
func (d Dirs) ConfigPath() string {
	return filepath.Join(d.State, "config.toml")
}

// AuditPath returns the path to audit.jsonl.
func (d Dirs) AuditPath() string {
	return filepath.Join(d.State, "audit.jsonl")
}

// SkillsCachePath returns the path to the skill cache database.
func (d Dirs) SkillsCachePath() string {
	return filepath.Join(d.Cache, "skills_cache.json")
}
