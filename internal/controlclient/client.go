// Package controlclient talks to gate4ai/server's control API
// (docs/sync-api.md in that repo) — everything WebDAV cannot express:
// allowed types/size/poll interval, folder<->mount registration, and
// pairing status. See internal/webdavclient for the actual file transfer.
package controlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const clientIDHeader = "X-Gate4AI-Client-ID"

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
	return http.DefaultClient
}

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
	AllowedExtensions   []string `json:"allowed_extensions"`
	MaxFileSizeBytes    int64    `json:"max_file_size_bytes"`
	IndexDeny           []string `json:"index_deny"`
	PollIntervalSeconds int      `json:"poll_interval_seconds"`
	VaultSlug           string   `json:"vault_slug"`
	// SettingsURL is the cabinet's "Sync client" tab for this vault, or
	// empty if no folder has been registered yet (see docs/sync-api.md).
	SettingsURL string `json:"settings_url"`
}

func (c *Client) Settings(ctx context.Context) (Settings, error) {
	var s Settings
	err := c.do(ctx, http.MethodGet, "/api/sync/v1/settings", nil, &s)
	return s, err
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
