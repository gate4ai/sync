// Package controlclient talks to gate4ai/server's control API
// (docs/sync-api.md in that repo) — everything WebDAV cannot express:
// allowed types/size/poll interval, folder<->mount registration, and
// pairing status. See internal/webdavclient for the actual file transfer.
package controlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const clientIDHeader = "X-Gate4AI-Client-ID"

// ErrUnauthorized is returned by any call whose response was 401 — the
// server no longer accepts this ClientID, most often because pairing was
// revoked on the server side. Callers treat it the same as never having
// linked: reset Config.Linked and send the user through /link again.
var ErrUnauthorized = errors.New("unauthorized")

// Client is scoped to one client_id (see internal/config — it is minted
// once and never changes), which is all the control API needs to identify
// a linked installation. See docs/sync-api.md on why this is not the same
// credential WebDAV uses.
type Client struct {
	BaseURL    string
	ClientID   string
	HTTPClient *http.Client
}

func (c *Client) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return defaultHTTPClient
}

// defaultHTTPClient has a timeout, unlike http.DefaultClient: a control call
// that never answers would otherwise hold a web UI request (Remove waits on
// one) or the whole sync loop for as long as the connection stays open.
var defaultHTTPClient = &http.Client{Timeout: 2 * time.Minute}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.BaseURL, "/")+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.ClientID != "" {
		req.Header.Set(clientIDHeader, c.ClientID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: %s: %s: %w", method, path, resp.Status, strings.TrimSpace(string(msg)), ErrUnauthorized)
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response from %s %s: %w", method, path, err)
	}
	return nil
}

// Settings is GET /api/sync/v1/settings.
type Settings struct {
	// AllowedExtensions and MaxFileSizeBytes are shown to the owner; which
	// files take part is Plan's answer, not something worked out from them.
	AllowedExtensions []string `json:"allowed_extensions"`
	MaxFileSizeBytes  int64    `json:"max_file_size_bytes"`
	// PolicyVersion changes whenever Plan's answers could; a cache of those
	// answers is dropped when it does.
	PolicyVersion       string `json:"policy_version"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	VaultSlug           string `json:"vault_slug"`
}

func (c *Client) Settings(ctx context.Context) (Settings, error) {
	var s Settings
	err := c.do(ctx, http.MethodGet, "/api/sync/v1/settings", nil, &s)
	return s, err
}

// PlanFile is one entry of a POST /api/sync/v1/plan request: a path
// relative to the synced folder and its size in bytes.
type PlanFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// PlanResult is POST /api/sync/v1/plan's answer; Accept[i] is about the
// request's files[i].
type PlanResult struct {
	Accept        []bool `json:"accept"`
	PolicyVersion string `json:"policy_version"`
}

// Plan asks which of files take part in syncing the folder registered as
// folderID. The server holds the rules; see docs/sync-api.md.
func (c *Client) Plan(ctx context.Context, folderID string, files []PlanFile) (PlanResult, error) {
	var r PlanResult
	err := c.do(ctx, http.MethodPost, "/api/sync/v1/plan",
		map[string]any{"folder_id": folderID, "files": files}, &r)
	if err == nil && len(r.Accept) != len(files) {
		err = fmt.Errorf("POST /api/sync/v1/plan: %d answers for %d files", len(r.Accept), len(files))
	}
	return r, err
}

// Mount is one entry of GET /api/sync/v1/mounts.
type Mount struct {
	FolderID string `json:"folder_id"`
	Slug     string `json:"slug"`
}

func (c *Client) Mounts(ctx context.Context) ([]Mount, error) {
	var resp struct {
		Mounts []Mount `json:"mounts"`
	}
	err := c.do(ctx, http.MethodGet, "/api/sync/v1/mounts", nil, &resp)
	return resp.Mounts, err
}

// RegisteredMount is POST /api/sync/v1/mounts. Username and Secret are set
// only the first time a given folder_id is registered — see
// docs/sync-api.md on why the credential is not repeated.
type RegisteredMount struct {
	Slug     string `json:"slug"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
}

// RegisterMount is idempotent by folderID: calling it again for a folder
// this client already registered returns the same slug and no credential.
func (c *Client) RegisterMount(ctx context.Context, folderID, folderName string) (RegisteredMount, error) {
	var m RegisteredMount
	err := c.do(ctx, http.MethodPost, "/api/sync/v1/mounts", map[string]string{
		"folder_id": folderID, "folder_name": folderName,
	}, &m)
	return m, err
}

func (c *Client) DisableMount(ctx context.Context, folderID string) error {
	return c.do(ctx, http.MethodDelete, "/api/sync/v1/mounts/"+folderID, nil, nil)
}

// PairingStatus is GET /api/sync/v1/pairing/{client_id}/status.
type PairingStatus struct {
	Status    string `json:"status"` // "pending" or "linked"
	VaultSlug string `json:"vault_slug"`
}

func (c *Client) PairingStatus(ctx context.Context) (PairingStatus, error) {
	var s PairingStatus
	err := c.do(ctx, http.MethodGet, "/api/sync/v1/pairing/"+c.ClientID+"/status", nil, &s)
	return s, err
}
