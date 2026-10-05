package syncengine

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gate4ai/sync/internal/webdavclient"
)

// File is one path in a synced folder with the size it has on one side.
type File struct {
	Path string
	Size int64
}

// Filter decides which files take part in sync. The rules belong to the
// server (POST /api/sync/v1/plan, docs/sync-api.md in gate4ai/server) —
// the client keeps no copy of them, so it cannot drift from what the server
// and the cabinet's folder import apply. A file the filter refuses is left
// out of consideration entirely: not uploaded, not downloaded, and never
// deleted on either side because of it.
type Filter interface {
	// Accept answers positionally: the result's i-th entry is about files[i].
	Accept(ctx context.Context, files []File) ([]bool, error)
}

// AcceptAll is the Filter that lets every file through, for tests.
type AcceptAll struct{}

func (AcceptAll) Accept(_ context.Context, files []File) ([]bool, error) {
	out := make([]bool, len(files))
	for i := range out {
		out[i] = true
	}
	return out, nil
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
func SyncOnce(ctx context.Context, dav *webdavclient.Client, localDir, manifestPath string, filter Filter) (Result, error) {
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

	// One question for both sides: a path can have a different size locally
	// and remotely, so each (path, size) pair is asked about once.
	var files []File
	seen := map[File]bool{}
	for p, l := range local {
		if f := (File{p, l.Size}); !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	for p, r := range remote {
		if f := (File{p, r.Size}); !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	accept, err := filter.Accept(ctx, files)
	if err != nil {
		return res, fmt.Errorf("ask which files take part: %w", err)
	}
	if len(accept) != len(files) {
		return res, fmt.Errorf("ask which files take part: %d answers for %d files", len(accept), len(files))
	}
	accepted := make(map[File]bool, len(files))
	for i, f := range files {
		accepted[f] = accept[i]
	}
	for p, l := range local {
		if !accepted[File{p, l.Size}] {
			delete(local, p)
			res.Skipped++
		}
	}
	for p, r := range remote {
		if !accepted[File{p, r.Size}] {
			delete(remote, p)
			res.Skipped++
		}
	}

	// The manifest is saved even when an operation fails part way through
	// the plan (including the context being cancelled on shutdown): a record
	// only enters it after its transfer is confirmed, so what the completed
	// operations wrote is true, and the next poll need not redo them.
	opErr := apply(ctx, dav, localDir, Plan(local, remote, manifest), remote, manifest, &res)
	if err := Save(manifestPath, manifest); err != nil && opErr == nil {
		return res, fmt.Errorf("save manifest: %w", err)
	}
	return res, opErr
}

func apply(ctx context.Context, dav *webdavclient.Client, localDir string, ops []Op, remote map[string]RemoteState, manifest Manifest, res *Result) error {
	for _, op := range ops {
		switch op.Kind {
		case OpUpload:
			if err := upload(ctx, dav, localDir, op.Path, manifest); err != nil {
				return fmt.Errorf("upload %q: %w", op.Path, err)
			}
			res.Uploaded++
		case OpDownload:
			if err := download(ctx, dav, localDir, op.Path, remote[op.Path], manifest); err != nil {
				return fmt.Errorf("download %q: %w", op.Path, err)
			}
			res.Downloaded++
		case OpDeleteLocal:
			if err := os.Remove(filepath.Join(localDir, filepath.FromSlash(op.Path))); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("delete local %q: %w", op.Path, err)
			}
			delete(manifest, op.Path)
			res.DeletedLocal++
		case OpDeleteRemote:
			if err := dav.Delete(ctx, op.Path); err != nil {
				return fmt.Errorf("delete remote %q: %w", op.Path, err)
			}
			delete(manifest, op.Path)
			res.DeletedRemote++
		case OpForget:
			delete(manifest, op.Path)
		}
	}
	return nil
}

// tempPrefix starts the name of every file this package writes before
// renaming it into place. A process killed between the two leaves one
// behind; scanLocal neither syncs it (it would upload a half-written copy)
// nor keeps it.
const tempPrefix = ".gate4ai-sync-"

func isTemp(name string) bool { return strings.HasPrefix(name, tempPrefix) }

// writeAtomic puts data at path so that the file there is, at every moment,
// either what it was before or all of data with mtime set — never a
// truncated copy with a fresh mtime, which the next poll would read as a
// local edit newer than the server's and upload over the good version. A
// zero mtime leaves the write time.
func writeAtomic(path string, data []byte, mtime time.Time) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), tempPrefix+"*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	// Keep the mode of a file being replaced; CreateTemp's own 0600 is the
	// same mode a new file used to get.
	if info, statErr := os.Stat(path); statErr == nil {
		if err := f.Chmod(info.Mode().Perm()); err != nil {
			_ = f.Close()
			return err
		}
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	// Without this, a power cut after the rename can leave the new name
	// pointing at blocks that were never written.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(tmp, mtime, mtime); err != nil {
			return err
		}
	}
	return os.Rename(tmp, path)
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
		if isTemp(d.Name()) {
			// Left by a process that was killed mid-write: this package is
			// the only writer of such files and runs one folder at a time, so
			// none of them belongs to a write still in progress.
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove leftover temporary file: %w", err)
			}
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
	if err := writeAtomic(full, body, remote.ModTime); err != nil {
		return fmt.Errorf("write local file: %w", err)
	}
	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("stat local file after write: %w", err)
	}
	manifest[clientPath] = Record{Size: info.Size(), ModTime: info.ModTime(), ETag: remote.ETag}
	return nil
}
