// Package syncengine builds the plan one poll of one local folder acts on:
// what to upload, download, or delete on either side. See docs/sync-api.md
// and docs/Синхронизация.md (both in gate4ai/server) for the reasoning this
// implements — three-way comparison against a local manifest, last-write-wins
// on real conflicts, and never treating an empty or failed remote listing as
// "everything was deleted".
package syncengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Record is what the manifest remembers about one path as of the last
// successful sync — gate4ai/server's docs/Синхронизация.md calls the
// Remotely Save equivalent "prevSync": without it, a path missing on one
// side is indistinguishable between "just created on the other side" and
// "deleted here since we last agreed on it".
type Record struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	ETag    string    `json:"etag"`
}

// Manifest is keyed by client path (relative to the folder's mount root).
type Manifest map[string]Record

// Load reads a manifest file, returning an empty one if it does not exist
// yet — the state of a folder that has never synced.
func Load(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Save writes the manifest, creating its parent directory if needed. Called
// only after a poll's operations have actually succeeded — see Apply's
// contract in engine.go: a record written before the transfer it describes
// is confirmed is the failure mode this whole package exists to avoid.
func Save(path string, m Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
