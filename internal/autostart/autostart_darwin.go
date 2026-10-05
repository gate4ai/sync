package autostart

// A per-user LaunchAgent: a plist in ~/Library/LaunchAgents/, which launchd
// reads at every login. No CGO and no launchctl either — the file on disk is
// the whole registration, the same way a person would set this up by hand.
//
// launchctl is deliberately not run. "launchctl load" on a plist with
// RunAtLoad starts the job right away: called from the app's own startup,
// that launched a second copy next to the one already running. And
// "launchctl unload" stops the job's process, which after a login is this
// very app. Neither change needs to take effect before the next login: the
// app is already running, and it quits when asked.

import (
	"fmt"
	"os"
	"path/filepath"
)

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
	return os.WriteFile(p, []byte(content), 0o644)
}

func disable() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
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
