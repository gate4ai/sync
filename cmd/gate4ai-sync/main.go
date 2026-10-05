// Command gate4ai-sync is the gate4.ai file sync client: a tray app with no
// GUI of its own — all configuration happens through a browser page it
// opens on loopback. See ~/CLAUDE.md and docs/sync-api.md (gate4ai/server)
// for the protocol this talks.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gate4ai/sync/internal/autostart"
	"github.com/gate4ai/sync/internal/browser"
	"github.com/gate4ai/sync/internal/config"
	"github.com/gate4ai/sync/internal/controlclient"
	"github.com/gate4ai/sync/internal/instance"
	"github.com/gate4ai/sync/internal/logging"
	"github.com/gate4ai/sync/internal/loop"
	"github.com/gate4ai/sync/internal/status"
	"github.com/gate4ai/sync/internal/trayapp"
	"github.com/gate4ai/sync/internal/webui"
)

// shutdownWait is how long quitting waits for the sync loop to stop.
const shutdownWait = 3 * time.Second

// settingsAddr is where the settings page lives whenever the port is free,
// so its address stays the same from one launch to the next.
//
// 23456 is below every OS's ephemeral port range (Linux starts at 32768,
// macOS and Windows at 49152 — where Hyper-V, WSL and Docker also take
// their reservations), so an outgoing connection never holds it.
const settingsAddr = "127.0.0.1:23456"

func main() {
	inv, err := parseArgs(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	// "gate4ai-sync url" prints the running client's settings address —
	// what the installer shows, and the way back to the page for anyone
	// who closed it and has no tray to click. Plain output, no log lines:
	// it is read by people and by install.sh.
	if inv.printURL {
		if err := printSettingsURL(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	log := logging.New(os.Stderr)
	if err := run(log, inv.host); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

// invocation is what the command line asked for.
type invocation struct {
	// host is --host: which gate4.ai deployment to use from now on.
	host string
	// printURL is the "url" command: print the running client's settings
	// address instead of starting one.
	printURL bool
}

const usageText = `Usage:
  gate4ai-sync [--host HOST]   start the client (tray icon + settings page)
  gate4ai-sync url             print the settings page address of the running client

Options:
`

// parseArgs reads the command line. Anything it does not know is refused:
// starting a whole second client over a mistyped word — a second tray icon
// and a second sync loop over the same folders — is the worst way to
// handle a typo.
func parseArgs(args []string, stderr io.Writer) (invocation, error) {
	fs := flag.NewFlagSet("gate4ai-sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), usageText)
		fs.PrintDefaults()
	}
	// --host is how an installer says which gate4.ai deployment it came
	// from (install.sh from test.gate4.ai passes test.gate4.ai); it is
	// saved, so later launches without it keep talking to the same one.
	host := fs.String("host", "", "gate4.ai deployment to use from now on, e.g. test.gate4.ai")
	if err := fs.Parse(withoutProcessSerial(args)); err != nil {
		return invocation{}, err
	}
	switch {
	case fs.NArg() == 0:
		return invocation{host: *host}, nil
	case fs.NArg() == 1 && fs.Arg(0) == "url":
		if *host != "" {
			return invocation{}, errors.New("--host is not used with url: it only prints the running client's address")
		}
		return invocation{printURL: true}, nil
	default:
		fs.Usage()
		return invocation{}, fmt.Errorf("unknown command %q", strings.Join(fs.Args(), " "))
	}
}

// run holds every early-exit path behind a single os.Exit in main, so no
// defer (cancel, stopSignals) is ever silently skipped by one buried deep
// in this function.
func run(log *slog.Logger, host string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	hostChanged := false
	if host != "" {
		if hostChanged, err = cfg.SetHost(host); err != nil {
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
		Log:           log,
		PreferredAddr: settingsAddr,
		InstanceToken: instance.NewToken(),
	}
	if err := ui.Start(); err != nil {
		return fmt.Errorf("start local web UI: %w", err)
	}
	settingsURL := "http://" + ui.Addr() + "/"
	log.Info("settings UI listening", "url", settingsURL)
	removeSettingsURL, err := recordSettingsURL(settingsURL, ui.InstanceToken)
	if err != nil {
		log.Warn("record settings URL", "err", err)
	}
	defer removeSettingsURL()

	// Sync isn't configured yet — open the settings page right away rather
	// than waiting for a click on the tray icon, since a person who just
	// installed the client has no reason yet to know it lives there.
	if !cfg.Linked {
		err := browser.Open(settingsURL)
		if err != nil {
			log.Warn("open settings in browser", "err", err)
		}
		// Started by hand from a terminal: say where the page is in words,
		// since the browser may not have come to the front.
		if isTerminal(os.Stderr) {
			fmt.Fprint(os.Stderr, setupHint(settingsURL, cfg.EffectiveCabinetURL(), err == nil))
		}
	}

	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		loop.Run(ctx, cfg, &mu, saveConfig, &st, wake, log)
	}()
	// shutdown cancels the loop and gives it a moment to return: the
	// transfer in flight aborts, and the folder's manifest is saved with
	// what was already done. Not waiting longer keeps quitting quick, and
	// stays inside the 10 s install.sh gives an old process to exit.
	shutdown := func() {
		cancel()
		shutdownUI(ui)
		select {
		case <-loopDone:
		case <-time.After(shutdownWait):
			log.Warn("sync loop did not stop in time; exiting anyway")
		}
	}

	// A signal (Ctrl+C, or a service manager stopping the process) shuts
	// down the same way the tray's own Quit item does.
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		<-sigCtx.Done()
		shutdown()
		removeSettingsURL()
		os.Exit(0)
	}()

	if err := trayapp.Run(settingsURL, cfg.EffectiveCabinetURL(), &st, shutdown, log); err != nil {
		return fmt.Errorf("tray: %w", err)
	}
	return nil
}

// shutdownUI stops the settings page on the way out. Bounded, because
// http.Server.Shutdown waits up to five seconds on a connection a browser
// opened ahead of time and never sent a request on — and an update
// (install.sh) waits for this process to exit, so its successor can take
// the same port. With shutdownWait for the loop after it, quitting takes
// at most about 3.5 s.
func shutdownUI(ui *webui.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = ui.Shutdown(ctx)
}

// setupHint is the "finish setup" note printed to a terminal; install.sh
// prints the same steps after launching the app. cabinetURL names the
// deployment the person will sign in to, and opened says whether a browser
// was started at all — the note does not claim one was when it was not.
func setupHint(settingsURL, cabinetURL string, opened bool) string {
	site := cabinetURL
	if u, err := url.Parse(cabinetURL); err == nil && u.Host != "" {
		site = u.Host
	}
	browserLine := "  (Tried to open it in your browser — if nothing came up, open the link above.)\n"
	if !opened {
		browserLine = "  (Could not open a browser here — open the link above yourself.)\n"
	}
	return "\nTo finish setup:\n" +
		"  1. Open: " + settingsURL + "\n" +
		"  2. Pick the folders to sync and press Sync — you'll be asked to sign in to " + site + ".\n" +
		browserLine + "\n" +
		"Lost the page later? Run: gate4ai-sync url — or use \"Open settings\" in the tray menu.\n\n"
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// recordSettingsURL leaves this client's settings address where
// printSettingsURL finds it; see internal/instance.
func recordSettingsURL(settingsURL, token string) (func(), error) {
	p, err := config.UIURLPath()
	if err != nil {
		return func() {}, err
	}
	return instance.Record(p, settingsURL, token)
}

// printSettingsURL prints the address of this user's running client.
func printSettingsURL() error {
	p, err := config.UIURLPath()
	if err != nil {
		return err
	}
	settingsURL, err := instance.Lookup(context.Background(), p)
	if err != nil {
		return err
	}
	fmt.Println(settingsURL)
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
