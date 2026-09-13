package webdavclient_test

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gate4ai/sync/internal/webdavclient"
)

// fakeServer emulates just enough of gate4ai/server's internal/webdav to
// exercise the client: an in-memory file map, Basic auth, and a PROPFIND
// response shaped exactly like webdav.go's (same element names, same
// D:-prefixed namespace, same href encoding) so a parsing bug here would
// also be a parsing bug against the real server.
type fakeServer struct {
	files map[string][]byte // client path -> content
	dirs  map[string]bool
}

func newFakeServer(t *testing.T) (*httptest.Server, *fakeServer) {
	t.Helper()
	fs := &fakeServer{files: map[string][]byte{}, dirs: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(fs.serve))
	t.Cleanup(srv.Close)
	return srv, fs
}

func (fs *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if !ok || user != "u" || pass != "s" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	p := r.URL.Path
	if len(p) > 0 && p[0] == '/' {
		p = p[1:]
	}

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
		if _, ok := fs.files[p]; !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		delete(fs.files, p)
		w.WriteHeader(http.StatusNoContent)
	case "MKCOL":
		fs.dirs[p] = true
		w.WriteHeader(http.StatusCreated)
	case "PROPFIND":
		fs.propfind(w, p)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// serverMultistatus, serverResponse etc. mirror internal/webdav/webdav.go's
// own XML types in gate4ai/server byte for byte, prefix included.
type serverMultistatus struct {
	XMLName   xml.Name         `xml:"D:multistatus"`
	XMLNS     string           `xml:"xmlns:D,attr"`
	Responses []serverResponse `xml:"D:response"`
}

type serverResponse struct {
	Href     string         `xml:"D:href"`
	Propstat serverPropstat `xml:"D:propstat"`
}

type serverPropstat struct {
	Prop   serverProp `xml:"D:prop"`
	Status string     `xml:"D:status"`
}

type serverProp struct {
	DisplayName   string              `xml:"D:displayname"`
	ResourceType  *serverResourceType `xml:"D:resourcetype"`
	ContentLength *int64              `xml:"D:getcontentlength,omitempty"`
	LastModified  string              `xml:"D:getlastmodified,omitempty"`
	ETag          string              `xml:"D:getetag,omitempty"`
}

type serverResourceType struct {
	Collection *struct{} `xml:"D:collection"`
}

func (fs *fakeServer) propfind(w http.ResponseWriter, base string) {
	ms := serverMultistatus{XMLNS: "DAV:"}
	ms.Responses = append(ms.Responses, serverResponse{
		Href: "/" + base + "/",
		Propstat: serverPropstat{
			Status: "HTTP/1.1 200 OK",
			Prop:   serverProp{DisplayName: "root", ResourceType: &serverResourceType{Collection: &struct{}{}}},
		},
	})
	prefix := ""
	if base != "" {
		prefix = base + "/"
	}
	for p, body := range fs.files {
		if p == base {
			continue
		}
		rest, ok := cut(p, prefix)
		if !ok || contains(rest, "/") {
			continue
		}
		size := int64(len(body))
		ms.Responses = append(ms.Responses, serverResponse{
			Href: "/" + p,
			Propstat: serverPropstat{
				Status: "HTTP/1.1 200 OK",
				Prop: serverProp{
					DisplayName:   rest,
					ResourceType:  &serverResourceType{},
					ContentLength: &size,
					LastModified:  time.Unix(0, 0).UTC().Format(http.TimeFormat),
					ETag:          `"etag-` + p + `"`,
				},
			},
		})
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	_ = enc.Encode(ms)
}

func cut(s, prefix string) (string, bool) {
	if prefix == "" {
		return s, true
	}
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func newClient(url string) *webdavclient.Client {
	return &webdavclient.Client{BaseURL: url, Username: "u", Secret: "s"}
}

func TestPutGetRoundTrip(t *testing.T) {
	srv, _ := newFakeServer(t)
	c := newClient(srv.URL)

	if err := c.Put(t.Context(), "notes/a.md", []byte("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := c.Get(t.Context(), "notes/a.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("Get = %q, want %q", got, "hello")
	}
}

func TestGetMissingIsErrNotFound(t *testing.T) {
	srv, _ := newFakeServer(t)
	c := newClient(srv.URL)
	if _, err := c.Get(t.Context(), "nope.md"); !errors.Is(err, webdavclient.ErrNotFound) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	srv, _ := newFakeServer(t)
	c := newClient(srv.URL)
	if err := c.Put(t.Context(), "a.md", []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.Delete(t.Context(), "a.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := c.Delete(t.Context(), "a.md"); err != nil {
		t.Fatalf("Delete again: %v, want no error for an already-gone path", err)
	}
}

func TestListParsesTheServersPropfindShape(t *testing.T) {
	srv, _ := newFakeServer(t)
	c := newClient(srv.URL)
	if err := c.Put(t.Context(), "notes/a.md", []byte("hello")); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := c.Put(t.Context(), "notes/b.md", []byte("hi")); err != nil {
		t.Fatalf("Put b: %v", err)
	}

	entries, err := c.List(t.Context(), "notes")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List = %d entries, want 2: %+v", len(entries), entries)
	}
	byPath := map[string]webdavclient.Entry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}
	a, ok := byPath["notes/a.md"]
	if !ok {
		t.Fatalf("missing notes/a.md in %+v", entries)
	}
	if a.IsDir {
		t.Error("a.md reported as a directory")
	}
	if a.Size != 5 {
		t.Errorf("Size = %d, want 5", a.Size)
	}
	if a.ETag == "" {
		t.Error("ETag is empty")
	}
}

func TestListDoesNotIncludeTheFolderItself(t *testing.T) {
	srv, _ := newFakeServer(t)
	c := newClient(srv.URL)
	if err := c.Put(t.Context(), "notes/a.md", []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	entries, err := c.List(t.Context(), "notes")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range entries {
		if e.Path == "notes" {
			t.Error("List included the requested folder itself")
		}
	}
}
