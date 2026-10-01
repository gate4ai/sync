package loop

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gate4ai/sync/internal/config"
	"github.com/gate4ai/sync/internal/controlclient"
	"github.com/gate4ai/sync/internal/status"
)

// fakeServer answers both the control API and plain WebDAV — everything
// this package talks to lives on one host in production (cmd/dav in
// gate4ai/server), so one fake stands in for both here too.
type fakeServer struct {
	linked bool
	// pairingWait is the ?wait= of the last pairing-status request.
	pairingWait string
	files       map[string][]byte
	// propfindUnauthorized makes every PROPFIND answer 401, simulating a
	// mount whose credential the server no longer accepts.
	propfindUnauthorized bool
}

func newFakeServer(t *testing.T) (*httptest.Server, *fakeServer) {
	t.Helper()
	fs := &fakeServer{files: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(fs.serve))
	t.Cleanup(srv.Close)
	return srv, fs
}

func (fs *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/sync/v1/pairing/c1/status":
		fs.pairingWait = r.URL.Query().Get("wait")
		status := "pending"
		if fs.linked {
			status = "linked"
		}
		_ = json.NewEncoder(w).Encode(controlclient.PairingStatus{Status: status, VaultSlug: "home-pc"})

	case r.URL.Path == "/api/sync/v1/settings":
		_ = json.NewEncoder(w).Encode(controlclient.Settings{
			MaxFileSizeBytes: 1 << 20, PollIntervalSeconds: 3600, VaultSlug: "home-pc",
		})

	case r.URL.Path == "/api/sync/v1/plan":
		// The rules are the server's; this fake accepts everything.
		var req struct {
			Files []controlclient.PlanFile `json:"files"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		accept := make([]bool, len(req.Files))
		for i := range accept {
			accept[i] = true
		}
		_ = json.NewEncoder(w).Encode(controlclient.PlanResult{Accept: accept})

	case r.URL.Path == "/api/sync/v1/mounts" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(controlclient.RegisteredMount{Slug: "documents", Username: "v-1", Secret: "sekret"})

	case r.Method == "PROPFIND" && fs.propfindUnauthorized:
		w.WriteHeader(http.StatusUnauthorized)

	case r.Method == "PROPFIND":
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"></D:multistatus>`))

	case r.Method == http.MethodPut:
		p := strings.TrimPrefix(r.URL.Path, "/")
		body, _ := io.ReadAll(r.Body)
		fs.files[p] = body
		w.WriteHeader(http.StatusCreated)

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestRunOnceDoesNothingUntilLinked(t *testing.T) {
	srv, fs := newFakeServer(t)
	t.Setenv("GATE4AI_SYNC_DIR", t.TempDir())
	cfg := &config.Config{ClientID: "c1", ServerURL: srv.URL}
	mu := &sync.Mutex{}

	interval := runOnce(t.Context(), cfg, mu, cfg.Save, &status.Status{}, planCaches{}, slog.New(slog.DiscardHandler))
	// The fake answers pending at once, like a server that does not hold the
	// request, so the loop must not spin on it.
	if interval != pairingRecheck {
		t.Errorf("interval = %v, want pairingRecheck before linking", interval)
	}
	if cfg.Linked {
		t.Error("Linked became true before the server reported it")
	}
	if fs.pairingWait != "25" {
		t.Errorf("wait = %q, want the loop to ask the server to hold the request for 25s", fs.pairingWait)
	}
}

func TestRunOnceLinksRegistersAndSyncsOnce(t *testing.T) {
	srv, fs := newFakeServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	t.Setenv("GATE4AI_SYNC_DIR", t.TempDir())

	cfg := &config.Config{
		ClientID:  "c1",
		ServerURL: srv.URL,
		Folders:   []config.Folder{{ID: "folder-1", Path: dir}},
	}
	mu := &sync.Mutex{}
	fs.linked = true

	interval := runOnce(t.Context(), cfg, mu, cfg.Save, &status.Status{}, planCaches{}, slog.New(slog.DiscardHandler))

	if !cfg.Linked {
		t.Fatal("Linked is still false after the server reported linked")
	}
	if cfg.VaultSlug != "home-pc" {
		t.Errorf("VaultSlug = %q, want home-pc", cfg.VaultSlug)
	}
	if !cfg.Folders[0].Registered() {
		t.Fatalf("folder was not registered: %+v", cfg.Folders[0])
	}
	if cfg.Folders[0].Slug != "documents" {
		t.Errorf("Slug = %q, want documents", cfg.Folders[0].Slug)
	}
	// The fake server does not model per-credential mount scoping the way
	// gate4ai/server's internal/webdav does — a real server would see this
	// PUT land under the mount's own slug prefix. What matters here is that
	// the sync loop uploaded the file at all, using the credential
	// RegisterMount handed back.
	if _, ok := fs.files["a.md"]; !ok {
		t.Errorf("local file was not uploaded, files = %+v", fs.files)
	}
	if interval.Seconds() != 3600 {
		t.Errorf("interval = %v, want the server's 3600s poll interval", interval)
	}
}

func TestRunOnceSkipsAnAlreadyRegisteredFolder(t *testing.T) {
	srv, fs := newFakeServer(t)
	fs.linked = true
	dir := t.TempDir()
	t.Setenv("GATE4AI_SYNC_DIR", t.TempDir())

	registerCalls := 0
	origHandler := fs.serve
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/v1/mounts" && r.Method == http.MethodPost {
			registerCalls++
		}
		origHandler(w, r)
	})

	cfg := &config.Config{
		ClientID:  "c1",
		ServerURL: srv.URL,
		Linked:    true,
		Folders:   []config.Folder{{ID: "folder-1", Path: dir, Slug: "documents", Username: "v-1", Secret: "sekret"}},
	}
	mu := &sync.Mutex{}

	runOnce(t.Context(), cfg, mu, cfg.Save, &status.Status{}, planCaches{}, slog.New(slog.DiscardHandler))

	if registerCalls != 0 {
		t.Errorf("RegisterMount was called %d times for an already-registered folder, want 0", registerCalls)
	}
}

func TestRunOnceUnlinksOnStaleMountCredential(t *testing.T) {
	srv, fs := newFakeServer(t)
	fs.linked = true
	fs.propfindUnauthorized = true
	dir := t.TempDir()
	t.Setenv("GATE4AI_SYNC_DIR", t.TempDir())

	cfg := &config.Config{
		ClientID:  "c1",
		ServerURL: srv.URL,
		Linked:    true,
		VaultSlug: "home-pc",
		Folders:   []config.Folder{{ID: "folder-1", Path: dir, Slug: "documents", Username: "v-1", Secret: "sekret"}},
	}
	mu := &sync.Mutex{}

	runOnce(t.Context(), cfg, mu, cfg.Save, &status.Status{}, planCaches{}, slog.New(slog.DiscardHandler))

	if cfg.Linked {
		t.Error("Linked is still true after a 401 from the folder's mount")
	}
	if cfg.Folders[0].Registered() {
		t.Errorf("folder is still registered after a 401 from its mount: %+v", cfg.Folders[0])
	}
}

func TestRunRegistersANewFolderAsSoonAsItIsWoken(t *testing.T) {
	srv, fs := newFakeServer(t)
	fs.linked = true
	t.Setenv("GATE4AI_SYNC_DIR", t.TempDir())

	cfg := &config.Config{ClientID: "c1", ServerURL: srv.URL, Linked: true}
	mu := &sync.Mutex{}
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		defer close(done)
		Run(ctx, cfg, mu, cfg.Save, &status.Status{}, wake, slog.New(slog.DiscardHandler))
	}()
	defer func() { cancel(); <-done }()

	// The fake's poll interval is an hour, so only the wake can get this
	// folder registered within the test's lifetime.
	mu.Lock()
	cfg.Folders = append(cfg.Folders, config.Folder{ID: "folder-1", Path: t.TempDir()})
	mu.Unlock()
	wake <- struct{}{}

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		registered := len(cfg.Folders) == 1 && cfg.Folders[0].Registered()
		mu.Unlock()
		if registered {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the folder was not registered after a wake")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
