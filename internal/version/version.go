// Package version exposes build-time version metadata, populated via -ldflags.
package version

import "fmt"

// Set via -ldflags at build time:
//
//	-X github.com/CoreyRDean/intent/internal/version.Version=...
var (
	Version   = "dev"     // semver, or "dev" for unreleased
	Commit    = "none"    // short git SHA
	BuildDate = "unknown" // RFC3339
	Channel   = "dev"     // stable | nightly | dev
)

// Short returns just the version string.
func Short() string { return Version }

// Long returns version, commit, build date, channel.
func Long() string {
	return fmt.Sprintf("intent %s (%s) built %s [%s]", Version, Commit, BuildDate, Channel)
}
