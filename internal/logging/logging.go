// Package logging sets up the client's single log sink: structured text on
// stderr. No rotation, no files, no third-party logging library — a tray
// app has no console for a user to read anyway, and whoever needs the log
// runs it from a terminal or redirects it themselves.
package logging

import (
	"io"
	"log/slog"
)

// New returns an slog.Logger writing text-formatted records to w.
func New(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, nil))
}
