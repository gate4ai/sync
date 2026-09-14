// Package trayapp is the tray icon — the only UI this client draws itself.
// Its menu opens a browser to one of two places: the local web UI
// (internal/webui) for settings, or the cabinet on gate4.ai directly.
package trayapp

import (
	"context"
	_ "embed"
	"log/slog"
	"os"
	"time"

	"github.com/gate4ai/sync/internal/browser"
	"github.com/gate4ai/sync/internal/status"
	"github.com/gogpu/systray"
)

//go:embed icon.png
var iconPNG []byte

// iconPollInterval is how often the tray checks the loop's status to decide
// whether to show the error badge — independent of the sync loop's own
// interval, since a failure should turn the icon red promptly rather than
// waiting for the next sync attempt to be logged as "seen".
const iconPollInterval = 5 * time.Second

// Run blocks until Quit is clicked, then exits the process — see onQuit's
// doc comment for why this package does that itself rather than returning.
// settingsURL is opened in the default browser when "Open settings" is
// clicked — the caller builds it from the webui server's actual port.
// cabinetURL is where "Open gate4.ai" goes — the account's cabinet, not the
// local settings page. st is polled to show a red exclamation badge on the
// icon, and the error in its tooltip, whenever the last sync attempt
// failed. onQuit runs synchronously before exit, for whatever cleanup the
// caller needs (nothing is required; the sync loop's own manifest writes
// are already durable after every poll, not just at shutdown).
func Run(settingsURL, cabinetURL string, st *status.Status, onQuit func(), log *slog.Logger) error {
	tray := systray.New()
	menu := systray.NewMenu()
	menu.Add("Open settings", func() {
		if err := browser.Open(settingsURL); err != nil {
			log.Error("open settings in browser", "err", err)
		}
	})
	menu.Add("Open gate4.ai", func() {
		if err := browser.Open(cabinetURL); err != nil {
			log.Error("open cabinet in browser", "err", err)
		}
	})
	menu.AddSeparator()
	menu.Add("Quit", func() {
		// tray.Run()'s own doc says it "returns when Quit() is called", but
		// the library's own example (examples/basic/main.go, gogpu/systray
		// v0.3.0) calls Remove() followed by os.Exit(0) rather than relying
		// on that — Run pumps a native OS message loop per platform, which
		// evidently does not always hand control back cleanly. Following
		// the maintainer's own demonstrated pattern here rather than the
		// (unverified in practice) doc comment.
		if onQuit != nil {
			onQuit()
		}
		tray.Remove()
		os.Exit(0)
	})

	// Show is not decoration: on Linux the SNI protocol this library speaks
	// (internal/platform_linux.go in gogpu/systray) starts every icon in
	// "Passive" status and only "Active" ones are guaranteed visible — the
	// Ubuntu AppIndicators extension in particular hides Passive icons
	// rather than showing them collapsed. Without this call the tray
	// registers correctly on D-Bus (Introspect and the menu both work) but
	// never actually appears in the panel.
	tray.SetIcon(iconPNG).SetTooltip("gate4.ai sync").SetMenu(menu).Show()

	errorIcon, err := errorBadge(iconPNG)
	if err != nil {
		log.Warn("build error badge icon", "err", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go watchStatus(ctx, tray, errorIcon, st)
	defer cancel()

	return tray.Run()
}

// watchStatus polls st and swaps the tray icon and tooltip between the
// plain icon and the red-badged one, so a sync failure is visible without
// opening the menu or the web UI. It runs until ctx is canceled, which
// happens when Run's own Run() call returns (Quit was clicked).
func watchStatus(ctx context.Context, tray *systray.SystemTray, errorIcon []byte, st *status.Status) {
	wasError := false
	tick := time.NewTicker(iconPollInterval)
	defer tick.Stop()
	for {
		snap := st.Snapshot()
		isError := snap.Error != ""
		if isError != wasError {
			wasError = isError
			if isError && errorIcon != nil {
				tray.SetIcon(errorIcon).SetTooltip("gate4.ai sync — " + snap.Error)
			} else {
				tray.SetIcon(iconPNG).SetTooltip("gate4.ai sync")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
