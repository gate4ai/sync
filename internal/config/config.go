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

// NewID returns a random UUIDv4 string — used for both ClientID and each
// Folder's ID. Written by hand instead of pulling in a dependency for
// something this small (see CLAUDE.md's "минимум зависимостей").
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failing means the OS RNG is broken
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Folder is one locally synced directory. Each one gets its own mount and
// its own WebDAV credential on the server (docs/sync-api.md in
// gate4ai/server) — a credential always opens exactly one mount there, so
// there is one per folder rather than one shared across all of them.
type Folder struct {
	// ID is a stable identifier minted once when the folder is first added.
	// It never changes, even if the folder is reordered, removed and
	// re-added, or the client is reinstalled — the server uses it to keep
	// the folder's name in the vault stable across those events.
	ID   string `json:"id"`
	Path string `json:"path"`

	// Slug, Username and Secret are empty until this folder's mount is
	// registered (POST /api/sync/v1/mounts) — which cannot happen before
	// the client itself is linked to a vault. See Registered.
	Slug     string `json:"slug,omitempty"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

// Registered reports whether this folder has its own mount and credential
// yet.
func (f Folder) Registered() bool {
	return f.Username != "" && f.Secret != ""
}

// Config is the client's persisted state: identity, folder selection, and
// what pairing produced. There are no profiles or overrides — one file,
// one shape.
type Config struct {
	// ClientID is generated once on first run and never changes; it is the
	// identifier the pairing flow (docs/sync-api.md in gate4ai/server) uses
	// to link this installation to a server-side vault, and the bearer the
	// control API accepts afterward.
	ClientID string   `json:"client_id"`
	Folders  []Folder `json:"folders"`

	ServerURL string `json:"server_url,omitempty"`
	// CabinetURL is where /link lives — a different host from ServerURL
	// (the cabinet, not the WebDAV/control-API host).
	CabinetURL string `json:"cabinet_url,omitempty"`
	// Linked is set once pairing (the /link page) has attached ClientID to
	// a vault — see docs/sync-api.md's pairing_status. It says nothing
	// about any individual folder's own registration; see Folder.Registered
	// for that.
	Linked    bool   `json:"linked,omitempty"`
	VaultSlug string `json:"vault_slug,omitempty"`

	ProxyURL string `json:"proxy_url,omitempty"`
}

// DefaultHost is which gate4.ai deployment the client talks to when nothing
// says otherwise — production. $GATE4AI_SYNC_HOST overrides it for the
// whole process (e.g. "test.gate4.ai" for the staging stand): set once at
// launch, never written to config.json, so switching back is just unsetting
// the variable rather than editing a file that quietly kept the override.
// It takes priority over ServerURL/CabinetURL below, which exist for a
// config file that already pinned an explicit URL before this variable did.
const DefaultHost = "gate4.ai"

func effectiveHost() string {
	if h := os.Getenv("GATE4AI_SYNC_HOST"); h != "" {
		return h
	}
	return ""
}

// DefaultServerURL is used when ServerURL is unset — a fresh config, or one
// from before this field existed.
const DefaultServerURL = "https://dav." + DefaultHost

// EffectiveServerURL is, in order: "https://dav.$GATE4AI_SYNC_HOST" if that
// variable is set, else ServerURL if that was ever saved, else
// DefaultServerURL.
func (c *Config) EffectiveServerURL() string {
	if h := effectiveHost(); h != "" {
		return "https://dav." + h
	}
	if c.ServerURL == "" {
		return DefaultServerURL
	}
	return c.ServerURL
}

// DefaultCabinetURL is used when CabinetURL is unset.
const DefaultCabinetURL = "https://" + DefaultHost

// EffectiveCabinetURL mirrors EffectiveServerURL for the cabinet host.
func (c *Config) EffectiveCabinetURL() string {
	if h := effectiveHost(); h != "" {
		return "https://" + h
	}
	if c.CabinetURL == "" {
		return DefaultCabinetURL
	}
	return c.CabinetURL
}

// Folder looks up a folder by id.
func (c *Config) Folder(id string) (Folder, bool) {
	for _, f := range c.Folders {
		if f.ID == id {
			return f, true
		}
	}
	return Folder{}, false
}

// SetFolder replaces the folder with the same ID, if there is one.
func (c *Config) SetFolder(f Folder) {
	for i := range c.Folders {
		if c.Folders[i].ID == f.ID {
			c.Folders[i] = f
			return
		}
	}
}

// RemoveFolder drops a folder from the list. It does not disable its mount
// on the server — the caller (the sync loop) does that first, since the
// mount id is what tells the server which files to delete.
func (c *Config) RemoveFolder(id string) {
	out := c.Folders[:0]
	for _, f := range c.Folders {
		if f.ID != id {
			out = append(out, f)
		}
	}
	c.Folders = out
}

// dir is where gate4ai-sync keeps everything of its own: the config file
// and, alongside it, one sync manifest per folder. $GATE4AI_SYNC_CONFIG
// overrides just the config file's own path (for tests); manifests always
// follow $GATE4AI_SYNC_DIR or the OS default.
func dir() (string, error) {
	if d := os.Getenv("GATE4AI_SYNC_DIR"); d != "" {
		return d, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "gate4ai-sync"), nil
}

// path returns the config file location, honoring $GATE4AI_SYNC_CONFIG for
// tests and creating the parent directory as needed.
func path() (string, error) {
	if p := os.Getenv("GATE4AI_SYNC_CONFIG"); p != "" {
		return p, nil
	}
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// ManifestPath is where a folder's own sync manifest lives — see
// internal/syncengine.Manifest. One file per folder ID, so removing a
// folder and adding a different one never reads stale state.
func ManifestPath(folderID string) (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "manifests", folderID+".json"), nil
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
		return &Config{ClientID: NewID()}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.ClientID == "" {
		cfg.ClientID = NewID()
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
