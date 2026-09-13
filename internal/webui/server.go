// Package webui is the client's entire user interface: an HTTP server on
// loopback, same-origin, no CORS or mixed-content concerns because it is
// opened as a plain page in the system default browser. There is no native
// GUI here at all — no dialogs, no file pickers beyond an HTML page walking
// os.ReadDir — see the issue this client implements and CLAUDE.md's
// "минимум интерфейса на клиенте".
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
)

// Server is the loopback settings UI.
type Server struct {
	Config *config.Config
	Mu     *sync.Mutex // shared with the sync loop that also reads/writes Config
	// SaveConfig persists Config after a handler mutates it.
	SaveConfig func() error
	Control    func() *controlclient.Client
	Log        *slog.Logger

	listener net.Listener
	http     *http.Server
}

// Start binds a random loopback port and begins serving. Addr() is valid
// once this returns.
func (s *Server) Start() error {
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	s.listener = l

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("GET /browse", s.browse)
	mux.HandleFunc("POST /folders", s.addFolder)
	mux.HandleFunc("POST /folders/remove", s.removeFolder)
	mux.HandleFunc("POST /save", s.save)

	s.http = &http.Server{Handler: mux}
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

func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
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
