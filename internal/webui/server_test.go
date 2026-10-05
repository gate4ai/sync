package webui_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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

// postForm posts like http.PostForm, following redirects within this
// server but stopping at one that leaves it — the cabinet's /link page an
// unlinked client is sent to. Following that would make the test depend on
// the network and on gate4.ai being up.
func (f *fixture) postForm(target string, form url.Values) (*http.Response, error) {
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Host != via[0].URL.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	return client.PostForm(target, form)
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

func TestAddFolderThenHomeListsIt(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()

	resp, err := f.postForm(f.url("/folders"), map[string][]string{"path": {dir}})
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

// setHome points os.UserHomeDir at dir on every platform: it reads $HOME on
// Unix and %USERPROFILE% on Windows.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// volumeRoot is the top of the volume a path sits on — "C:\\" on Windows, "/"
// elsewhere. The tests that need a whole volume use the one the temp folder is
// already on, rather than naming a drive that may not exist on the runner.
func volumeRoot(path string) string {
	if vol := filepath.VolumeName(path); vol != "" {
		return vol + string(filepath.Separator)
	}
	return string(filepath.Separator)
}

// containsFold is Contains ignoring case, for assertions on a rendered URL:
// html/template writes its percent escapes in lower case and url.QueryEscape
// in upper, and the temp paths here have upper-case letters of their own that
// cannot simply be folded away.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
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
		resp, err := f.postForm(f.url("/folders"), map[string][]string{"path": {dir}})
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

	resp, err := f.postForm(f.url("/folders"), map[string][]string{"path": {file.Name()}})
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

	resp, err := f.postForm(f.url("/folders/remove"), map[string][]string{"id": {"folder-1"}})
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

	resp, err := f.postForm(f.url("/folders/remove"), map[string][]string{"id": {"folder-1"}})
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
	// The button for the folder that is open is what makes a pasted path
	// usable: a pasted path lands you inside the folder you meant, so
	// per-row buttons alone would leave nothing to press.
	if !strings.Contains(string(body), "Sync this folder") {
		t.Error("browse page has no button for the folder that is open")
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

	resp, err := f.postForm(f.url("/folders/remove"), map[string][]string{"id": {"folder-1"}})
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

// The picker opens on the roots screen rather than in the home folder. That
// is the fix for the demo where a folder on D: could not be reached at all:
// the home folder is on C:, and walking up from it stops at the top of C:.
func TestPickerOpensOnTheRootsScreen(t *testing.T) {
	f := newFixture(t)
	home := t.TempDir()
	setHome(t, home)

	resp, err := http.Get(f.url("/browse"))
	if err != nil {
		t.Fatalf("GET /browse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)

	// Each platform names these after its own file manager, and the paste hint
	// is only useful if it names that platform's own shortcut.
	heading, hint := "Volumes", "Paste the full path"
	switch runtime.GOOS {
	case "windows":
		heading, hint = "Drives", "Copy as path"
	case "darwin":
		hint = "Option-Command-C"
	}
	if !strings.Contains(body, heading) {
		t.Errorf("roots screen does not list the volumes under %q:\n%s", heading, body)
	}
	if !strings.Contains(body, hint) {
		t.Errorf("roots screen does not say how to copy a path on this platform:\n%s", body)
	}
	if !containsFold(body, `href="/browse?path=`+url.QueryEscape(volumeRoot(home))+`"`) {
		t.Errorf("roots screen has no link to the volume this machine runs from:\n%s", body)
	}
	if !strings.Contains(body, "Quick access") || !strings.Contains(body, ">"+filepath.Base(home)+"<") {
		t.Errorf("roots screen is missing the home folder in quick access:\n%s", body)
	}
	if !strings.Contains(body, `name="path"`) {
		t.Errorf("roots screen has no box to paste a path into:\n%s", body)
	}
}

// Explorer's "Copy as path" wraps the path in quotes, which is what Sergey
// pasted. Both ways in have to accept it: the path box and the form that adds
// the folder.
func TestPastedPathWithQuotesIsAccepted(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()

	resp, err := http.Get(f.url("/browse?path=" + url.QueryEscape(`"`+dir+`"`)))
	if err != nil {
		t.Fatalf("GET /browse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `name="path" value="`+dir+`"`) {
		t.Errorf("quoted path did not open the folder:\n%s", b)
	}

	add, err := f.postForm(f.url("/folders"), map[string][]string{"path": {`"` + dir + `"`}})
	if err != nil {
		t.Fatalf("POST /folders: %v", err)
	}
	_ = add.Body.Close()
	if len(f.cfg.Folders) != 1 || f.cfg.Folders[0].Path != dir {
		t.Errorf("Folders = %+v, want one entry for %q", f.cfg.Folders, dir)
	}
}

// Pasting the path of a file is a normal way to say "that folder" — it is
// what Explorer gives you when a document is selected.
func TestPastingAFilePathOpensTheFolderHoldingIt(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "estimate.xlsx")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(f.url("/browse?path=" + url.QueryEscape(file)))
	if err != nil {
		t.Fatalf("GET /browse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)

	if !strings.Contains(body, "is a file — showing the folder it is in") {
		t.Errorf("no explanation of where the listing came from:\n%s", body)
	}
	if !strings.Contains(body, `name="path" value="`+dir+`"`) {
		t.Errorf("the folder holding the file was not offered for syncing:\n%s", body)
	}
}

// A typo in a pasted path must not be a dead end: the closest folder above it
// opens, and what was typed stays in the box to be corrected.
func TestPastingAMissingPathShowsTheClosestFolderAbove(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	typo := filepath.Join(dir, "Rabta", "Proekt")

	resp, err := http.Get(f.url("/browse?path=" + url.QueryEscape(typo)))
	if err != nil {
		t.Fatalf("GET /browse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)

	if !strings.Contains(body, "There is no folder at "+typo) {
		t.Errorf("the missing path is not named back:\n%s", body)
	}
	if !strings.Contains(body, `class="notice error"`) {
		t.Errorf("the missing path is not shown as a problem:\n%s", body)
	}
	if !strings.Contains(body, `value="`+typo+`"`) {
		t.Errorf("what was typed was dropped from the box:\n%s", body)
	}
	if !strings.Contains(body, `value="`+dir+`">`) {
		t.Errorf("the closest folder above was not opened:\n%s", body)
	}
}

// Every step of the trail is a link, so coming back up five levels is one
// click rather than five presses of "..".
func TestBrowseLinksEveryStepOfTheTrail(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(f.url("/browse?path=" + url.QueryEscape(deep)))
	if err != nil {
		t.Fatalf("GET /browse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)

	if !containsFold(body, `href="/browse?path=`+url.QueryEscape(filepath.Join(dir, "a"))+`"`) {
		t.Errorf("the parent is not a link in the trail:\n%s", body)
	}
	if !strings.Contains(body, `href="/browse"`) {
		t.Errorf("the trail does not lead back to the roots screen:\n%s", body)
	}
	if !strings.Contains(body, `<span class="here">b</span>`) {
		t.Errorf("the folder that is open is not the last step of the trail:\n%s", body)
	}
}

// Syncing a whole volume is legitimate but almost never what a single click
// meant, so it is a question first.
func TestSyncingAWholeVolumeAsksFirst(t *testing.T) {
	f := newFixture(t)
	root := volumeRoot(t.TempDir())

	resp, err := f.postForm(f.url("/folders"), map[string][]string{"path": {root}})
	if err != nil {
		t.Fatalf("POST /folders: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(b), "Sync the whole of "+root+"?") {
		t.Errorf("no confirmation was asked for:\n%s", b)
	}
	if !strings.Contains(string(b), `name="confirmed" value="1"`) {
		t.Errorf("the confirmation has no way to go ahead:\n%s", b)
	}
	if len(f.cfg.Folders) != 0 {
		t.Errorf("the volume was added without an answer: %+v", f.cfg.Folders)
	}
}

func TestConfirmedBroadFolderIsAdded(t *testing.T) {
	f := newFixture(t)
	root := volumeRoot(t.TempDir())

	resp, err := f.postForm(f.url("/folders"), map[string][]string{"path": {root}, "confirmed": {"1"}})
	if err != nil {
		t.Fatalf("POST /folders: %v", err)
	}
	_ = resp.Body.Close()

	if len(f.cfg.Folders) != 1 || f.cfg.Folders[0].Path != root {
		t.Errorf("Folders = %+v, want the confirmed folder added", f.cfg.Folders)
	}
}

func TestSyncingTheHomeFolderAsksFirst(t *testing.T) {
	f := newFixture(t)
	home := t.TempDir()
	setHome(t, home)

	resp, err := f.postForm(f.url("/folders"), map[string][]string{"path": {home}})
	if err != nil {
		t.Fatalf("POST /folders: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(b), "Sync your whole home folder?") {
		t.Errorf("no confirmation for the home folder:\n%s", b)
	}
	if len(f.cfg.Folders) != 0 {
		t.Errorf("the home folder was added without an answer: %+v", f.cfg.Folders)
	}
}

// Two mounts over the same files is the mistake here, so overlapping a folder
// that is already synced is a question too — in both directions.
func TestSyncingAFolderThatOverlapsASyncedOneAsksFirst(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "clients")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("holds a synced folder", func(t *testing.T) {
		f := newFixture(t)
		f.cfg.Folders = []config.Folder{{ID: "f1", Path: inner}}

		resp, err := f.postForm(f.url("/folders"), map[string][]string{"path": {dir}})
		if err != nil {
			t.Fatalf("POST /folders: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), "A folder inside this one is already synced") {
			t.Errorf("no confirmation:\n%s", b)
		}
		if !strings.Contains(string(b), inner) {
			t.Errorf("the confirmation does not name the folder it clashes with:\n%s", b)
		}
		if len(f.cfg.Folders) != 1 {
			t.Errorf("Folders = %+v, want only the one that was there", f.cfg.Folders)
		}
	})

	t.Run("sits inside a synced folder", func(t *testing.T) {
		f := newFixture(t)
		f.cfg.Folders = []config.Folder{{ID: "f1", Path: dir}}

		resp, err := f.postForm(f.url("/folders"), map[string][]string{"path": {inner}})
		if err != nil {
			t.Fatalf("POST /folders: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), "This folder is inside one that is already synced") {
			t.Errorf("no confirmation:\n%s", b)
		}
		if len(f.cfg.Folders) != 1 {
			t.Errorf("Folders = %+v, want only the one that was there", f.cfg.Folders)
		}
	})
}

// An ordinary folder is added with no question at all: the confirmations are
// there for the broad choices, not as a toll on every one.
func TestAnOrdinaryFolderIsAddedWithoutAQuestion(t *testing.T) {
	f := newFixture(t)
	setHome(t, t.TempDir())
	dir := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	resp, err := noRedirectClient().PostForm(f.url("/folders"), map[string][]string{"path": {dir}})
	if err != nil {
		t.Fatalf("POST /folders: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want a redirect rather than a question", resp.StatusCode)
	}
	if len(f.cfg.Folders) != 1 {
		t.Errorf("Folders = %+v, want the folder added straight away", f.cfg.Folders)
	}
}

// The folder that is open is shown with its status instead of a button when it
// is already configured, the same as any subfolder in the listing.
func TestBrowseMarksTheOpenFolderWhenItIsAlreadySynced(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	f.cfg.Folders = []config.Folder{{ID: "f1", Path: dir}}

	resp, err := http.Get(f.url("/browse?path=" + url.QueryEscape(dir)))
	if err != nil {
		t.Fatalf("GET /browse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "Sync this folder") {
		t.Errorf("an already-synced folder was offered again:\n%s", b)
	}
	if !strings.Contains(string(b), "Already syncing") {
		t.Errorf("the open folder is not marked as synced:\n%s", b)
	}
}

func TestStartFallsBackWhenPreferredAddrTaken(t *testing.T) {
	var lc net.ListenConfig
	taken, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()

	s := &webui.Server{PreferredAddr: taken.Addr().String(), Log: slog.New(slog.DiscardHandler)}
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(t.Context()) })
	if s.Addr() == taken.Addr().String() {
		t.Fatalf("Addr = %s, want a different port than the taken one", s.Addr())
	}

	free := taken.Addr().String()
	taken.Close()
	s2 := &webui.Server{PreferredAddr: free, Log: slog.New(slog.DiscardHandler)}
	if err := s2.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s2.Shutdown(t.Context()) })
	if s2.Addr() != free {
		t.Fatalf("Addr = %s, want the preferred %s", s2.Addr(), free)
	}
}

// send makes a request with the headers a browser would add, and does not
// follow redirects, so the guard's own answer is what the test sees.
func (f *fixture) send(t *testing.T, method, path, host string, header map[string]string, form url.Values) *http.Response {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(t.Context(), method, f.url(path), body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// A DNS-rebinding page reaches the loopback server under its own domain:
// the browser sends that domain as Host. It must not get the folder list.
func TestGuardRejectsForeignHost(t *testing.T) {
	f := newFixture(t)
	_, port, _ := net.SplitHostPort(f.srv.Addr())
	for _, path := range []string{"/", "/browse?path=" + url.QueryEscape(t.TempDir())} {
		resp := f.send(t, http.MethodGet, path, "evil.example:"+port, nil, nil)
		if resp.StatusCode != http.StatusMisdirectedRequest {
			t.Errorf("GET %s with a foreign Host: status %d, want %d", path, resp.StatusCode, http.StatusMisdirectedRequest)
		}
	}
	dir := t.TempDir()
	resp := f.send(t, http.MethodPost, "/folders", "evil.example:"+port, nil, url.Values{"path": {dir}, "confirmed": {"1"}})
	if resp.StatusCode != http.StatusMisdirectedRequest || len(f.cfg.Folders) != 0 {
		t.Errorf("POST with a foreign Host: status %d, folders %+v; want %d and none", resp.StatusCode, f.cfg.Folders, http.StatusMisdirectedRequest)
	}
}

func TestGuardAllowsLocalhostAndLoopbackHost(t *testing.T) {
	f := newFixture(t)
	_, port, _ := net.SplitHostPort(f.srv.Addr())
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port, "LOCALHOST:" + port} {
		if resp := f.send(t, http.MethodGet, "/", host, nil, nil); resp.StatusCode != http.StatusOK {
			t.Errorf("GET / with Host %s: status %d, want 200", host, resp.StatusCode)
		}
	}
}

// A form on another site posting to the settings server — the CSRF case —
// must not add or remove a folder, confirmed=1 or not.
func TestGuardRejectsCrossSitePost(t *testing.T) {
	cases := map[string]map[string]string{
		"Sec-Fetch-Site cross-site": {"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"},
		"Sec-Fetch-Site same-site":  {"Sec-Fetch-Site": "same-site"},
		"foreign Origin only":       {"Origin": "https://evil.example"},
		"opaque Origin":             {"Origin": "null"},
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			dir := t.TempDir()
			resp := f.send(t, http.MethodPost, "/folders", "", header, url.Values{"path": {dir}, "confirmed": {"1"}})
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status %d, want %d", resp.StatusCode, http.StatusForbidden)
			}
			if len(f.cfg.Folders) != 0 {
				t.Errorf("folder was added: %+v", f.cfg.Folders)
			}

			f.cfg.Folders = []config.Folder{{ID: "f1", Path: dir}}
			resp = f.send(t, http.MethodPost, "/folders/remove", "", header, url.Values{"id": {"f1"}})
			if resp.StatusCode != http.StatusForbidden || len(f.cfg.Folders) != 1 {
				t.Errorf("remove: status %d, folders %+v; want %d and the folder kept", resp.StatusCode, f.cfg.Folders, http.StatusForbidden)
			}
		})
	}
}

// The settings page's own forms still work, as a browser sends them.
func TestGuardAllowsSameOriginPost(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	header := map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://" + f.srv.Addr()}
	resp := f.send(t, http.MethodPost, "/folders", "", header, url.Values{"path": {dir}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	if len(f.cfg.Folders) != 1 {
		t.Errorf("Folders = %+v, want the one posted", f.cfg.Folders)
	}
}

func TestGuardForbidsFraming(t *testing.T) {
	f := newFixture(t)
	resp := f.send(t, http.MethodGet, "/", "", nil, nil)
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
		t.Errorf("Content-Security-Policy = %q", got)
	}
}

// GET /instance is how "gate4ai-sync url" knows the client on the recorded
// port is the one that recorded it.
func TestInstanceAnswersWithItsToken(t *testing.T) {
	f := newFixture(t)
	f.srv.InstanceToken = "tok-123"
	resp := f.send(t, http.MethodGet, "/instance", "", nil, nil)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(b) != "tok-123" {
		t.Errorf("GET /instance = %d %q, want 200 %q", resp.StatusCode, b, "tok-123")
	}
}
