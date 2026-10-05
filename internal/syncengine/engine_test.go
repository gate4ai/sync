package syncengine_test

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gate4ai/sync/internal/syncengine"
	"github.com/gate4ai/sync/internal/webdavclient"
)

// fakeServer is the same minimal stand-in used in webdavclient's own tests:
// an in-memory file map behind Basic auth, answering PROPFIND in the exact
// shape internal/webdav (gate4ai/server) uses.
type fakeServer struct {
	files map[string][]byte
	// failPut names a path whose PUT is refused, to break a plan part way.
	failPut string
}

func newFakeServer(t *testing.T) (*httptest.Server, *fakeServer) {
	t.Helper()
	fs := &fakeServer{files: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(fs.serve))
	t.Cleanup(srv.Close)
	return srv, fs
}

func (fs *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	if u, p, ok := r.BasicAuth(); !ok || u != "u" || p != "s" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")

	switch r.Method {
	case http.MethodGet:
		body, ok := fs.files[p]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	case http.MethodPut:
		if p == fs.failPut {
			http.Error(w, "refused", http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		fs.files[p] = body
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		delete(fs.files, p)
		w.WriteHeader(http.StatusNoContent)
	case "PROPFIND":
		fs.propfind(w, p)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type msResponse struct {
	Href     string `xml:"D:href"`
	Propstat struct {
		Prop struct {
			ResourceType *struct {
				Collection *struct{} `xml:"D:collection"`
			} `xml:"D:resourcetype"`
			ContentLength *int64 `xml:"D:getcontentlength,omitempty"`
			LastModified  string `xml:"D:getlastmodified,omitempty"`
			ETag          string `xml:"D:getetag,omitempty"`
		} `xml:"D:prop"`
		Status string `xml:"D:status"`
	} `xml:"D:propstat"`
}

func (fs *fakeServer) propfind(w http.ResponseWriter, base string) {
	type multistatus struct {
		XMLName   xml.Name     `xml:"D:multistatus"`
		XMLNS     string       `xml:"xmlns:D,attr"`
		Responses []msResponse `xml:"D:response"`
	}
	ms := multistatus{XMLNS: "DAV:"}

	self := msResponse{Href: "/" + base}
	self.Propstat.Status = "HTTP/1.1 200 OK"
	if body, isFile := fs.files[base]; isFile {
		size := int64(len(body))
		self.Propstat.Prop.ResourceType = &struct {
			Collection *struct{} `xml:"D:collection"`
		}{}
		self.Propstat.Prop.ContentLength = &size
		self.Propstat.Prop.LastModified = time.Unix(int64(len(base)), 0).UTC().Format(http.TimeFormat)
		self.Propstat.Prop.ETag = `"` + base + "-" + string(rune('a'+len(body)%26)) + `"`
	} else {
		self.Propstat.Prop.ResourceType = &struct {
			Collection *struct{} `xml:"D:collection"`
		}{Collection: &struct{}{}}
	}
	ms.Responses = append(ms.Responses, self)

	prefix := base
	if prefix != "" {
		prefix += "/"
	}
	for p, body := range fs.files {
		if p == base || !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if strings.Contains(rest, "/") {
			continue
		}
		size := int64(len(body))
		r := msResponse{Href: "/" + p}
		r.Propstat.Status = "HTTP/1.1 200 OK"
		r.Propstat.Prop.ResourceType = &struct {
			Collection *struct{} `xml:"D:collection"`
		}{}
		r.Propstat.Prop.ContentLength = &size
		r.Propstat.Prop.LastModified = time.Unix(int64(len(p)), 0).UTC().Format(http.TimeFormat)
		r.Propstat.Prop.ETag = `"` + p + "-" + string(rune('a'+len(body)%26)) + `"`
		ms.Responses = append(ms.Responses, r)
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(ms)
}

func newClient(url string) *webdavclient.Client {
	return &webdavclient.Client{BaseURL: url, Username: "u", Secret: "s"}
}

func TestSyncOnceUploadsANewLocalFile(t *testing.T) {
	srv, fs := newFakeServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")

	res, err := syncengine.SyncOnce(t.Context(), newClient(srv.URL), dir, manifest, syncengine.AcceptAll{})
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if res.Uploaded != 1 {
		t.Errorf("Uploaded = %d, want 1", res.Uploaded)
	}
	if string(fs.files["a.md"]) != "hello" {
		t.Errorf("remote content = %q, want %q", fs.files["a.md"], "hello")
	}
}

func TestSyncOnceDownloadsANewRemoteFile(t *testing.T) {
	srv, fs := newFakeServer(t)
	fs.files["b.md"] = []byte("from server")
	dir := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "manifest.json")

	res, err := syncengine.SyncOnce(t.Context(), newClient(srv.URL), dir, manifest, syncengine.AcceptAll{})
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if res.Downloaded != 1 {
		t.Errorf("Downloaded = %d, want 1", res.Downloaded)
	}
	got, err := os.ReadFile(filepath.Join(dir, "b.md"))
	if err != nil {
		t.Fatalf("read local file: %v", err)
	}
	if string(got) != "from server" {
		t.Errorf("local content = %q, want %q", got, "from server")
	}
}

func TestSyncOnceIsAQuietNoOpOnASecondRunWithNoChanges(t *testing.T) {
	srv, _ := newFakeServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	client := newClient(srv.URL)

	if _, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.AcceptAll{}); err != nil {
		t.Fatalf("first SyncOnce: %v", err)
	}
	res, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.AcceptAll{})
	if err != nil {
		t.Fatalf("second SyncOnce: %v", err)
	}
	if res.Uploaded != 0 || res.Downloaded != 0 || res.DeletedLocal != 0 || res.DeletedRemote != 0 {
		t.Errorf("second run = %+v, want a no-op", res)
	}
}

// refuse is a Filter that refuses the listed paths, whatever their size —
// the rules themselves are the server's and are tested there.
type refuse map[string]bool

func (r refuse) Accept(_ context.Context, files []syncengine.File) ([]bool, error) {
	out := make([]bool, len(files))
	for i, f := range files {
		out[i] = !r[f.Path]
	}
	return out, nil
}

func TestSyncOnceLeavesARefusedFileAloneOnBothSides(t *testing.T) {
	srv, fs := newFakeServer(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".obsidian"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".obsidian", "app.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	fs.files["meeting.mp4"] = []byte("video")
	manifest := filepath.Join(t.TempDir(), "manifest.json")

	res, err := syncengine.SyncOnce(t.Context(), newClient(srv.URL), dir, manifest,
		refuse{".obsidian/app.json": true, "meeting.mp4": true})
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if res != (syncengine.Result{Skipped: 2}) {
		t.Errorf("res = %+v, want both files skipped and nothing else done", res)
	}
	if _, ok := fs.files[".obsidian/app.json"]; ok {
		t.Error("the refused local file was uploaded")
	}
	if _, err := os.Stat(filepath.Join(dir, "meeting.mp4")); !os.IsNotExist(err) {
		t.Error("the refused remote file was downloaded")
	}
	if _, ok := fs.files["meeting.mp4"]; !ok {
		t.Error("the refused remote file was deleted")
	}
}

// failingFilter stands in for a server that could not be asked.
type failingFilter struct{}

func (failingFilter) Accept(context.Context, []syncengine.File) ([]bool, error) {
	return nil, errors.New("server unreachable")
}

func TestSyncOnceTransfersNothingWhenTheFilterCannotAnswer(t *testing.T) {
	srv, fs := newFakeServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")

	if _, err := syncengine.SyncOnce(t.Context(), newClient(srv.URL), dir, manifest, failingFilter{}); err == nil {
		t.Fatal("SyncOnce succeeded without knowing which files take part")
	}
	if len(fs.files) != 0 {
		t.Errorf("remote files = %v, want nothing uploaded", fs.files)
	}
}

func TestSyncOnceDeletesLocallyAfterARemoteDeletion(t *testing.T) {
	srv, fs := newFakeServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	client := newClient(srv.URL)

	if _, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.AcceptAll{}); err != nil {
		t.Fatalf("first SyncOnce: %v", err)
	}
	delete(fs.files, "a.md")

	res, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.AcceptAll{})
	if err != nil {
		t.Fatalf("second SyncOnce: %v", err)
	}
	if res.DeletedLocal != 1 {
		t.Errorf("DeletedLocal = %d, want 1", res.DeletedLocal)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.md")); !os.IsNotExist(err) {
		t.Error("local file still exists after the remote deletion propagated")
	}
}

// A process killed between writing a download and renaming it into place
// leaves a temporary file and the user's previous copy. The next poll must
// neither upload that half-written file nor let it stand in for the real
// one, and must clear it away.
func TestSyncOnceIgnoresAndRemovesADownloadLeftByAKilledProcess(t *testing.T) {
	srv, fs := newFakeServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("v1"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	client := newClient(srv.URL)
	if _, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.AcceptAll{}); err != nil {
		t.Fatalf("first SyncOnce: %v", err)
	}

	fs.files["a.md"] = []byte("version two")
	fs.files["new.md"] = []byte("brand new")
	leftovers := []string{
		filepath.Join(dir, ".gate4ai-sync-123"),
		filepath.Join(dir, "sub", ".gate4ai-sync-456"),
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, p := range leftovers {
		if err := os.WriteFile(p, []byte("vers"), 0o600); err != nil {
			t.Fatalf("write leftover: %v", err)
		}
	}

	res, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.AcceptAll{})
	if err != nil {
		t.Fatalf("second SyncOnce: %v", err)
	}
	if res != (syncengine.Result{Downloaded: 2}) {
		t.Errorf("res = %+v, want just the two downloads", res)
	}
	for p := range fs.files {
		if strings.Contains(p, ".gate4ai-sync-") {
			t.Errorf("temporary file %q was uploaded", p)
		}
	}
	if string(fs.files["a.md"]) != "version two" {
		t.Errorf("remote a.md = %q, want the server's version untouched", fs.files["a.md"])
	}
	for name, want := range map[string]string{"a.md": "version two", "new.md": "brand new"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != want {
			t.Errorf("local %s = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, p := range leftovers {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("leftover %s was not removed", p)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".gate4ai-sync-") {
			t.Errorf("download left temporary file %s behind", e.Name())
		}
	}
}

func TestSyncOnceDownloadKeepsTheServersMtimeAndTheFilesMode(t *testing.T) {
	srv, fs := newFakeServer(t)
	dir := t.TempDir()
	local := filepath.Join(dir, "a.md")
	if err := os.WriteFile(local, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	client := newClient(srv.URL)
	if _, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.AcceptAll{}); err != nil {
		t.Fatalf("first SyncOnce: %v", err)
	}
	if err := os.Chmod(local, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// The chmod changed nothing the manifest compares, so the file still
	// reads as unchanged locally and the server's new version wins.
	fs.files["a.md"] = []byte("version two")

	if _, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.AcceptAll{}); err != nil {
		t.Fatalf("second SyncOnce: %v", err)
	}
	info, err := os.Stat(local)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if want := time.Unix(int64(len("a.md")), 0); !info.ModTime().Equal(want) {
		t.Errorf("mtime = %v, want the server's %v", info.ModTime(), want)
	}
	// Windows has no permission bits: Chmod only sets or clears read-only,
	// and every writable file reads back as 0666 whatever was asked for.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 kept from the replaced file", info.Mode().Perm())
	}
}

// A manifest cut short by an older version of this client must not stop
// the folder syncing: the poll starts over as a first sync and leaves a
// valid manifest behind.
func TestSyncOnceRecoversFromATruncatedManifest(t *testing.T) {
	srv, fs := newFakeServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	fs.files["b.md"] = []byte("from server")
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"a.md": {"size": 5, "mod_ti`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	res, err := syncengine.SyncOnce(t.Context(), newClient(srv.URL), dir, manifest, syncengine.AcceptAll{})
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if res != (syncengine.Result{Uploaded: 1, Downloaded: 1}) {
		t.Errorf("res = %+v, want one upload and one download, nothing deleted", res)
	}
	m, err := syncengine.Load(manifest)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m) != 2 {
		t.Errorf("manifest = %v, want records for a.md and b.md", m)
	}
}

// An operation failing part way through the plan — as one does when the
// context is cancelled on shutdown — still leaves the manifest recording
// every transfer that did complete, so the next poll does not redo them.
func TestSyncOnceSavesTheManifestForWhatCompletedBeforeAFailure(t *testing.T) {
	srv, fs := newFakeServer(t)
	fs.failPut = "c.md"
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md", "e.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("write local file: %v", err)
		}
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")

	if _, err := syncengine.SyncOnce(t.Context(), newClient(srv.URL), dir, manifest, syncengine.AcceptAll{}); err == nil {
		t.Fatal("SyncOnce succeeded although a PUT was refused")
	}
	m, err := syncengine.Load(manifest)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m) != len(fs.files) {
		t.Errorf("manifest has %d records, server has %d files; want one per completed upload", len(m), len(fs.files))
	}
	for p := range fs.files {
		if _, ok := m[p]; !ok {
			t.Errorf("uploaded %s has no manifest record", p)
		}
	}
	if _, ok := m["c.md"]; ok {
		t.Error("the refused upload has a manifest record")
	}
}
