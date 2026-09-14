package syncengine

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gate4ai/sync/internal/webdavclient"
)

// Policy is the subset of the server's settings (controlclient.Settings)
// the engine needs to keep a file out of sync consideration entirely — see
// applyPolicy for why "out of consideration" and not "delete it" is what a
// disallowed file gets.
type Policy struct {
	// AllowedExtensions, nil meaning every extension is allowed. Compared
	// case-insensitively, dot included ("*.md" -> ".md").
	AllowedExtensions []string
	// MaxFileSizeBytes, 0 meaning no limit.
	MaxFileSizeBytes int64
}

func (p Policy) allows(clientPath string, size int64) bool {
	if p.MaxFileSizeBytes > 0 && size > p.MaxFileSizeBytes {
		return false
	}
	if p.AllowedExtensions == nil {
		return true
	}
	ext := strings.ToLower(filepath.Ext(clientPath))
	for _, a := range p.AllowedExtensions {
		if strings.ToLower(a) == ext {
			return true
		}
	}
	return false
}

// Result is what one SyncOnce call did, for the caller to log.
type Result struct {
	Uploaded, Downloaded, DeletedLocal, DeletedRemote, Skipped int
}

// SyncOnce runs one poll cycle for one local folder against the mount dav
// is scoped to. See docs/sync-api.md ("PROPFIND is all or nothing"): a
// failure listing the remote side aborts here, before the manifest is
// touched or anything is transferred, rather than being read as "the
// server has nothing".
func SyncOnce(ctx context.Context, dav *webdavclient.Client, localDir, manifestPath string, policy Policy) (Result, error) {
	var res Result

	manifest, err := Load(manifestPath)
	if err != nil {
		return res, fmt.Errorf("load manifest: %w", err)
	}

	local, err := scanLocal(localDir)
	if err != nil {
		return res, fmt.Errorf("scan local folder: %w", err)
	}
	remoteEntries, err := webdavclient.ListRecursive(ctx, dav, "")
	if err != nil {
		return res, fmt.Errorf("list remote: %w", err)
	}
	remote := make(map[string]RemoteState, len(remoteEntries))
	for p, e := range remoteEntries {
		remote[p] = RemoteState{Size: e.Size, ModTime: e.ModTime, ETag: e.ETag}
	}

	for p, l := range local {
		if !policy.allows(p, l.Size) {
			delete(local, p)
			res.Skipped++
		}
	}
	for p, r := range remote {
		if !policy.allows(p, r.Size) {
			delete(remote, p)
			res.Skipped++
		}
	}

	for _, op := range Plan(local, remote, manifest) {
		switch op.Kind {
		case OpUpload:
			if err := upload(ctx, dav, localDir, op.Path, manifest); err != nil {
				return res, fmt.Errorf("upload %q: %w", op.Path, err)
			}
			res.Uploaded++
		case OpDownload:
			if err := download(ctx, dav, localDir, op.Path, remote[op.Path], manifest); err != nil {
				return res, fmt.Errorf("download %q: %w", op.Path, err)
			}
			res.Downloaded++
		case OpDeleteLocal:
			if err := os.Remove(filepath.Join(localDir, filepath.FromSlash(op.Path))); err != nil && !os.IsNotExist(err) {
				return res, fmt.Errorf("delete local %q: %w", op.Path, err)
			}
			delete(manifest, op.Path)
			res.DeletedLocal++
		case OpDeleteRemote:
			if err := dav.Delete(ctx, op.Path); err != nil {
				return res, fmt.Errorf("delete remote %q: %w", op.Path, err)
			}
			delete(manifest, op.Path)
			res.DeletedRemote++
		case OpForget:
			delete(manifest, op.Path)
		}
	}

	if err := Save(manifestPath, manifest); err != nil {
		return res, fmt.Errorf("save manifest: %w", err)
	}
	return res, nil
}

func scanLocal(dir string) (map[string]LocalState, error) {
	out := map[string]LocalState{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = LocalState{Size: info.Size(), ModTime: info.ModTime()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// upload sends a local file and records the server's own ETag for it — not
// a value this side invented — so the next poll's remote-changed comparison
// is exact rather than approximate.
func upload(ctx context.Context, dav *webdavclient.Client, localDir, clientPath string, manifest Manifest) error {
	full := filepath.Join(localDir, filepath.FromSlash(clientPath))
	body, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}
	if err := dav.Put(ctx, clientPath, body); err != nil {
		return err
	}
	entry, err := dav.Stat(ctx, clientPath)
	if err != nil {
		return fmt.Errorf("stat after put: %w", err)
	}
	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("stat local file after put: %w", err)
	}
	manifest[clientPath] = Record{Size: info.Size(), ModTime: info.ModTime(), ETag: entry.ETag}
	return nil
}

// download writes a remote file locally and sets its mtime to the server's
// own Last-Modified — not the moment this download happened — so the next
// poll's local-changed comparison has a real value to compare against.
func download(ctx context.Context, dav *webdavclient.Client, localDir, clientPath string, remote RemoteState, manifest Manifest) error {
	body, err := dav.Get(ctx, clientPath)
	if err != nil {
		return err
	}
	full := filepath.Join(localDir, filepath.FromSlash(clientPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return fmt.Errorf("create local directory: %w", err)
	}
	if err := os.WriteFile(full, body, 0o600); err != nil {
		return fmt.Errorf("write local file: %w", err)
	}
	if !remote.ModTime.IsZero() {
		if err := os.Chtimes(full, remote.ModTime, remote.ModTime); err != nil {
			return fmt.Errorf("set local mtime: %w", err)
		}
	}
	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("stat local file after write: %w", err)
	}
	manifest[clientPath] = Record{Size: info.Size(), ModTime: info.ModTime(), ETag: remote.ETag}
	return nil
}
