package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// InstallParams are everything Install needs to write a system service file.
type InstallParams struct {
	Binary string // absolute path to the intent binary
	Label  string // service label, e.g. "com.coreyrdean.intent"
	LogDir string // directory the service writes stdout/stderr to
	Socket string // daemon control socket path (informational)
	Cache  string // cache root (so the service knows where llamafile lives)
	State  string // state root
}

// InstallResult describes what was written and how to control it.
type InstallResult struct {
	UnitPath string   // path to the launchd plist or systemd unit
	StartCmd []string // command to start the unit
	StopCmd  []string // command to stop the unit
	LogPath  string   // path to the stdout log
	Notes    string   // human-readable post-install hint
}

// Install writes the platform-appropriate service file and starts it.
// On macOS, returns the LaunchAgent plist path.
// On Linux, returns the user systemd unit path.
// Other platforms return an error.
func Install(p InstallParams) (*InstallResult, error) {
	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(p)
	case "linux":
		return installSystemd(p)
	default:
		return nil, fmt.Errorf("daemon install not supported on %s yet", runtime.GOOS)
	}
}

// Uninstall removes the platform-appropriate service file (and stops it).
func Uninstall(label string) error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchd(label)
	case "linux":
		return uninstallSystemd(label)
	default:
		return fmt.Errorf("daemon uninstall not supported on %s yet", runtime.GOOS)
	}
}

// IsInstalled reports whether the platform-appropriate service file exists.
func IsInstalled(label string) bool {
	switch runtime.GOOS {
	case "darwin":
		path, _ := launchdPlistPath(label)
		_, err := os.Stat(path)
		return err == nil
	case "linux":
		path, _ := systemdUnitPath(label)
		_, err := os.Stat(path)
		return err == nil
	}
	return false
}

// --- macOS / launchd ---

func launchdPlistPath(label string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func installLaunchd(p InstallParams) (*InstallResult, error) {
	plistPath, err := launchdPlistPath(p.Label)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.LogDir, 0o700); err != nil {
		return nil, err
	}
	logOut := filepath.Join(p.LogDir, "intentd.out.log")
	logErr := filepath.Join(p.LogDir, "intentd.err.log")
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>            <string>%s</string>
  <key>ProgramArguments</key> <array>
    <string>%s</string>
    <string>daemon</string>
    <string>start</string>
    <string>--foreground</string>
  </array>
  <key>RunAtLoad</key>        <true/>
  <key>KeepAlive</key>        <true/>
  <key>ProcessType</key>      <string>Background</string>
  <key>StandardOutPath</key>  <string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key>           <string>%s</string>
    <key>PATH</key>           <string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
</dict>
</plist>
`, p.Label, p.Binary, logOut, logErr, mustHome())
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return nil, fmt.Errorf("write plist: %w", err)
	}
	// Best-effort start. launchctl load is the right verb on macOS LaunchAgents
	// even though it's been deprecated in favor of bootstrap. bootstrap requires
	// a target like `gui/$UID` and is awkward; load still works.
	_, _ = exec.Command("launchctl", "unload", plistPath).CombinedOutput()
	if out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput(); err != nil {
		return &InstallResult{
			UnitPath: plistPath,
			StartCmd: []string{"launchctl", "load", plistPath},
			StopCmd:  []string{"launchctl", "unload", plistPath},
			LogPath:  logOut,
			Notes: fmt.Sprintf("plist installed but launchctl load failed: %s\n"+
				"start manually with: launchctl load %s", string(out), plistPath),
		}, nil
	}
	return &InstallResult{
		UnitPath: plistPath,
		StartCmd: []string{"launchctl", "load", plistPath},
		StopCmd:  []string{"launchctl", "unload", plistPath},
		LogPath:  logOut,
		Notes:    "intentd is now running and will start at login.",
	}, nil
}

func uninstallLaunchd(label string) error {
	plistPath, err := launchdPlistPath(label)
	if err != nil {
		return err
	}
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return nil
	}
	_, _ = exec.Command("launchctl", "unload", plistPath).CombinedOutput()
	return os.Remove(plistPath)
}

// --- Linux / systemd user unit ---

func systemdUnitPath(label string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	// systemd allows arbitrary unit names; we use the label suffix as the
	// unit basename to avoid collisions with system units.
	return filepath.Join(home, ".config", "systemd", "user", label+".service"), nil
}

func installSystemd(p InstallParams) (*InstallResult, error) {
	unitPath, err := systemdUnitPath(p.Label)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return nil, err
	}
	unit := fmt.Sprintf(`[Unit]
Description=intent daemon (keeps a local LLM warm)
After=default.target

[Service]
Type=simple
ExecStart=%s daemon start --foreground
Restart=on-failure
RestartSec=2
Environment=PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
NoNewPrivileges=yes
PrivateTmp=yes

[Install]
WantedBy=default.target
`, p.Binary)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return nil, fmt.Errorf("write unit: %w", err)
	}
	_, _ = exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput()
	unitName := p.Label + ".service"
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", unitName).CombinedOutput(); err != nil {
		return &InstallResult{
			UnitPath: unitPath,
			StartCmd: []string{"systemctl", "--user", "start", unitName},
			StopCmd:  []string{"systemctl", "--user", "stop", unitName},
			LogPath:  "journalctl --user -u " + unitName,
			Notes: fmt.Sprintf("unit installed but `systemctl --user enable --now` failed: %s\n"+
				"start manually with: systemctl --user start %s", string(out), unitName),
		}, nil
	}
	return &InstallResult{
		UnitPath: unitPath,
		StartCmd: []string{"systemctl", "--user", "start", unitName},
		StopCmd:  []string{"systemctl", "--user", "stop", unitName},
		LogPath:  "journalctl --user -u " + unitName,
		Notes:    "intentd is enabled and running. Logs: journalctl --user -u " + unitName,
	}, nil
}

func uninstallSystemd(label string) error {
	unitPath, err := systemdUnitPath(label)
	if err != nil {
		return err
	}
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return nil
	}
	unitName := label + ".service"
	_, _ = exec.Command("systemctl", "--user", "disable", "--now", unitName).CombinedOutput()
	if err := os.Remove(unitPath); err != nil {
		return err
	}
	_, _ = exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput()
	return nil
}

func mustHome() string {
	h, _ := os.UserHomeDir()
	return h
}
