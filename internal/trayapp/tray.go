// Package trayapp is the tray icon — the only UI this client draws itself,
// and it has exactly one menu item: "Open settings", which opens the local
// web UI (internal/webui) in the default browser. See the issue this
// client implements: "единственный пункт меню в трее — «Открыть настройки»".
package trayapp

import (
	_ "embed"
	"log/slog"
	"os"

	"github.com/gate4ai/sync/internal/browser"
	"github.com/gogpu/systray"
)

//go:embed icon.png
var iconPNG []byte

// Run blocks until Quit is clicked, then exits the process — see onQuit's
// doc comment for why this package does that itself rather than returning.
// settingsURL is opened in the default browser when "Open settings" is
// clicked — the caller builds it from the webui server's actual port.
// onQuit runs synchronously before exit, for whatever cleanup the caller
// needs (nothing is required; the sync loop's own manifest writes are
// already durable after every poll, not just at shutdown).
func Run(settingsURL string, onQuit func(), log *slog.Logger) error {
	tray := systray.New()
	menu := systray.NewMenu()
	menu.Add("Open settings", func() {
		if err := browser.Open(settingsURL); err != nil {
			log.Error("open settings in browser", "err", err)
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
	return tray.Run()
}
