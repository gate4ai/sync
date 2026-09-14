package syncengine_test

import (
	"testing"
	"time"

	"github.com/gate4ai/sync/internal/syncengine"
)

func opKinds(t *testing.T, ops []syncengine.Op) map[string]syncengine.OpKind {
	t.Helper()
	out := map[string]syncengine.OpKind{}
	for _, op := range ops {
		if _, dup := out[op.Path]; dup {
			t.Fatalf("Plan returned two ops for %q", op.Path)
		}
		out[op.Path] = op.Kind
	}
	return out
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
var t1 = t0.Add(time.Hour)

func TestNewLocalFileIsUploaded(t *testing.T) {
	local := map[string]syncengine.LocalState{"a.md": {Size: 5, ModTime: t0}}
	ops := opKinds(t, syncengine.Plan(local, nil, nil))
	if ops["a.md"] != syncengine.OpUpload {
		t.Errorf("a.md = %v, want upload", ops["a.md"])
	}
}

func TestNewRemoteFileIsDownloaded(t *testing.T) {
	remote := map[string]syncengine.RemoteState{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	ops := opKinds(t, syncengine.Plan(nil, remote, nil))
	if ops["a.md"] != syncengine.OpDownload {
		t.Errorf("a.md = %v, want download", ops["a.md"])
	}
}

func TestUnchangedFileOnBothSidesIsNoOp(t *testing.T) {
	manifest := syncengine.Manifest{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	local := map[string]syncengine.LocalState{"a.md": {Size: 5, ModTime: t0}}
	remote := map[string]syncengine.RemoteState{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	ops := syncengine.Plan(local, remote, manifest)
	if len(ops) != 0 {
		t.Errorf("Plan = %+v, want no ops for an unchanged file", ops)
	}
}

func TestLocallyModifiedFileIsUploaded(t *testing.T) {
	manifest := syncengine.Manifest{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	local := map[string]syncengine.LocalState{"a.md": {Size: 6, ModTime: t1}}
	remote := map[string]syncengine.RemoteState{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	ops := opKinds(t, syncengine.Plan(local, remote, manifest))
	if ops["a.md"] != syncengine.OpUpload {
		t.Errorf("a.md = %v, want upload", ops["a.md"])
	}
}

func TestRemotelyModifiedFileIsDownloaded(t *testing.T) {
	manifest := syncengine.Manifest{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	local := map[string]syncengine.LocalState{"a.md": {Size: 5, ModTime: t0}}
	remote := map[string]syncengine.RemoteState{"a.md": {Size: 7, ModTime: t1, ETag: `"2"`}}
	ops := opKinds(t, syncengine.Plan(local, remote, manifest))
	if ops["a.md"] != syncengine.OpDownload {
		t.Errorf("a.md = %v, want download", ops["a.md"])
	}
}

func TestFileDeletedRemotelyAndUnchangedLocallyIsDeletedLocally(t *testing.T) {
	manifest := syncengine.Manifest{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	local := map[string]syncengine.LocalState{"a.md": {Size: 5, ModTime: t0}}
	ops := opKinds(t, syncengine.Plan(local, nil, manifest))
	if ops["a.md"] != syncengine.OpDeleteLocal {
		t.Errorf("a.md = %v, want delete-local", ops["a.md"])
	}
}

func TestFileDeletedLocallyAndUnchangedRemotelyIsDeletedRemotely(t *testing.T) {
	manifest := syncengine.Manifest{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	remote := map[string]syncengine.RemoteState{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	ops := opKinds(t, syncengine.Plan(nil, remote, manifest))
	if ops["a.md"] != syncengine.OpDeleteRemote {
		t.Errorf("a.md = %v, want delete-remote", ops["a.md"])
	}
}

// A local edit racing a remote deletion must not destroy the edit: keeping
// the data (by re-uploading) is the safe default, not guessing that the
// deletion should win.
func TestLocallyModifiedAfterRemoteDeletionIsUploadedNotDeleted(t *testing.T) {
	manifest := syncengine.Manifest{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	local := map[string]syncengine.LocalState{"a.md": {Size: 9, ModTime: t1}}
	ops := opKinds(t, syncengine.Plan(local, nil, manifest))
	if ops["a.md"] != syncengine.OpUpload {
		t.Errorf("a.md = %v, want upload (data preserved over deletion)", ops["a.md"])
	}
}

func TestBothSidesChangedIsLastWriteWinsByModTime(t *testing.T) {
	manifest := syncengine.Manifest{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}

	// Local is newer: local wins.
	localNewer := map[string]syncengine.LocalState{"a.md": {Size: 6, ModTime: t1}}
	remoteOlder := map[string]syncengine.RemoteState{"a.md": {Size: 7, ModTime: t0, ETag: `"2"`}}
	ops := opKinds(t, syncengine.Plan(localNewer, remoteOlder, manifest))
	if ops["a.md"] != syncengine.OpUpload {
		t.Errorf("local newer: a.md = %v, want upload", ops["a.md"])
	}

	// Remote is newer: remote wins.
	localOlder := map[string]syncengine.LocalState{"a.md": {Size: 6, ModTime: t0}}
	remoteNewer := map[string]syncengine.RemoteState{"a.md": {Size: 7, ModTime: t1, ETag: `"2"`}}
	ops = opKinds(t, syncengine.Plan(localOlder, remoteNewer, manifest))
	if ops["a.md"] != syncengine.OpDownload {
		t.Errorf("remote newer: a.md = %v, want download", ops["a.md"])
	}
}

func TestPathGoneFromBothSidesIsForgotten(t *testing.T) {
	manifest := syncengine.Manifest{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	ops := opKinds(t, syncengine.Plan(nil, nil, manifest))
	if ops["a.md"] != syncengine.OpForget {
		t.Errorf("a.md = %v, want forget", ops["a.md"])
	}
}

func TestAnEmptyRemoteListingDeletesEverythingLocalThatWasEverSynced(t *testing.T) {
	// This documents a caller obligation, not a Plan behavior to fix: Plan
	// has no way to tell "the server has nothing" from "the listing failed
	// and came back empty". The poller (engine.go) must never call Plan with
	// an empty remote map unless PROPFIND genuinely succeeded and returned
	// nothing — see docs/sync-api.md's "PROPFIND is all or nothing".
	manifest := syncengine.Manifest{"a.md": {Size: 5, ModTime: t0, ETag: `"1"`}}
	local := map[string]syncengine.LocalState{"a.md": {Size: 5, ModTime: t0}}
	ops := opKinds(t, syncengine.Plan(local, map[string]syncengine.RemoteState{}, manifest))
	if ops["a.md"] != syncengine.OpDeleteLocal {
		t.Fatalf("a.md = %v, want delete-local — confirming the caller must guard this case itself", ops["a.md"])
	}
}
