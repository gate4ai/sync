// Package webui is the client's entire user interface: an HTTP server on
// loopback, same-origin, no CORS or mixed-content concerns because it is
// opened as a plain page in the system default browser. There is no native
// GUI here at all — no dialogs, no file pickers beyond an HTML page walking
// os.ReadDir — see the issue this client implements and CLAUDE.md's
// "minimal client-side interface" rule.
package webui

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/gate4ai/sync/internal/config"
	"github.com/gate4ai/sync/internal/controlclient"
	"github.com/gate4ai/sync/internal/instance"
	"github.com/gate4ai/sync/internal/status"
)

// Server is the loopback settings UI.
type Server struct {
	Config *config.Config
	Mu     *sync.Mutex // shared with the sync loop that also reads/writes Config
	// SaveConfig persists Config after a handler mutates it.
	SaveConfig func() error
	Control    func() *controlclient.Client
	// Status is the loop's own record of when it last reached the server
	// and what, if anything, went wrong — shown as the home page's "Status"
	// line.
	Status *status.Status
	// Wake asks the sync loop to run now instead of at the end of its poll
	// interval; handlers call it after changing the folder list. Optional.
	Wake func()
	Log  *slog.Logger
	// PreferredAddr is the loopback address tried first, so the settings
	// page keeps one address a person can bookmark or retype from the
	// installer's output. When it is taken (another instance, another
	// program) Start falls back to a random port. Empty means random.
	PreferredAddr string
	// InstanceToken is what GET /instance answers with — how
	// "gate4ai-sync url" tells this client apart from whatever else might
	// hold its old port. See internal/instance.
	InstanceToken string

	listener net.Listener
	http     *http.Server
}

// Start binds PreferredAddr, or a random loopback port when that is unset
// or taken, and begins serving. Addr() is valid once this returns.
func (s *Server) Start() error {
	var lc net.ListenConfig
	var l net.Listener
	var err error
	if s.PreferredAddr != "" {
		if l, err = lc.Listen(context.Background(), "tcp", s.PreferredAddr); err != nil {
			s.Log.Warn("preferred settings address taken, using a random port", "addr", s.PreferredAddr, "err", err)
		}
	}
	if l == nil {
		if l, err = lc.Listen(context.Background(), "tcp", "127.0.0.1:0"); err != nil {
			return fmt.Errorf("listen on loopback: %w", err)
		}
	}
	s.listener = l

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("GET /browse", s.browse)
	mux.HandleFunc("GET /logo.svg", serveLogo)
	mux.HandleFunc("GET "+instance.Path, s.instanceToken)
	mux.HandleFunc("POST /folders", s.addFolder)
	mux.HandleFunc("POST /folders/remove", s.removeFolder)
	mux.HandleFunc("POST /save", s.save)

	s.http = &http.Server{Handler: s.guard(mux)}
	go func() {
		if err := s.http.Serve(l); err != nil && err != http.ErrServerClosed {
			s.Log.Error("webui server", "err", err)
		}
	}()
	return nil
}

// Addr is the loopback address the settings page is served on, e.g.
// "127.0.0.1:54321" — the tray menu opens a browser to "http://" + Addr().
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Shutdown stops serving, letting requests in flight finish until ctx is
// done and then dropping whatever connections are left.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	if err := s.http.Shutdown(ctx); err != nil {
		return s.http.Close()
	}
	return nil
}

func (s *Server) instanceToken(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(s.InstanceToken))
}

func serveLogo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "max-age=86400")
	_, _ = w.Write(logoSVG)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	tmpl, err := template.New(name).Parse(templates[name])
	if err != nil {
		s.Log.Error("parse template", "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		s.Log.Error("render template", "name", name, "err", err)
	}
}
