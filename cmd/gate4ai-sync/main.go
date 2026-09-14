// Command gate4ai-sync is the gate4.ai file sync client: a tray app with no
// GUI of its own — all configuration happens through a browser page it
// opens on loopback. See ~/CLAUDE.md and docs/sync-api.md (gate4ai/server)
// for the protocol this talks.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/gate4ai/sync/internal/autostart"
	"github.com/gate4ai/sync/internal/config"
	"github.com/gate4ai/sync/internal/controlclient"
	"github.com/gate4ai/sync/internal/logging"
	"github.com/gate4ai/sync/internal/loop"
	"github.com/gate4ai/sync/internal/status"
	"github.com/gate4ai/sync/internal/trayapp"
	"github.com/gate4ai/sync/internal/webui"
)

func main() {
	log := logging.New(os.Stderr)
	if err := run(log); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

// run holds every early-exit path behind a single os.Exit in main, so no
// defer (cancel, stopSignals) is ever silently skipped by one buried deep
// in this function.
func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	var mu sync.Mutex
	saveConfig := func() error { return cfg.Save() }
	if err := saveConfig(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	log.Info("gate4ai-sync starting", "client_id", cfg.ClientID, "linked", cfg.Linked)

	// Best-effort: a person who declines it in their OS settings still gets
	// a fully working app, just started by hand.
	if err := autostart.Enable(); err != nil {
		log.Warn("enable autostart", "err", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var st status.Status
	ui := &webui.Server{
		Config:     cfg,
		Mu:         &mu,
		SaveConfig: saveConfig,
		Control: func() *controlclient.Client {
			mu.Lock()
			defer mu.Unlock()
			return &controlclient.Client{BaseURL: cfg.EffectiveServerURL(), ClientID: cfg.ClientID}
		},
		Status: &st,
		Log:    log,
	}
	if err := ui.Start(); err != nil {
		return fmt.Errorf("start local web UI: %w", err)
	}
	settingsURL := "http://" + ui.Addr() + "/"
	log.Info("settings UI listening", "url", settingsURL)

	go loop.Run(ctx, cfg, &mu, saveConfig, &st, log)

	// A signal (Ctrl+C, or a service manager stopping the process) shuts
	// down the same way the tray's own Quit item does.
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		<-sigCtx.Done()
		cancel()
		_ = ui.Shutdown(context.Background())
		os.Exit(0)
	}()

	if err := trayapp.Run(settingsURL, cfg.EffectiveCabinetURL(), func() {
		cancel()
		_ = ui.Shutdown(context.Background())
	}, log); err != nil {
		return fmt.Errorf("tray: %w", err)
	}
	return nil
}
