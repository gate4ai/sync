# gate4ai/sync

## Installation

gate4.ai serves the installers of the latest release itself, at
[gate4.ai/help/install-sync](https://gate4.ai/help/install-sync). It mirrors every release
published here by [release.yml](.github/workflows/release.yml); the same files are on this
repo's [Releases page](../../releases) under versioned names.

### macOS and Ubuntu / Debian

One command for both — installs the latest release and starts the app:

```
curl -fsSL https://gate4.ai/install.sh | sh
```

On macOS it copies the app from the universal `.dmg` (one build for Apple Silicon and Intel)
into Applications. The app is not notarized/signed with an Apple Developer ID, but Gatekeeper
does not stop it: nothing is downloaded by a browser, and the script clears the quarantine
flag. On Linux it installs the amd64 `.deb` with apt. The script's source is
`web/public/install.sh` in [gate4ai/server](https://github.com/gate4ai/server); each
deployment serves it with its own host, and starts the client with `--host` (see
[Which server it talks to](#which-server-it-talks-to)).

By hand on Ubuntu / Debian:

```
curl -fLO https://gate4.ai/download/gate4ai-sync-amd64.deb
sudo apt install ./gate4ai-sync-amd64.deb
```

then launch **gate4.ai sync** from your application menu, or run `gate4ai-sync` from a
terminal.

### Windows

1. Download [gate4.ai/download/windows](https://gate4.ai/download/windows) (the `.msi`).
2. Run it. The installer is not code-signed, so SmartScreen may show "Windows protected your
   PC" — click **More info** → **Run anyway**.
3. It installs to `Program Files\gate4ai-sync` and adds a Start Menu shortcut.
4. Launch **gate4.ai sync** from the Start Menu; a tray icon appears.

The installers only place the binary — autostart on login is opt-in from the app's
own settings page (`internal/autostart`), not something the installer sets up for you.

Prefer a plain binary (other Linux distros, or you just want the file)? Every release also
publishes the raw `gate4ai-sync-<os>-<arch>` binaries the installers are built from; every
CI run on a branch or PR does the same under that run's **Artifacts** for un-released
builds (see [Building](#building) below).

File sync client for the gate4.ai server — a tray app for Mac, Windows and Linux, pure Go,
no CGO.

Becomes the primary sync channel: the third-party Obsidian remotely-save plugin stays as an
additional option, but stops being the only one. Unlike it, this client runs as its own
process — it does not depend on Obsidian being open — and syncs arbitrary local folders, not
just one editor's vault.

Problem statement — issue [gate4ai/server#38](https://github.com/gate4ai/server/issues/38).
Protocol (control API on top of WebDAV, pairing) —
[docs/sync-api.md](https://github.com/gate4ai/server/blob/dev/docs/sync-api.md) in the server
repository; that document is the single source of truth for the contract between client and
server.

## How it works

1. The first launch creates a tray icon with a single menu item — "Open settings".
2. Clicking it opens a local settings page in the browser (`http://127.0.0.1:23456/`, or a
   random port when 23456 is taken) — the client has no GUI of its own, all configuration
   happens through the browser. Until the client is linked, a launch opens the page on its
   own; since that browser window does not always come to the front, the installer (and a
   launch from a terminal) also prints the address, and `gate4ai-sync url` prints it for a
   running instance at any time.
3. There you pick local folders to sync (a plain HTML picker over `os.ReadDir`, no native
   dialogs) and press "Sync" — on an unlinked client this goes straight on to
   `https://gate4.ai/link?client=<id>`, where the installation gets attached to the
   account's vault. No separate "Connect" click is needed; the "Connect to gate4.ai"
   button on the home page stays only for coming back to an unfinished pairing.

   The picker opens on a "This PC" screen listing the drives and the folders most people
   mean, because without it a folder on a second drive could not be reached at all: it used
   to open in the home folder, and walking up from there stops at the top of `C:`. Above
   the listing is a box for a path pasted straight out of Explorer or Finder —
   `internal/localpath` unwraps the quotes "Copy as path" adds, `file://` URLs,
   `%USERPROFILE%` and `~`, opens the folder holding a path that names a file, and falls
   back to the closest folder above one that does not exist. A choice broad enough to be a
   mis-click — a whole drive, a home or system folder, or one overlapping a folder already
   synced — is confirmed first.
4. Once linked, the client registers each selected folder with the server on its own, gets a
   WebDAV credential per folder, and starts periodic two-way sync.
5. Allowed file types, the maximum file size and the poll interval are set in the cabinet on
   the server, not in this client.

## Which server it talks to

By default the client talks to production — `gate4.ai` (cabinet) and `dav.gate4.ai`
(control API + WebDAV). The host is substituted into both addresses (`https://<host>` for
the cabinet/`link`, `https://dav.<host>` for the control API/WebDAV). Two ways to change it:

- `--host`, saved to `config.json`, so every later launch (autostart, Start menu) keeps it:

  ```
  gate4ai-sync --host test.gate4.ai
  ```

  This is what the installer from a deployment passes — `curl -fsSL
  https://test.gate4.ai/install.sh | sh` sets up a client for the test stand. Switching to a
  different host drops the pairing, every folder's mount credential and its sync manifest,
  since all of them belong to the previous server; the folder selection stays. The tray
  tooltip names the host whenever it isn't `gate4.ai`.

- `GATE4AI_SYNC_HOST`, for one run only, **never saved** — handy in development:

  ```
  GATE4AI_SYNC_HOST=test.gate4.ai go run ./cmd/gate4ai-sync
  ```

  It takes priority over whatever `config.json` says.

## Building

```
go build ./cmd/gate4ai-sync
```

No CGO on any of the three platforms — `CGO_ENABLED=0 GOOS={linux,windows,darwin} go build ./...`
is checked in CI (`.github/workflows/ci.yml`).

Tests run on Linux, Windows and macOS runners, and `go vet` and golangci-lint are run once
per target from Linux. Platform code is otherwise only ever compiled, never checked: the
cross-lint job found unchecked errors and a bare `==` on an error in `internal/autostart`
the first time it ran. The Windows and macOS test jobs each mount a small volume with a
letter, name and label chosen in advance (`diskpart` and `hdiutil`), so
`internal/localpath`'s drive list — the thing that makes a folder on a second drive
reachable at all — is checked against a volume whose answer is known, rather than only
against "it did not crash".

### CI-built binaries and installers

- Every push to a branch other than `main`, and every pull request, runs
  [ci.yml](.github/workflows/ci.yml)'s `build-artifacts` job, which cross-builds the three
  plain binaries and attaches them to that workflow run's **Artifacts** — useful for a
  tester to grab a build without waiting for a release.
- Every push to `main` runs [release.yml](.github/workflows/release.yml): lint and test,
  auto-increment a `vX.Y.Z` tag (patch bump over the latest existing tag), build the
  installers described in [Installation](#installation) above (`packaging/nfpm`,
  `packaging/windows`, `packaging/macos` hold their configs), and publish everything to
  [Releases](../../releases).

## Layout

- `internal/config` — the client's single flat JSON settings file.
- `internal/webdavclient`, `internal/controlclient` — protocol clients (see `docs/sync-api.md`).
- `internal/syncengine` — three-way comparison of local/remote state and the manifest,
  last-write-wins on conflicts.
- `internal/loop` — the background loop: pairing → folder registration → sync.
- `internal/webui` — the local settings HTTP UI.
- `internal/trayapp`, `internal/autostart`, `internal/browser` — tray icon, autostart,
  opening the browser.
- `packaging/` — installer configs used by `release.yml` (nfpm for `.deb`, WiX for `.msi`,
  an `Info.plist` template for the macOS `.app`/`.dmg`).

## Status

The full cycle from issue #38 is implemented: pairing, folder registration, two-way sync,
tray icon, autostart. Not implemented (deliberately, see the issue): client-side file
preprocessing, conflict copies, real-time filesystem reaction (polling only).

## License

MIT — see [LICENSE](LICENSE).
