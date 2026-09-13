package syncengine_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	res, err := syncengine.SyncOnce(t.Context(), newClient(srv.URL), dir, manifest, syncengine.Policy{})
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

	res, err := syncengine.SyncOnce(t.Context(), newClient(srv.URL), dir, manifest, syncengine.Policy{})
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

	if _, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.Policy{}); err != nil {
		t.Fatalf("first SyncOnce: %v", err)
	}
	res, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.Policy{})
	if err != nil {
		t.Fatalf("second SyncOnce: %v", err)
	}
	if res.Uploaded != 0 || res.Downloaded != 0 || res.DeletedLocal != 0 || res.DeletedRemote != 0 {
		t.Errorf("second run = %+v, want a no-op", res)
	}
}

func TestSyncOnceSkipsAFileOverTheSizeLimitWithoutTouchingEitherSide(t *testing.T) {
	srv, fs := newFakeServer(t)
	dir := t.TempDir()
	big := make([]byte, 100)
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")

	res, err := syncengine.SyncOnce(t.Context(), newClient(srv.URL), dir, manifest,
		syncengine.Policy{MaxFileSizeBytes: 10})
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if res.Uploaded != 0 || res.Skipped != 1 {
		t.Errorf("res = %+v, want the oversized file skipped and not uploaded", res)
	}
	if _, ok := fs.files["big.bin"]; ok {
		t.Error("the oversized file was uploaded despite the policy")
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

	if _, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.Policy{}); err != nil {
		t.Fatalf("first SyncOnce: %v", err)
	}
	delete(fs.files, "a.md")

	res, err := syncengine.SyncOnce(t.Context(), client, dir, manifest, syncengine.Policy{})
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
