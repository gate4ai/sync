// Package config loads and saves the client's single flat JSON config file.
package config

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// newID returns a random UUIDv4 string. Written by hand instead of pulling
// in a dependency for something this small (see CLAUDE.md's "минимум
// зависимостей").
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failing means the OS RNG is broken
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Folder is one locally synced directory.
type Folder struct {
	// ID is a stable identifier minted once when the folder is first added.
	// It never changes, even if the folder is reordered, removed and
	// re-added, or the client is reinstalled — the server uses it to keep
	// the folder's name in the vault stable across those events.
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Config is the client's persisted state: identity, folder selection, and
// the WebDAV credential obtained through pairing. There are no profiles or
// overrides — one file, one shape.
type Config struct {
	// ClientID is generated once on first run and never changes; it is the
	// identifier the pairing flow (docs/sync-api.md in gate4ai/server) uses
	// to link this installation to a server-side vault.
	ClientID string   `json:"client_id"`
	Folders  []Folder `json:"folders"`

	ServerURL string `json:"server_url,omitempty"`
	Username  string `json:"username,omitempty"`
	Secret    string `json:"secret,omitempty"`
	VaultSlug string `json:"vault_slug,omitempty"`

	ProxyURL string `json:"proxy_url,omitempty"`
}

// Paired reports whether pairing has completed and the client holds a
// WebDAV credential.
func (c *Config) Paired() bool {
	return c.Username != "" && c.Secret != ""
}

// path returns the config file location, honoring $GATE4AI_SYNC_CONFIG for
// tests and creating the parent directory as needed.
func path() (string, error) {
	if p := os.Getenv("GATE4AI_SYNC_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gate4ai-sync", "config.json"), nil
}

// Load reads the config file, creating a fresh one (with a new ClientID) if
// none exists yet.
func Load() (*Config, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{ClientID: newID()}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.ClientID == "" {
		cfg.ClientID = newID()
	}
	return &cfg, nil
}

// Save writes the config file, creating its parent directory if needed.
func (c *Config) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}
