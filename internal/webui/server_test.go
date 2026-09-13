package webui_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gate4ai/sync/internal/config"
	"github.com/gate4ai/sync/internal/controlclient"
	"github.com/gate4ai/sync/internal/webui"
)

// fakeControlServer answers the handful of control-API calls the webui
// package makes (Settings, DisableMount).
type fakeControlServer struct {
	disabled []string
}

func newFakeControlServer(t *testing.T) (*httptest.Server, *fakeControlServer) {
	t.Helper()
	fc := &fakeControlServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/sync/v1/settings":
			_ = json.NewEncoder(w).Encode(controlclient.Settings{
				MaxFileSizeBytes: 50 * 1024 * 1024, PollIntervalSeconds: 60, VaultSlug: "home-pc",
			})
		case strings.HasPrefix(r.URL.Path, "/api/sync/v1/mounts/") && r.Method == http.MethodDelete:
			fc.disabled = append(fc.disabled, strings.TrimPrefix(r.URL.Path, "/api/sync/v1/mounts/"))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, fc
}

type fixture struct {
	srv     *webui.Server
	control *fakeControlServer
	cfg     *config.Config
	saved   int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	controlSrv, control := newFakeControlServer(t)
	cfg := &config.Config{ClientID: "c1"}
	f := &fixture{control: control, cfg: cfg}

	s := &webui.Server{
		Config: cfg,
		Mu:     &sync.Mutex{},
		SaveConfig: func() error {
			f.saved++
			return nil
		},
		Control: func() *controlclient.Client {
			return &controlclient.Client{BaseURL: controlSrv.URL, ClientID: cfg.ClientID}
		},
		Log: slog.New(slog.DiscardHandler),
	}
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(t.Context()) })
	f.srv = s
	return f
}

func (f *fixture) url(path string) string {
	return "http://" + f.srv.Addr() + path
}

func TestHomeShowsUnlinkedState(t *testing.T) {
	f := newFixture(t)
	resp, err := http.Get(f.url("/"))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Not connected yet") {
		t.Errorf("home page did not mention being unconnected:\n%s", body)
	}
	if !strings.Contains(string(body), "Connect to gate4.ai") {
		t.Error("home page is missing the connect button")
	}
}

func TestAddFolderThenHomeListsIt(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()

	resp, err := http.PostForm(f.url("/folders"), map[string][]string{"path": {dir}})
	if err != nil {
		t.Fatalf("POST /folders: %v", err)
	}
	_ = resp.Body.Close()

	if len(f.cfg.Folders) != 1 || f.cfg.Folders[0].Path != dir {
		t.Fatalf("Folders = %+v, want one entry for %q", f.cfg.Folders, dir)
	}
	if f.saved == 0 {
		t.Error("SaveConfig was not called")
	}

	home, _ := http.Get(f.url("/"))
	body, _ := io.ReadAll(home.Body)
	_ = home.Body.Close()
	if !strings.Contains(string(body), dir) {
		t.Errorf("home page does not list the added folder:\n%s", body)
	}
}

func TestAddingTheSameFolderTwiceIsANoOp(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()

	for range 2 {
		resp, err := http.PostForm(f.url("/folders"), map[string][]string{"path": {dir}})
		if err != nil {
			t.Fatalf("POST /folders: %v", err)
		}
		_ = resp.Body.Close()
	}
	if len(f.cfg.Folders) != 1 {
		t.Errorf("Folders = %+v, want exactly one", f.cfg.Folders)
	}
}

func TestAddingAFileRatherThanADirectoryIsRefused(t *testing.T) {
	f := newFixture(t)
	file, err := os.CreateTemp(t.TempDir(), "not-a-dir")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	_ = file.Close()

	resp, err := http.PostForm(f.url("/folders"), map[string][]string{"path": {file.Name()}})
	if err != nil {
		t.Fatalf("POST /folders: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if len(f.cfg.Folders) != 0 {
		t.Error("a file was accepted as a folder")
	}
}

func TestRemovingARegisteredFolderDisablesItsMountFirst(t *testing.T) {
	f := newFixture(t)
	f.cfg.Folders = []config.Folder{{ID: "folder-1", Path: "/tmp/x", Username: "u", Secret: "s", Slug: "x"}}

	resp, err := http.PostForm(f.url("/folders/remove"), map[string][]string{"id": {"folder-1"}})
	if err != nil {
		t.Fatalf("POST /folders/remove: %v", err)
	}
	_ = resp.Body.Close()

	if len(f.control.disabled) != 1 || f.control.disabled[0] != "folder-1" {
		t.Errorf("disabled = %+v, want [folder-1]", f.control.disabled)
	}
	if len(f.cfg.Folders) != 0 {
		t.Error("folder was not removed locally")
	}
}

func TestRemovingAnUnregisteredFolderSkipsTheNetworkCall(t *testing.T) {
	f := newFixture(t)
	f.cfg.Folders = []config.Folder{{ID: "folder-1", Path: "/tmp/x"}}

	resp, err := http.PostForm(f.url("/folders/remove"), map[string][]string{"id": {"folder-1"}})
	if err != nil {
		t.Fatalf("POST /folders/remove: %v", err)
	}
	_ = resp.Body.Close()

	if len(f.control.disabled) != 0 {
		t.Errorf("disabled = %+v, want none (folder was never registered)", f.control.disabled)
	}
	if len(f.cfg.Folders) != 0 {
		t.Error("folder was not removed locally")
	}
}

func TestSaveRedirectsToLinkWithTheClientID(t *testing.T) {
	f := newFixture(t)
	f.cfg.CabinetURL = "https://cabinet.example"

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm(f.url("/save"), nil)
	if err != nil {
		t.Fatalf("POST /save: %v", err)
	}
	_ = resp.Body.Close()

	want := "https://cabinet.example/link?client=c1"
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestBrowseListsSubdirectoriesOnly(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	if err := os.Mkdir(dir+"/sub", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dir+"/file.txt", []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	resp, err := http.Get(f.url("/browse?path=" + dir))
	if err != nil {
		t.Fatalf("GET /browse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "sub/") {
		t.Errorf("browse page did not list the subfolder:\n%s", body)
	}
	if strings.Contains(string(body), "file.txt") {
		t.Error("browse page listed a plain file")
	}
}
