package webdavclient

import (
	"context"
	"fmt"
	"net/http"
)

// Stat is PROPFIND with Depth: 0 — the resource's own metadata, not its
// children. Used after a Put to learn the ETag the server assigned, since
// the PUT response itself carries none.
func (c *Client) Stat(ctx context.Context, clientPath string) (Entry, error) {
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", c.url(clientPath), nil)
	if err != nil {
		return Entry{}, fmt.Errorf("build PROPFIND request: %w", err)
	}
	req.SetBasicAuth(c.Username, c.Secret)
	req.Header.Set("Depth", "0")
	resp, err := c.client().Do(req)
	if err != nil {
		return Entry{}, fmt.Errorf("PROPFIND %s: %w", clientPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return Entry{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return Entry{}, statusError(resp)
	}
	ms, err := decodeMultistatus(resp)
	if err != nil {
		return Entry{}, fmt.Errorf("decode PROPFIND response for %s: %w", clientPath, err)
	}
	if len(ms.Responses) == 0 {
		return Entry{}, ErrNotFound
	}
	return entryFromResponse(ms.Responses[0])
}

// ListRecursive walks the whole tree under root and returns every file
// (directories are not included — this server has no directory metadata
// worth syncing, see docs/sync-api.md). It is built from repeated Depth: 1
// List calls rather than a single Depth: infinity PROPFIND, matching what
// gate4ai/server's own docs/Синхронизация.md recommends a client do (the
// server supports Depth: infinity, but nothing about a poll-based client
// needs the one round trip badly enough to lose the ability to fail one
// subtree without aborting the whole listing... except this client *does*
// want an all-or-nothing listing — see the caller in engine.go, which
// treats any error here as reason to abort the whole sync run).
func ListRecursive(ctx context.Context, c *Client, root string) (map[string]Entry, error) {
	files := map[string]Entry{}
	dirs := []string{root}
	for len(dirs) > 0 {
		dir := dirs[len(dirs)-1]
		dirs = dirs[:len(dirs)-1]

		children, err := c.List(ctx, dir)
		if err != nil {
			if dir == root {
				return nil, err
			}
			return nil, fmt.Errorf("list %q: %w", dir, err)
		}
		for _, e := range children {
			if e.IsDir {
				dirs = append(dirs, e.Path)
				continue
			}
			files[e.Path] = e
		}
	}
	return files, nil
}
