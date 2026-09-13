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
	if cfg.Linked {
		t.Fatal("fresh config must not be linked")
	}
	if cfg.EffectiveServerURL() != DefaultServerURL {
		t.Errorf("EffectiveServerURL = %q, want the default", cfg.EffectiveServerURL())
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	t.Setenv("GATE4AI_SYNC_CONFIG", filepath.Join(t.TempDir(), "nested", "config.json"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Folders = append(cfg.Folders, Folder{ID: "f1", Path: "/tmp/docs", Username: "u", Secret: "s", Slug: "documents"})
	cfg.Linked, cfg.VaultSlug = true, "home-pc"

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
	if !reloaded.Linked {
		t.Error("expected reloaded config to be linked")
	}
	if !reloaded.Folders[0].Registered() {
		t.Error("expected the reloaded folder to be registered")
	}
}

func TestSyncHostEnvOverridesBothURLsAndTakesPriorityOverSavedOnes(t *testing.T) {
	t.Setenv("GATE4AI_SYNC_HOST", "test.gate4.ai")
	cfg := &Config{ServerURL: "https://dav.gate4.ai", CabinetURL: "https://gate4.ai"}

	if got := cfg.EffectiveServerURL(); got != "https://dav.test.gate4.ai" {
		t.Errorf("EffectiveServerURL = %q, want the env override", got)
	}
	if got := cfg.EffectiveCabinetURL(); got != "https://test.gate4.ai" {
		t.Errorf("EffectiveCabinetURL = %q, want the env override", got)
	}
}

func TestManifestPathIsOnePerFolderUnderTheConfiguredDir(t *testing.T) {
	t.Setenv("GATE4AI_SYNC_DIR", t.TempDir())

	a, err := ManifestPath("folder-a")
	if err != nil {
		t.Fatalf("ManifestPath: %v", err)
	}
	b, err := ManifestPath("folder-b")
	if err != nil {
		t.Fatalf("ManifestPath: %v", err)
	}
	if a == b {
		t.Errorf("two different folders got the same manifest path: %q", a)
	}
	if filepath.Base(a) != "folder-a.json" {
		t.Errorf("ManifestPath(folder-a) = %q, want it to end in folder-a.json", a)
	}
}

func TestFolderLookupSetAndRemove(t *testing.T) {
	t.Setenv("GATE4AI_SYNC_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Folders = []Folder{{ID: "a", Path: "/a"}, {ID: "b", Path: "/b"}}

	if _, ok := cfg.Folder("a"); !ok {
		t.Fatal("Folder(a) not found")
	}
	cfg.SetFolder(Folder{ID: "a", Path: "/a", Username: "u", Secret: "s"})
	a, _ := cfg.Folder("a")
	if !a.Registered() {
		t.Error("SetFolder did not update the folder")
	}

	cfg.RemoveFolder("a")
	if _, ok := cfg.Folder("a"); ok {
		t.Error("RemoveFolder did not remove it")
	}
	if len(cfg.Folders) != 1 || cfg.Folders[0].ID != "b" {
		t.Errorf("Folders = %+v, want only b left", cfg.Folders)
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
