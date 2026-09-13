package autostart

// A per-user LaunchAgent: a plist in ~/Library/LaunchAgents/, loaded with
// launchctl. No CGO — launchd is driven entirely through that one
// command-line tool, the same way a person would set this up by hand.

import (
	"fmt"
	"os"
	"os/exec"
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
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return err
	}
	// Best-effort: a plist written before the next login works anyway, and
	// a failure here (e.g. launchd already has a stale copy loaded) should
	// not stop the file from being in place for next time.
	_ = exec.Command("launchctl", "load", p).Run()
	return nil
}

func disable() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	_ = exec.Command("launchctl", "unload", p).Run()
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
