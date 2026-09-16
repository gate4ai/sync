// Package loop ties config, controlclient, webdavclient and syncengine
// together into the one background loop main.go runs: poll pairing until
// linked, register any folder that still needs a mount, then sync every
// registered folder — repeating on the interval the server hands back.
package loop

import (
	"context"
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
// that is known) say. st records the outcome of each iteration — see
// internal/status — for the local web UI's "Status" line.
func Run(ctx context.Context, cfg *config.Config, mu *sync.Mutex, saveConfig func() error, st *status.Status, log *slog.Logger) {
	for {
		interval := runOnce(ctx, cfg, mu, saveConfig, st, log)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func runOnce(ctx context.Context, cfg *config.Config, mu *sync.Mutex, saveConfig func() error, st *status.Status, log *slog.Logger) time.Duration {
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
		log.Warn("read settings", "err", err)
		st.RecordError(err, time.Now())
		return pendingInterval
	}
	policy := syncengine.NewPolicy(settings.AllowedExtensions, settings.MaxFileSizeBytes, settings.IndexDeny, log)

	mu.Lock()
	folders := append([]config.Folder(nil), cfg.Folders...)
	mu.Unlock()

	var lastErr error
	for _, f := range folders {
		if !f.Registered() {
			f, err = register(ctx, control, saveConfig, cfg, mu, f)
			if err != nil {
				log.Error("register folder", "path", f.Path, "err", err)
				lastErr = err
				continue
			}
		}

		manifestPath, err := config.ManifestPath(f.ID)
		if err != nil {
			log.Error("resolve manifest path", "folder", f.Path, "err", err)
			lastErr = err
			continue
		}
		dav := &webdavclient.Client{BaseURL: serverURL, Username: f.Username, Secret: f.Secret}
		res, err := syncengine.SyncOnce(ctx, dav, f.Path, manifestPath, policy)
		if err != nil {
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
