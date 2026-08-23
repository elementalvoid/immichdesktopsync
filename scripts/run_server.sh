#!/usr/bin/env bash
# Headless wails3 (server-mode) run script — NO GTK window, NO Xvfb needed.
#
# This builds & runs the wails3-plus app as a pure HTTP server (`-tags server`),
# which is perfect for headless/browser-driven development:
#   - the Svelte frontend is served over plain HTTP (embedded dist OR vite dev)
#   - every frontend->Go call uses Wails3's HTTP transport (POST /wails/runtime)
#   - the headless browser (Playwright) drives the real UI end-to-end
#
# Usage:
#   ./run_server.sh            build + run server-mode (port 3823)
#   ./run_server.sh --mock     also start mock Immich server on 127.0.0.1:22841
#   ./run_server.sh --reset    clear persisted auth/config before starting
set -e

BASE="$(cd "$(dirname "$0")" && pwd)"
export HOME="$BASE/.home"
export GOPATH="$BASE/.go"
export GOCACHE="$BASE/.gocache"
export XDG_CACHE_HOME="$BASE/.cache"
export XDG_CONFIG_HOME="$BASE/.home/.config"
export XDG_DATA_HOME="$BASE/.home/.local/share"

MOCK=0
RESET=0
for a in "$@"; do
  [ "$a" = "--mock" ] && MOCK=1
  [ "$a" = "--reset" ] && RESET=1
done

if [ "$RESET" = "1" ]; then
  echo "[*] Clearing persisted auth config"
  rm -rf "$XDG_CONFIG_HOME/immich-desktop"
fi

if [ "$MOCK" = "1" ]; then
  echo "[*] Starting mock Immich server on 127.0.0.1:22841"
  "$BASE/../.pv/venv/bin/python" "$BASE/mock_immich.py" 22841 >"$BASE/.mock.log" 2>&1 &
  # Or: /root/immich-desktop/.pv/venv/bin/python (venv lives one level up from the repo)
  sleep 1
fi

# Build server-mode binary if missing
if ! [ -f "$BASE/bin/ImmichDesktopSync-server" ]; then
  echo "[*] Building server-mode binary (run: go build -tags server,dev)"
  go build -tags server,dev -buildvcs=false -gcflags=all="-l" -o "$BASE/bin/ImmichDesktopSync-server"
fi

echo "[*] Running wails3 server-mode binary on 127.0.0.1:3823"
unset DISPLAY
svc() {
  "$BASE/bin/ImmichDesktopSync-server"
}
svc