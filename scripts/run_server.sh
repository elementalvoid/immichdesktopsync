#!/usr/bin/env bash
# Headless wails3 (server-mode) run script — idiomatic wails3, NO GTK/Xvfb.
#
# Uses the wails3 CLI + project Taskfile (the idiomatic path) to build & run the
# app in server mode (`-tags server`). Server mode serves the Svelte frontend
# over plain HTTP and routes every frontend->Go call through Wails3's HTTP
# transport (POST /wails/runtime) — ideal for headless/browser-driven dev.
#
#   Run via:  wails3 task common:run:server DEV=true
#   (builds + runs bin/ImmichDesktopSync-server from the embedded dist)
#
# Usage:
#   ./run_server.sh            build + run server-mode (port 3823)
#   ./run_server.sh --mock     also start mock Immich server on 127.0.0.1:22841
#   ./run_server.sh --reset    clear persisted auth/config before starting
#   ./run_server.sh --dev      use `wails3 dev` with EXTRA_TAGS=server (hot-reload)
set -e

# Repo root: this script lives in <root>/scripts, so its parent is the root.
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BASE="$(cd "$SCRIPT_DIR/.." && pwd)"

# Toolchain env for the wails3 CLI + nested go/node subprocesses.
# Only what functionally matters: the project Taskfile invokes `wails3`
# (generate:bindings), so $BASE/.tools must be on PATH. Everything else
# (go/node/npm/uv) is expected to resolve from the ambient environment.
# WAILS3 is script-local; nothing else consumes it.
export PATH="$BASE/.tools:$PATH"
WAILS3="$BASE/.tools/wails3"

MOCK=0
RESET=0
DEV=0
MOCK_PORT=22841
for a in "$@"; do
  [ "$a" = "--mock" ] && MOCK=1
  [ "$a" = "--reset" ] && RESET=1
  [ "$a" = "--dev" ] && DEV=1
done

if [ "$RESET" = "1" ]; then
  echo "[*] Clearing persisted auth config ($HOME/.config/immich-desktop)"
  # backend/config.go uses os.UserHomeDir(), so state lives under $HOME,
  # not XDG_CONFIG_HOME.
  rm -rf "$HOME/.config/immich-desktop"
fi

if [ "$MOCK" = "1" ]; then
  echo "[*] Starting mock Immich server on 127.0.0.1:$MOCK_PORT (uv run)"
  # Use uv with the script's PEP 723 header (stdlib-only) — no venv to manage.
  uv run tests/mock_immich.py "$MOCK_PORT" >"$BASE/.mock.log" 2>&1 &
  sleep 1
fi

if [ "$DEV" = "1" ]; then
  echo "[*] wails3 dev with EXTRA_TAGS=server (vite hot-reload on :9245, app on :3823)"
  export EXTRA_TAGS="server"
  unset DISPLAY
  "$WAILS3" dev
else
  echo "[*] wails3 task common:run:server DEV=true"
  unset DISPLAY
  "$WAILS3" task common:run:server DEV=true
fi
