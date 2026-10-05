package autostart

// A per-user LaunchAgent: a plist in ~/Library/LaunchAgents/, loaded with
// launchctl. No CGO — launchd is driven entirely through that one
// command-line tool, the same way a person would set this up by hand.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// launchctlTimeout bounds the one external command this package runs. It is
// called while the settings page waits for a reply, so a launchctl that never
// returns would hang the page rather than just this setting.
const launchctlTimeout = 10 * time.Second

// launchctl runs one best-effort launchctl command. Failures are deliberately
// ignored: the plist on disk is what makes autostart work at the next login,
// and loading it now is only so the change takes effect without one.
func launchctl(args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), launchctlTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "launchctl", args...).Run()
}

const label = "ai.gate4.sync"

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func enable() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable path: %w", err)
	}
	p, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`, label, exe)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return err
	}
	launchctl("load", p)
	return nil
}

func disable() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	launchctl("unload", p)
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func isEnabled() (bool, error) {
	p, err := plistPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
