package autostart

// XDG autostart: a .desktop file in ~/.config/autostart/, per the
// Freedesktop Desktop Application Autostart Specification. Every major
// Linux desktop (GNOME, KDE, XFCE, ...) reads this directory on login —
// no per-desktop-environment code needed.

import (
	"fmt"
	"os"
	"path/filepath"
)

const desktopFileName = "gate4ai-sync.desktop"

func desktopFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "autostart", desktopFileName), nil
}

func enable() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable path: %w", err)
	}
	p, err := desktopFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=gate4.ai sync
Exec=%s
X-GNOME-Autostart-enabled=true
NoDisplay=true
`, exe)
	return os.WriteFile(p, []byte(content), 0o644)
}

func disable() error {
	p, err := desktopFilePath()
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
	p, err := desktopFilePath()
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
