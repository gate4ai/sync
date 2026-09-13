package config

import (
	"path/filepath"
	"testing"
)

func TestLoadCreatesFreshConfigWithID(t *testing.T) {
	t.Setenv("GATE4AI_SYNC_CONFIG", filepath.Join(t.TempDir(), "config.json"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID == "" {
		t.Fatal("expected a generated ClientID")
	}
	if cfg.Paired() {
		t.Fatal("fresh config must not be paired")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	t.Setenv("GATE4AI_SYNC_CONFIG", filepath.Join(t.TempDir(), "nested", "config.json"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Folders = append(cfg.Folders, Folder{ID: "f1", Path: "/tmp/docs"})
	cfg.Username, cfg.Secret, cfg.VaultSlug = "u", "s", "home-pc"

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if reloaded.ClientID != cfg.ClientID {
		t.Errorf("ClientID changed across save/load: %q != %q", reloaded.ClientID, cfg.ClientID)
	}
	if len(reloaded.Folders) != 1 || reloaded.Folders[0].Path != "/tmp/docs" {
		t.Errorf("folders not persisted: %+v", reloaded.Folders)
	}
	if !reloaded.Paired() {
		t.Error("expected reloaded config to be paired")
	}
}

func TestClientIDStableAcrossLoads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("GATE4AI_SYNC_CONFIG", p)

	first, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := first.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	second, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if second.ClientID != first.ClientID {
		t.Errorf("ClientID not stable: %q != %q", second.ClientID, first.ClientID)
	}
}
