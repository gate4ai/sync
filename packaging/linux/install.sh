#!/usr/bin/env sh
# One-line installer for gate4.ai sync on Debian/Ubuntu:
#
#   curl -fsSL https://raw.githubusercontent.com/gate4ai/sync/main/packaging/linux/install.sh | sh
#
# Finds the latest GitHub release, downloads the amd64 .deb, installs it
# with apt, and launches the app once so its tray icon appears. Only
# amd64 .deb builds are published today (see .github/workflows/release.yml);
# this script fails on anything else rather than silently doing nothing.

set -eu

REPO="gate4ai/sync"

fail() {
	echo "error: $1" >&2
	exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v dpkg >/dev/null 2>&1 || fail "this installer is for Debian/Ubuntu (dpkg not found)"

arch="$(dpkg --print-architecture)"
[ "$arch" = "amd64" ] || fail "unsupported architecture '$arch' (only amd64 .deb builds are published)"

echo "Looking up the latest release..."
release_json="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest")"

deb_url="$(printf '%s' "$release_json" | grep -o '"browser_download_url": *"[^"]*-amd64\.deb"' | head -n1 | sed -e 's/^"browser_download_url": *"//' -e 's/"$//')"
[ -n "$deb_url" ] || fail "could not find an amd64 .deb asset in the latest release"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

deb_path="$tmp_dir/$(basename "$deb_url")"
echo "Downloading $(basename "$deb_url")..."
curl -fsSL -o "$deb_path" "$deb_url"

echo "Installing (sudo apt install)..."
sudo apt install -y "$deb_path"

echo "Starting gate4ai-sync..."
setsid gate4ai-sync >/dev/null 2>&1 &
disown

echo "Done. Look for the gate4.ai sync tray icon to finish setup."
