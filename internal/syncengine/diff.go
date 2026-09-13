package syncengine

import "time"

// LocalState is what one poll's directory scan found for a path.
type LocalState struct {
	Size    int64
	ModTime time.Time
}

// RemoteState is what one PROPFIND found for a path. ETag is the content
// hash the server computed (see vault.Note.ETag on the server) — more
// reliable than ModTime for "did the bytes change", since ETag never lies
// about an unchanged file the way a filesystem clock skew could.
type RemoteState struct {
	Size    int64
	ModTime time.Time
	ETag    string
}

// OpKind is what Op asks the caller to do.
type OpKind int

const (
	OpUpload OpKind = iota
	OpDownload
	OpDeleteLocal
	OpDeleteRemote
	// OpForget means the path is gone on both sides and only lives on in the
	// manifest — nothing to transfer, just drop the record.
	OpForget
)

func (k OpKind) String() string {
	switch k {
	case OpUpload:
		return "upload"
	case OpDownload:
		return "download"
	case OpDeleteLocal:
		return "delete-local"
	case OpDeleteRemote:
		return "delete-remote"
	case OpForget:
		return "forget"
	default:
		return "unknown"
	}
}

// Op is one decision Plan made about one path.
type Op struct {
	Path string
	Kind OpKind
}

// Plan is the three-way comparison at the center of this package: local and
// remote are this poll's actual state, manifest is what was true after the
// last successful sync. See the package comment for why manifest is what
// makes "created" and "deleted" distinguishable at all.
//
// This is deliberately simpler than Remotely Save's own algorithm (see
// gate4ai/server's docs/Синхронизация.md §2 for that one's full branch
// table): no configurable conflict strategy, no size-skip branches (the
// caller filters those out before Plan ever sees them, against the limits
// from controlclient.Settings), no percentage-based abort. Just: unknown on
// one side is new or deleted depending on the manifest, and known-different
// on both sides is last-write-wins by modification time — which is the
// whole of what the issue this client implements asks for.
func Plan(local map[string]LocalState, remote map[string]RemoteState, manifest Manifest) []Op {
	paths := map[string]struct{}{}
	for p := range local {
		paths[p] = struct{}{}
	}
	for p := range remote {
		paths[p] = struct{}{}
	}
	for p := range manifest {
		paths[p] = struct{}{}
	}

	var ops []Op
	for p := range paths {
		l, hasLocal := local[p]
		r, hasRemote := remote[p]
		prev, hasPrev := manifest[p]

		switch {
		case !hasLocal && !hasRemote:
			if hasPrev {
				ops = append(ops, Op{Path: p, Kind: OpForget})
			}

		case hasLocal && hasRemote:
			localChanged := !hasPrev || l.Size != prev.Size || !l.ModTime.Equal(prev.ModTime)
			remoteChanged := !hasPrev || r.Size != prev.Size || r.ETag != prev.ETag
			switch {
			case !localChanged && !remoteChanged:
				// Equal as far as the manifest is concerned — nothing to do.
			case localChanged && !remoteChanged:
				ops = append(ops, Op{Path: p, Kind: OpUpload})
			case !localChanged && remoteChanged:
				ops = append(ops, Op{Path: p, Kind: OpDownload})
			default:
				// Both sides moved since the last sync: a real conflict.
				// Last-write-wins by modification time, no conflict copies —
				// see the issue's own choice of strategy.
				if l.ModTime.After(r.ModTime) {
					ops = append(ops, Op{Path: p, Kind: OpUpload})
				} else {
					ops = append(ops, Op{Path: p, Kind: OpDownload})
				}
			}

		case hasLocal && !hasRemote:
			if hasPrev && l.Size == prev.Size && l.ModTime.Equal(prev.ModTime) {
				// Unchanged locally since the last sync, and gone from the
				// server: someone deleted it elsewhere.
				ops = append(ops, Op{Path: p, Kind: OpDeleteLocal})
			} else {
				// New locally, or changed here after the server's copy was
				// deleted — either way, keep the data by uploading it rather
				// than guessing that a deletion should win.
				ops = append(ops, Op{Path: p, Kind: OpUpload})
			}

		case !hasLocal && hasRemote:
			if hasPrev && r.Size == prev.Size && r.ETag == prev.ETag {
				ops = append(ops, Op{Path: p, Kind: OpDeleteRemote})
			} else {
				ops = append(ops, Op{Path: p, Kind: OpDownload})
			}
		}
	}
	return ops
}
