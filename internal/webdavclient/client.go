// Package webdavclient is a hand-written HTTP client for the WebDAV verbs
// gate4ai/server actually implements: GET, PUT, DELETE, MKCOL and PROPFIND
// (Depth 1 only — see docs/sync-api.md and docs/Синхронизация.md in
// gate4ai/server). No third-party WebDAV library: the server's PROPFIND
// response is hand-rolled too (not golang.org/x/net/webdav), a generic
// client would carry code for verbs and quirks (LOCK, chunked PATCH
// uploads, multiple Depth: infinity strategies) this server never needs,
// and the whole point of this client is that someone can read it in one
// sitting. See CLAUDE.md's "минимум зависимостей и логики".
package webdavclient

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// ErrNotFound is returned by Get and List for a path the server does not
// have.
var ErrNotFound = fmt.Errorf("not found")

// Client talks to one mount: BaseURL is the server address
// (https://dav.gate4.ai), Username and Secret are the credential
// controlclient.RegisterMount returned for this specific folder.
type Client struct {
	BaseURL    string
	Username   string
	Secret     string
	HTTPClient *http.Client
}

// Entry is one file or directory PROPFIND reported, in the client's own
// path space (relative to this mount's root, no vault or mount prefix —
// the server already stripped that, see vault.Mount.ClientPath on the
// server side).
type Entry struct {
	Path    string
	IsDir   bool
	Size    int64
	ModTime time.Time
	ETag    string
}

func (c *Client) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Client) url(clientPath string) string {
	segments := strings.Split(strings.Trim(clientPath, "/"), "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	joined := strings.Join(segments, "/")
	return strings.TrimSuffix(c.BaseURL, "/") + "/" + joined
}

func (c *Client) do(ctx context.Context, method, clientPath string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.url(clientPath), body)
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", method, err)
	}
	req.SetBasicAuth(c.Username, c.Secret)
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, clientPath, err)
	}
	return resp, nil
}

// Get downloads a file's bytes.
func (c *Client) Get(ctx context.Context, clientPath string) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, clientPath, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", clientPath, err)
	}
	return body, nil
}

// Put uploads a file's bytes, creating or overwriting it.
func (c *Client) Put(ctx context.Context, clientPath string, body []byte) error {
	resp, err := c.do(ctx, http.MethodPut, clientPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return statusError(resp)
	}
	return nil
}

// Delete removes a file or directory. A path that is already gone is not an
// error — deleting is idempotent, which is what lets the sync engine retry a
// half-applied plan without special-casing "already deleted".
func (c *Client) Delete(ctx context.Context, clientPath string) error {
	resp, err := c.do(ctx, http.MethodDelete, clientPath, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return statusError(resp)
	}
	return nil
}

// Mkcol creates a directory. Same idempotency reasoning as Delete: the
// server answers 405 for a mount root and 201 for a fresh collection, and a
// collection that already exists is not a failure the engine needs to act
// on.
func (c *Client) Mkcol(ctx context.Context, clientPath string) error {
	resp, err := c.do(ctx, "MKCOL", clientPath, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMethodNotAllowed {
		return statusError(resp)
	}
	return nil
}

// List returns the direct children of clientPath (Depth: 1) — never a
// deeper listing, and never an empty result papered over as success: see
// docs/sync-api.md on why a failed or partial listing must abort a sync run
// rather than be read as "everything here was deleted".
func (c *Client) List(ctx context.Context, clientPath string) ([]Entry, error) {
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", c.url(clientPath), nil)
	if err != nil {
		return nil, fmt.Errorf("build PROPFIND request: %w", err)
	}
	req.SetBasicAuth(c.Username, c.Secret)
	req.Header.Set("Depth", "1")
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("PROPFIND %s: %w", clientPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, statusError(resp)
	}

	ms, err := decodeMultistatus(resp)
	if err != nil {
		return nil, fmt.Errorf("decode PROPFIND response for %s: %w", clientPath, err)
	}

	base := strings.Trim(clientPath, "/")
	var out []Entry
	for _, r := range ms.Responses {
		p, err := hrefPath(r.Href)
		if err != nil {
			return nil, fmt.Errorf("decode href %q: %w", r.Href, err)
		}
		if p == base {
			continue // the folder itself, not a child
		}
		e, err := entryFromResponse(r)
		if err != nil {
			return nil, err
		}
		e.Path = p
		out = append(out, e)
	}
	return out, nil
}

func decodeMultistatus(resp *http.Response) (multistatus, error) {
	var ms multistatus
	err := xml.NewDecoder(resp.Body).Decode(&ms)
	return ms, err
}

// entryFromResponse builds an Entry from one <response> element. The
// caller sets Path — List derives it relative to the folder it asked
// about, Stat derives it from the request path itself.
func entryFromResponse(r response) (Entry, error) {
	p, err := hrefPath(r.Href)
	if err != nil {
		return Entry{}, fmt.Errorf("decode href %q: %w", r.Href, err)
	}
	return Entry{
		Path:    p,
		IsDir:   r.Propstat.Prop.ResourceType != nil && r.Propstat.Prop.ResourceType.Collection != nil,
		Size:    int64Or0(r.Propstat.Prop.ContentLength),
		ModTime: parseModTime(r.Propstat.Prop.LastModified),
		ETag:    r.Propstat.Prop.ETag,
	}, nil
}

// --- PROPFIND response decoding ---------------------------------------------
//
// Struct tags carry only local names, no namespace or prefix: the server
// writes every element as "D:foo" with xmlns:D="DAV:", and encoding/xml
// matches an untagged-namespace field by local name regardless of what
// prefix or namespace URI the document actually used.

type multistatus struct {
	Responses []response `xml:"response"`
}

type response struct {
	Href     string   `xml:"href"`
	Propstat propstat `xml:"propstat"`
}

type propstat struct {
	Prop prop `xml:"prop"`
}

type prop struct {
	ResourceType  *resourceType `xml:"resourcetype"`
	ContentLength *int64        `xml:"getcontentlength"`
	LastModified  string        `xml:"getlastmodified"`
	ETag          string        `xml:"getetag"`
}

type resourceType struct {
	Collection *struct{} `xml:"collection"`
}

func int64Or0(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func parseModTime(s string) time.Time {
	t, err := http.ParseTime(s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// hrefPath turns a PROPFIND href like "/documents/notes/a.md" into the
// client path "documents/notes/a.md" — percent-decoded, no leading or
// trailing slash.
func hrefPath(href string) (string, error) {
	u, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	return strings.Trim(path.Clean(u.Path), "/"), nil
}

func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
}
