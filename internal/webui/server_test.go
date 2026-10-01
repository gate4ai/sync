package webui_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gate4ai/sync/internal/config"
	"github.com/gate4ai/sync/internal/controlclient"
	"github.com/gate4ai/sync/internal/status"
	"github.com/gate4ai/sync/internal/webui"
)

// fakeControlServer answers the handful of control-API calls the webui
// package makes (Settings, DisableMount).
type fakeControlServer struct {
	disabled   []string
	disableErr error
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
			if fc.disableErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
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
	status  *status.Status
	saved   int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	controlSrv, control := newFakeControlServer(t)
	cfg := &config.Config{ClientID: "c1"}
	st := &status.Status{}
	f := &fixture{control: control, cfg: cfg, status: st}

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
		Status: st,
		Log:    slog.New(slog.DiscardHandler),
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
	if strings.Contains(string(body), "Connect to gate4.ai") {
		t.Error("first-run home page offers a connect button; adding a folder starts pairing")
	}
}

func TestHomeOffersReconnectWhenUnlinkedWithFolders(t *testing.T) {
	f := newFixture(t)
	f.cfg.Folders = []config.Folder{{ID: "folder-1", Path: "/tmp/x", Slug: "x"}}

	resp, err := http.Get(f.url("/"))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.Contains(body, "Not connected") || strings.Contains(body, "Not connected yet") {
		t.Errorf("want the plain \"Not connected\" error heading:\n%s", body)
	}
	if strings.Contains(body, "Add a folder to sync") {
		t.Error("first-run hint shown to someone who connected before")
	}
	if !strings.Contains(body, "Connect to gate4.ai") {
		t.Error("home page is missing the connect button")
	}
}

func TestHomeShowsSyncedStatusWhenLinked(t *testing.T) {
	f := newFixture(t)
	f.cfg.Linked = true
	f.status.RecordSuccess(time.Now())

	resp, err := http.Get(f.url("/"))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Status: Synced just now") {
		t.Errorf("home page did not show a synced status:\n%s", body)
	}
}

func TestHomeShowsErrorStatusOverASuccessfulOne(t *testing.T) {
	f := newFixture(t)
	f.cfg.Linked = true
	f.status.RecordSuccess(time.Now().Add(-time.Hour))
	f.status.RecordError(errors.New("connection refused"), time.Now())

	resp, err := http.Get(f.url("/"))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "connection refused") {
		t.Errorf("home page did not show the error:\n%s", body)
	}
	if strings.Contains(string(body), "Synced") {
		t.Errorf("home page showed a stale success next to a fresh error:\n%s", body)
	}
}

func TestHomeShowsServerSettingsAsSeparateLabeledLines(t *testing.T) {
	f := newFixture(t)
	f.cfg.Linked = true

	resp, err := http.Get(f.url("/"))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	for _, name := range []string{"Allowed types", "Max file size", "Poll interval"} {
		if !strings.Contains(string(body), `class="setting-name">`+name) {
			t.Errorf("home page is missing a labeled row for %q:\n%s", name, body)
		}
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

func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestAddingAFolderOnAnUnlinkedClientGoesStraightToLink(t *testing.T) {
	f := newFixture(t)
	f.cfg.CabinetURL = "https://cabinet.example"
	dir := t.TempDir()

	resp, err := noRedirectClient().PostForm(f.url("/folders"), map[string][]string{"path": {dir}})
	if err != nil {
		t.Fatalf("POST /folders: %v", err)
	}
	_ = resp.Body.Close()

	if len(f.cfg.Folders) != 1 {
		t.Fatalf("Folders = %+v, want the folder saved before pairing", f.cfg.Folders)
	}
	want := "https://cabinet.example/link?client=c1&return=" + url.QueryEscape("http://"+f.srv.Addr()+"/")
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestAddingAFolderOnALinkedClientGoesHome(t *testing.T) {
	f := newFixture(t)
	f.cfg.Linked = true

	resp, err := noRedirectClient().PostForm(f.url("/folders"), map[string][]string{"path": {t.TempDir()}})
	if err != nil {
		t.Fatalf("POST /folders: %v", err)
	}
	_ = resp.Body.Close()

	if got := resp.Header.Get("Location"); got != "/" {
		t.Errorf("Location = %q, want /", got)
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

	resp, err := noRedirectClient().PostForm(f.url("/save"), nil)
	if err != nil {
		t.Fatalf("POST /save: %v", err)
	}
	_ = resp.Body.Close()

	want := "https://cabinet.example/link?client=c1&return=" + url.QueryEscape("http://"+f.srv.Addr()+"/")
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
	if !strings.Contains(string(body), ">Sync<") {
		t.Errorf("browse page is missing a per-row Sync button:\n%s", body)
	}
	if strings.Contains(string(body), "Sync this folder") {
		t.Error("browse page still has the old whole-folder Sync button")
	}
	if !strings.Contains(string(body), `>Cancel<`) {
		t.Error("browse page is missing a Cancel action")
	}
	if strings.Contains(string(body), "&larr; Back") {
		t.Error("browse page still has the old Back link")
	}
}

func TestBrowseShowsTheFullFoldersListEvenWhenBrowsingElsewhere(t *testing.T) {
	f := newFixture(t)
	elsewhere := t.TempDir()
	f.cfg.Folders = []config.Folder{
		{ID: "f1", Path: "/home/alex/Documents/obsidian", Username: "u", Secret: "s", Slug: "obsidian"},
	}

	resp, err := http.Get(f.url("/browse?path=" + elsewhere))
	if err != nil {
		t.Fatalf("GET /browse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "/home/alex/Documents/obsidian") {
		t.Errorf("browse page dropped the configured folder while browsing elsewhere:\n%s", body)
	}
	if !strings.Contains(string(body), `synced as <a href="https://gate4.ai/files/obsidian"`) {
		t.Errorf("browse page is missing the folder's status:\n%s", body)
	}
	if !strings.Contains(string(body), `name="id" value="f1"`) {
		t.Error("browse page is missing the Remove form for the configured folder")
	}
}

func TestBrowseKeepsAnAlreadySyncedFolderListedWithoutASyncButton(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	if err := os.Mkdir(dir+"/already", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f.cfg.Folders = []config.Folder{{ID: "f1", Path: dir + "/already"}}

	resp, err := http.Get(f.url("/browse?path=" + dir))
	if err != nil {
		t.Fatalf("GET /browse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "already/") {
		t.Errorf("browse page dropped an already-synced folder from the listing:\n%s", body)
	}
	if !strings.Contains(string(body), "Already syncing") {
		t.Errorf("browse page does not mark the already-synced folder:\n%s", body)
	}
}

func TestHomeLinksARegisteredFolderToItsFilesAndSettings(t *testing.T) {
	f := newFixture(t)
	f.cfg.Linked = true
	f.cfg.CabinetURL = "https://cabinet.example"
	f.cfg.Folders = []config.Folder{
		{ID: "f1", Path: "/tmp/obsidian", Username: "u", Secret: "s", Slug: "obsidian-copy"},
		{ID: "f2", Path: "/tmp/pending"},
	}

	resp, err := http.Get(f.url("/"))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	page := string(body)

	if !strings.Contains(page, `synced as <a href="https://cabinet.example/files/obsidian-copy"`) {
		t.Errorf("folder slug is not a link to its files:\n%s", page)
	}
	if !strings.Contains(page, `href="https://cabinet.example/settings/obsidian-copy"`) {
		t.Errorf("folder has no settings link:\n%s", page)
	}
	if strings.Count(page, `class="icon-btn"`) != 1 {
		t.Error("only the registered folder should get a settings link")
	}
	if strings.Contains(page, "Change on gate4.ai") {
		t.Error("the page-wide settings link is still there")
	}
}

func TestRemoveFailureShowsAnErrorPageAndKeepsTheFolder(t *testing.T) {
	f := newFixture(t)
	f.cfg.Folders = []config.Folder{{ID: "folder-1", Path: "/tmp/x", Username: "u", Secret: "s", Slug: "x"}}
	f.control.disableErr = errors.New("boom")

	resp, err := http.PostForm(f.url("/folders/remove"), map[string][]string{"id": {"folder-1"}})
	if err != nil {
		t.Fatalf("POST /folders/remove: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Could not remove the folder") || !strings.Contains(string(body), "<html") {
		t.Errorf("body is not the error page: %q", body)
	}
	if len(f.cfg.Folders) != 1 {
		t.Error("folder was removed despite the failure")
	}
}

func TestRemoveButtonIsDisabledForARegisteredFolderWhileNotLinked(t *testing.T) {
	f := newFixture(t)
	f.cfg.Linked = false
	f.cfg.Folders = []config.Folder{{ID: "folder-1", Path: "/tmp/x", Username: "u", Secret: "s", Slug: "x"}}

	resp, err := http.Get(f.url("/"))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `type="submit" disabled`) {
		t.Errorf("Remove button is not disabled: %s", body)
	}
}
