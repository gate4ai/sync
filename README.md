# gate4ai/sync

## Installation

Grab the latest release from this repo's [Releases page](../../releases). Each release
publishes a native installer for every platform below, built by
[release.yml](.github/workflows/release.yml).

### macOS

1. Download `gate4ai-sync-<version>-universal.dmg` (one build for Apple Silicon and Intel).
2. Open it and drag **gate4.ai sync** into **Applications**.
3. Launch it from Applications. The app is not notarized/signed with an Apple Developer ID,
   so Gatekeeper will refuse the first launch ("cannot be opened because the developer
   cannot be verified") — right-click the app, choose **Open**, then confirm in the dialog.
   This is only needed once.
4. A tray icon appears; use its "Open settings" menu item to configure sync.

### Windows

1. Download `gate4ai-sync-<version>-amd64.msi`.
2. Run it. The installer is not code-signed, so SmartScreen may show "Windows protected your
   PC" — click **More info** → **Run anyway**.
3. It installs to `Program Files\gate4ai-sync` and adds a Start Menu shortcut.
4. Launch **gate4.ai sync** from the Start Menu; a tray icon appears.

### Ubuntu / Debian

1. Download `gate4ai-sync-<version>-amd64.deb`.
2. Install it:
   ```
   sudo apt install ./gate4ai-sync-<version>-amd64.deb
   ```
3. Launch **gate4.ai sync** from your application menu, or run `gate4ai-sync` from a
   terminal.

All three installers only place the binary — autostart on login is opt-in from the app's
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
2. Clicking it opens a local settings page in the browser (`http://127.0.0.1:<port>/`) — the
   client has no GUI of its own, all configuration happens through the browser.
3. There you pick local folders to sync (a plain HTML picker over `os.ReadDir`, no native
   dialogs) and press "Connect to gate4.ai" — this opens
   `https://gate4.ai/link?client=<id>`, where the installation gets attached to the
   account's vault.
4. Once linked, the client registers each selected folder with the server on its own, gets a
   WebDAV credential per folder, and starts periodic two-way sync.
5. Allowed file types, the maximum file size and the poll interval are set in the cabinet on
   the server, not in this client.

## Which server it talks to

By default the client talks to production — `gate4.ai` (cabinet) and `dav.gate4.ai`
(control API + WebDAV). To point it at the test stand instead, set this before launch:

```
GATE4AI_SYNC_HOST=test.gate4.ai go run ./cmd/gate4ai-sync
```

The value is substituted into both addresses (`https://<host>` for the cabinet/`link`,
`https://dav.<host>` for the control API/WebDAV) and is **never saved** to `config.json` —
it is a launch-time switch, not a persistent setting. Without it, `gate4.ai` is used.

## Building

```
go build ./cmd/gate4ai-sync
```

No CGO on any of the three platforms — `CGO_ENABLED=0 GOOS={linux,windows,darwin} go build ./...`
is checked in CI (`.github/workflows/ci.yml`).

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
