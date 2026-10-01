// Package loop ties config, controlclient, webdavclient and syncengine
// together into the one background loop main.go runs: poll pairing until
// linked, register any folder that still needs a mount, then sync every
// registered folder — repeating on the interval the server hands back.
package loop

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/gate4ai/sync/internal/config"
	"github.com/gate4ai/sync/internal/controlclient"
	"github.com/gate4ai/sync/internal/status"
	"github.com/gate4ai/sync/internal/syncengine"
	"github.com/gate4ai/sync/internal/webdavclient"
)

// pendingInterval is how often to retry while unlinked, or after a
// control-API call fails — short enough that pairing feels responsive,
// long enough not to hammer a server that is genuinely down.
const pendingInterval = 15 * time.Second

// Run blocks until ctx is done, running one iteration immediately and then
// on whatever interval the server's settings (or pendingInterval, before
// that is known) say, or as soon as wake receives — the web UI sends on it
// when the folder list changes, so a newly added folder is registered now
// rather than after the rest of the poll interval. st records the outcome of
// each iteration — see internal/status — for the local web UI's "Status"
// line.
func Run(ctx context.Context, cfg *config.Config, mu *sync.Mutex, saveConfig func() error, st *status.Status, wake <-chan struct{}, log *slog.Logger) {
	plans := planCaches{}
	for {
		interval := runOnce(ctx, cfg, mu, saveConfig, st, plans, log)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func runOnce(ctx context.Context, cfg *config.Config, mu *sync.Mutex, saveConfig func() error, st *status.Status, plans planCaches, log *slog.Logger) time.Duration {
	mu.Lock()
	clientID, serverURL, linked := cfg.ClientID, cfg.EffectiveServerURL(), cfg.Linked
	mu.Unlock()
	control := &controlclient.Client{BaseURL: serverURL, ClientID: clientID}

	if !linked {
		pairing, err := control.PairingStatus(ctx)
		if err != nil {
			log.Warn("read pairing status", "err", err)
			st.RecordError(err, time.Now())
			return pendingInterval
		}
		if pairing.Status != "linked" {
			return pendingInterval
		}
		mu.Lock()
		cfg.Linked, cfg.VaultSlug = true, pairing.VaultSlug
		err = saveConfig()
		mu.Unlock()
		if err != nil {
			log.Error("save config after linking", "err", err)
		}
		log.Info("linked to vault", "vault_slug", pairing.VaultSlug)
	}

	settings, err := control.Settings(ctx)
	if err != nil {
		if errors.Is(err, controlclient.ErrUnauthorized) {
			log.Warn("server no longer accepts this client; re-pairing", "err", err)
			mu.Lock()
			cfg.Unlink()
			saveErr := saveConfig()
			mu.Unlock()
			if saveErr != nil {
				log.Error("save config after unlinking", "err", saveErr)
			}
		} else {
			log.Warn("read settings", "err", err)
		}
		st.RecordError(err, time.Now())
		return pendingInterval
	}

	mu.Lock()
	folders := append([]config.Folder(nil), cfg.Folders...)
	mu.Unlock()

	// Every folder is registered before any is synced: registering is one
	// quick call, syncing a big folder is not, and a folder added just now
	// should not sit at "registering…" behind the others' file transfers.
	var lastErr error
	for i, f := range folders {
		if f.Registered() {
			continue
		}
		f, err = register(ctx, control, saveConfig, cfg, mu, f)
		if err != nil {
			log.Error("register folder", "path", f.Path, "err", err)
			lastErr = err
			continue
		}
		folders[i] = f
	}

	for _, f := range folders {
		if !f.Registered() {
			continue
		}

		manifestPath, err := config.ManifestPath(f.ID)
		if err != nil {
			log.Error("resolve manifest path", "folder", f.Path, "err", err)
			lastErr = err
			continue
		}
		dav := &webdavclient.Client{BaseURL: serverURL, Username: f.Username, Secret: f.Secret}
		filter := &serverFilter{control: control, folderID: f.ID, version: settings.PolicyVersion, cache: plans.folder(f.ID)}
		res, err := syncengine.SyncOnce(ctx, dav, f.Path, manifestPath, filter)
		if err != nil {
			if errors.Is(err, webdavclient.ErrUnauthorized) {
				log.Warn("server no longer accepts this folder's mount credential; re-pairing", "path", f.Path, "err", err)
				mu.Lock()
				f.Slug, f.Username, f.Secret = "", "", ""
				cfg.SetFolder(f)
				cfg.Unlink()
				saveErr := saveConfig()
				mu.Unlock()
				if saveErr != nil {
					log.Error("save config after unlinking", "err", saveErr)
				}
			}
			log.Error("sync folder", "path", f.Path, "err", err)
			lastErr = err
			continue
		}
		if res.Uploaded+res.Downloaded+res.DeletedLocal+res.DeletedRemote+res.Skipped > 0 {
			log.Info("synced folder", "path", f.Path, "slug", f.Slug,
				"uploaded", res.Uploaded, "downloaded", res.Downloaded,
				"deleted_local", res.DeletedLocal, "deleted_remote", res.DeletedRemote,
				"skipped", res.Skipped)
		}
	}

	now := time.Now()
	if lastErr != nil {
		st.RecordError(lastErr, now)
	} else {
		st.RecordSuccess(now)
	}

	if settings.PollIntervalSeconds <= 0 {
		return pendingInterval
	}
	return time.Duration(settings.PollIntervalSeconds) * time.Second
}

// register calls POST /api/sync/v1/mounts for a folder that has never been
// registered, persisting the credential it gets back — see
// docs/sync-api.md on why that credential is only ever handed out once.
func register(ctx context.Context, control *controlclient.Client, saveConfig func() error, cfg *config.Config, mu *sync.Mutex, f config.Folder) (config.Folder, error) {
	reg, err := control.RegisterMount(ctx, f.ID, filepath.Base(f.Path))
	if err != nil {
		return f, err
	}
	f.Slug = reg.Slug
	if reg.Username != "" {
		f.Username, f.Secret = reg.Username, reg.Secret
	}
	mu.Lock()
	cfg.SetFolder(f)
	saveErr := saveConfig()
	mu.Unlock()
	return f, saveErr
}
