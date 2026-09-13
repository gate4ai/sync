// Command gate4ai-sync is the gate4.ai file sync client: a tray app with no
// GUI of its own — all configuration happens through a browser page it
// opens on loopback. See ~/CLAUDE.md and docs/sync-api.md (gate4ai/server)
// for the protocol this talks.
package main

import (
	"os"

	"github.com/gate4ai/sync/internal/config"
	"github.com/gate4ai/sync/internal/logging"
)

func main() {
	log := logging.New(os.Stderr)

	cfg, err := config.Load()
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}
	if err := cfg.Save(); err != nil {
		log.Error("save config", "err", err)
		os.Exit(1)
	}

	log.Info("gate4ai-sync starting", "client_id", cfg.ClientID, "paired", cfg.Paired())

	// Sync engine, local web UI, tray icon and autostart wiring land in
	// later stages (Sy1-Sy4, see docs/sync-api.md and the project plan).
}
