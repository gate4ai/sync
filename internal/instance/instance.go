// Package instance is how a running client tells the rest of the machine
// where its settings page is: the installer prints that address, and so does
// "gate4ai-sync url" for anyone who closed the page and has no tray to click.
// The browser the client opens on its own does not always come to the front,
// and the port can differ from launch to launch (see webui.Server's
// PreferredAddr), so the address is otherwise visible nowhere.
//
// A running client writes its address and a random token to a file only its
// user can read, and answers GET /instance with that token. Lookup trusts the
// address only when whatever listens there gives the token back: a file left
// by a crash can point at a port that another program — or another user's
// client — has taken since.
package instance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Path is the settings page's route that answers with the token.
const Path = "/instance"

// ErrNotRunning is Lookup's answer whenever no client of this user is
// serving the recorded address.
var ErrNotRunning = errors.New("gate4ai-sync is not running")

// NewToken is a fresh random token for one run of the client.
func NewToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails; see crypto/rand.Read
	return hex.EncodeToString(b)
}

// Record writes settingsURL and token to file. The returned func removes the
// file again, unless another client has since recorded its own there; it is
// safe to call more than once.
func Record(file, settingsURL, token string) (func(), error) {
	content := settingsURL + "\n" + token + "\n"
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return func() {}, err
	}
	// Written whole and renamed into place: the installer polls for this
	// file and must never read half of it.
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return func() {}, err
	}
	if err := os.Rename(tmp, file); err != nil {
		return func() {}, err
	}
	return func() {
		if data, err := os.ReadFile(file); err == nil && string(data) == content {
			_ = os.Remove(file)
		}
	}, nil
}

// Lookup returns the settings address recorded in file, once the client
// serving it has proved to be the one that recorded it.
func Lookup(ctx context.Context, file string) (string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", ErrNotRunning
	}
	settingsURL, token, ok := strings.Cut(strings.TrimSpace(string(data)), "\n")
	if !ok || token == "" {
		return "", ErrNotRunning
	}
	got, err := fetchToken(ctx, strings.TrimSuffix(settingsURL, "/")+Path)
	if err != nil || got != token {
		return "", ErrNotRunning
	}
	return settingsURL, nil
}

func fetchToken(ctx context.Context, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
