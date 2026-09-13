package autostart_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gate4ai/sync/internal/autostart"
)

func TestEnableIsIdempotentAndDisableRemovesIt(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	for range 2 { // Enable twice must not fail or duplicate anything
		if err := autostart.Enable(); err != nil {
			t.Fatalf("Enable: %v", err)
		}
	}
	on, err := autostart.IsEnabled()
	if err != nil {
		t.Fatalf("IsEnabled: %v", err)
	}
	if !on {
		t.Fatal("IsEnabled = false after Enable")
	}

	p := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "autostart", "gate4ai-sync.desktop")
	content, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read desktop file: %v", err)
	}
	if !strings.Contains(string(content), "[Desktop Entry]") {
		t.Errorf("desktop file missing its header:\n%s", content)
	}

	if err := autostart.Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	on, err = autostart.IsEnabled()
	if err != nil {
		t.Fatalf("IsEnabled after Disable: %v", err)
	}
	if on {
		t.Error("IsEnabled = true after Disable")
	}
}

func TestDisableWithoutEnableIsNotAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := autostart.Disable(); err != nil {
		t.Fatalf("Disable on a fresh environment: %v", err)
	}
}
