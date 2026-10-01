// Command gate4ai-sync is the gate4.ai file sync client: a tray app with no
// GUI of its own — all configuration happens through a browser page it
// opens on loopback. See ~/CLAUDE.md and docs/sync-api.md (gate4ai/server)
// for the protocol this talks.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/gate4ai/sync/internal/autostart"
	"github.com/gate4ai/sync/internal/browser"
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
	// --host is how an installer says which gate4.ai deployment it came
	// from (install.sh from test.gate4.ai passes test.gate4.ai); it is
	// saved, so later launches without it keep talking to the same one.
	host := flag.String("host", "", "gate4.ai deployment to use from now on, e.g. test.gate4.ai")
	if err := flag.CommandLine.Parse(withoutProcessSerial(os.Args[1:])); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	hostChanged := false
	if *host != "" {
		if hostChanged, err = cfg.SetHost(*host); err != nil {
			return err
		}
	}
	var mu sync.Mutex
	saveConfig := func() error { return cfg.Save() }
	if err := saveConfig(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if hostChanged {
		if err := cfg.RemoveManifests(); err != nil {
			return fmt.Errorf("remove manifests of the previous host: %w", err)
		}
		log.Info("switched deployment", "server", cfg.EffectiveServerURL())
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
	// Buffered by one: a wake sent while the loop is mid-iteration is kept
	// for the next pass, and any number of them collapse into that one.
	wake := make(chan struct{}, 1)
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
		Wake: func() {
			select {
			case wake <- struct{}{}:
			default:
			}
		},
		Log: log,
	}
	if err := ui.Start(); err != nil {
		return fmt.Errorf("start local web UI: %w", err)
	}
	settingsURL := "http://" + ui.Addr() + "/"
	log.Info("settings UI listening", "url", settingsURL)

	// Sync isn't configured yet — open the settings page right away rather
	// than waiting for a click on the tray icon, since a person who just
	// installed the client has no reason yet to know it lives there.
	if !cfg.Linked {
		if err := browser.Open(settingsURL); err != nil {
			log.Warn("open settings in browser", "err", err)
		}
	}

	go loop.Run(ctx, cfg, &mu, saveConfig, &st, wake, log)

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

	if err := trayapp.Run(settingsURL, cfg.EffectiveCabinetURL(), &st, func() {
		cancel()
		_ = ui.Shutdown(context.Background())
	}, log); err != nil {
		return fmt.Errorf("tray: %w", err)
	}
	return nil
}

// withoutProcessSerial drops the -psn_<n>_<n> argument macOS's Launch
// Services has been known to hand an app opened from Finder; the flag
// package would otherwise refuse to start over an unknown flag.
func withoutProcessSerial(args []string) []string {
	out := args[:0:0]
	for _, a := range args {
		if !strings.HasPrefix(a, "-psn_") {
			out = append(out, a)
		}
	}
	return out
}
