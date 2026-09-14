// Package status holds the one-line summary the local web UI shows under
// "Connected to vault ..." — when the loop last reached the server
// successfully, or what went wrong the last time it tried. It is runtime
// state, not configuration: nothing here is persisted, so a restart starts
// with "never synced yet" rather than replaying a stale timestamp.
package status

import (
	"sync"
	"time"
)

// Status is safe for concurrent use — the loop writes to it from its own
// goroutine, the web UI reads it while serving a request.
type Status struct {
	mu        sync.Mutex
	lastSync  time.Time
	lastError string
	erroredAt time.Time
}

// RecordSuccess marks that the client reached the server and applied
// whatever it needed to just now. It also clears any earlier error — a
// transient failure that has since recovered has nothing left to report.
func (s *Status) RecordSuccess(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSync = now
	s.lastError = ""
}

// RecordError marks that an attempt failed. lastSync is left untouched —
// the last time something actually succeeded is still worth knowing while
// the error is showing.
func (s *Status) RecordError(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = err.Error()
	s.erroredAt = now
}

// Snapshot is a plain copy for rendering — see Snapshot's own fields for
// what each one means.
type Snapshot struct {
	// LastSync is the zero time if nothing has ever succeeded yet.
	LastSync time.Time
	// Error is empty when the most recent attempt succeeded.
	Error     string
	ErroredAt time.Time
}

func (s *Status) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{LastSync: s.lastSync, Error: s.lastError, ErroredAt: s.erroredAt}
}
