package autostart

// HKCU\Software\Microsoft\Windows\CurrentVersion\Run — the standard
// per-user autostart registry key. golang.org/x/sys/windows/registry is
// pure Go (syscalls, no CGO), so this needs nothing beyond what's already
// a transitive dependency of the tray library.

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const valueName = "gate4ai-sync"

func enable() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable path: %w", err)
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()
	return k.SetStringValue(valueName, exe)
}

func disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()
	err = k.DeleteValue(valueName)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}

func isEnabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		return false, fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()
	_, _, err = k.GetStringValue(valueName)
	if err == registry.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
