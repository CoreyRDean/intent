// Package installmeta records and reports how this `intent` binary was
// installed, so `i update now` can dispatch to the right updater.
//
// Two sources of truth, in priority order:
//
//  1. A marker file at <state>/install.json, written by install.sh,
//     by the Homebrew formula's post_install, or by `i update now`
//     after a successful self-update.
//  2. A best-effort heuristic on the binary's path (Homebrew puts it
//     under */Cellar/intent/*; Go's `go install` puts it under
//     ${GOPATH:-~/go}/bin; install.sh defaults to /usr/local/bin).
//
// The marker file always wins. The heuristic is for users who installed
// before the marker existed.
package installmeta

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Method is the discriminator for how the binary was installed.
type Method string

const (
	MethodUnknown Method = "unknown"
	MethodScript  Method = "script"  // install.sh
	MethodBrew    Method = "brew"    // Homebrew
	MethodGo      Method = "go"      // go install
	MethodManual  Method = "manual"  // user dropped a binary they built
	MethodPackage Method = "package" // distro package
)

// Marker is the on-disk record. JSON for forward compatibility.
type Marker struct {
	Method      Method    `json:"method"`
	Version     string    `json:"version,omitempty"`
	BinaryPath  string    `json:"binary_path,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
	Channel     string    `json:"channel,omitempty"` // stable | nightly
}

// MarkerPath returns the marker file path under stateDir.
func MarkerPath(stateDir string) string {
	return filepath.Join(stateDir, "install.json")
}

// Write persists the marker. Called by install.sh (via `i init record-install`),
// by the brew post_install, and by `i update now` after a successful update.
func Write(stateDir string, m Marker) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	if m.InstalledAt.IsZero() {
		m.InstalledAt = time.Now().UTC()
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(MarkerPath(stateDir), b, 0o600)
}

// Read returns the marker if it exists, or an empty Marker with the
// detected method otherwise. Never returns an error for "not present".
func Read(stateDir string) (Marker, error) {
	b, err := os.ReadFile(MarkerPath(stateDir))
	if err == nil {
		var m Marker
		if jerr := json.Unmarshal(b, &m); jerr != nil {
			return Marker{}, fmt.Errorf("read install marker: %w", jerr)
		}
		if m.Method == "" {
			m.Method = MethodUnknown
		}
		return m, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Marker{}, err
	}
	// No marker — guess.
	return detect(), nil
}

// detect uses the binary's path to guess the install method.
func detect() Marker {
	bin, err := os.Executable()
	if err != nil {
		return Marker{Method: MethodUnknown}
	}
	resolved, err := filepath.EvalSymlinks(bin)
	if err == nil {
		bin = resolved
	}
	m := Marker{BinaryPath: bin}
	switch {
	case strings.Contains(bin, "/Cellar/intent/"),
		strings.HasPrefix(bin, "/opt/homebrew/"),
		strings.HasPrefix(bin, "/usr/local/Homebrew/"):
		m.Method = MethodBrew
	case strings.Contains(bin, "/go/bin/intent"):
		m.Method = MethodGo
	case strings.HasPrefix(bin, "/usr/local/bin/intent"),
		strings.HasPrefix(bin, "/usr/bin/intent"):
		// install.sh's default destination, but also where distro
		// packages put things. Without a marker, we can't disambiguate;
		// call it script (the most likely case for a fresh install).
		m.Method = MethodScript
	default:
		m.Method = MethodManual
	}
	return m
}

// HumanName returns a UI-friendly name for the method.
func (m Method) HumanName() string {
	switch m {
	case MethodBrew:
		return "Homebrew"
	case MethodScript:
		return "install.sh"
	case MethodGo:
		return "go install"
	case MethodManual:
		return "manual / built from source"
	case MethodPackage:
		return "system package"
	default:
		return "unknown"
	}
}

// PlatformDefault returns the most likely method for users with no marker
// on this OS. Used only as a last-ditch guess in error messages.
func PlatformDefault() Method {
	switch runtime.GOOS {
	case "darwin", "linux":
		return MethodScript
	default:
		return MethodUnknown
	}
}
