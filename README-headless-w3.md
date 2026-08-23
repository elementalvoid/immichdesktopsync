# Running the wails3-plus app headless (browser-only, no GUI)

This documents how to run the **Wails 3** version of Immich Desktop Sync in a
**headless** environment — no physical display and, crucially, **no GTK window
and no Xvfb**. We use Wails 3's first-class **server mode** (`-tags server`),
which runs the entire app as a pure HTTP server. The full **go ↔ JavaScript**
stack is driven from a headless browser (Playwright) through Wails3's HTTP
IPC transport.

This matches the upgrade goals of the `wails3-plus` branch:
- newer GTK (WebKitGTK 6.0 / GTK 4) on desktop
- better performance (Wails3 server/HTTP transport + modern bindings via
  `@wailsio/runtime`)
- native system tray (Wails3 `app.SystemTray`) instead of an external lib

## How it runs — server mode

Wails 3 ships a "server" build tag that swaps the GUI platform backend for a
pure `http.Server` implementation (`pkg/application/application_server.go`).
All Window / SystemTray / Menu / Desktop calls become safe no-ops. The service
(`App.RegisterService`) runs exactly as on desktop — only the window/tray are
dropped.

The HTTP server:
- serves the built Svelte frontend (`frontend/dist`) at `/`
- exposes `/wails/runtime` → which the `@wailsio/runtime` JS library POSTs
  every `window.go`-style bindings call to (ID-based `$Call.ByID`)
- exposes `/wails/events` (WebSocket) for backend→frontend events
- exposes `/wails/stream/ws` (WebSocket) for streams
- serves a JSON `/health` endpoint

So the full round trip is:
**browser JS → @wailsio/runtime → POST /wails/runtime → Wails3 dispatcher →
Go bound method → backend (Immich HTTP, SQLite) → response back → page.**

## One-time setup (system deps)

Wails3 CLI and the app must be compiled once. For the Linux desktop build
you'd need GTK4/webkitgtk-6.0, but **server mode does not need them** at
runtime. The CLI binary itself does pull GTK C headers at compile time, so:

```bash
apt install -y build-essential gcc pkg-config libgtk-4-dev \
  libwebkitgtk-6.0-dev libjavascriptcoregtk-6.0-dev
```

(The older wails2 deps webkit2gtk-4.1/gtk-3 are also fine to have installed;
wails3 detects either. GTK3/4 are both supported at runtime.)

Tooling in the repo (kept local, no global write access). `$BASE` below is the
repository root (where this README lives):

```
GOMODULE: $BASE/.go   (GOPATH)
GOBIN:    $BASE/.home/.../bin
BUILD:    $BASE/.gocache
Wails3 CLI: $BASE/.tools/wails3   (stable copy of `go install .../wails3@latest`)
```

## Build + run (server mode)

```bash
# From the repository root:
cd "$(git rev-parse --show-toplevel)"   # or: cd path/to/immichdesktopsync

# 1) compile the server-mode binary (do once):
go build -tags server,dev -buildvcs=false -gcflags=all="-l" -o bin/ImmichDesktopSync-server

# 2) run it (no DISPLAY, no GTK):
unset DISPLAY
./bin/ImmichDesktopSync-server
#    logs: "Server mode starting address=127.0.0.1:3823"

# or use the helper which also optionally starts the mock Immich server:
./scripts/run_server.sh --mock --reset
```

The server listens on `127.0.0.1:3823` (override with `WAILS_SERVER_PORT`).

---

## Running the browser tests with uv (preferred)

Each test carries a **PEP 723 inline dependency header**, so `uv run` creates a
managed env and installs `playwright` automatically. Set a workspace-local uv
cache and point at the shared headless-browser binary:

```bash
# From the repository root:
uv run tests/test_server_smoke.py
uv run tests/test_e2e_w3.py
```

The sandbox has no global write access outside the workspace, so these env vars
point at writable sibling dirs. They are environment-specific: adjust the
absolute paths to wherever your uv cache and playwright browsers live, keeping
`$BASE` as the repo root for everything in-repo:

```
UV_CACHE_DIR=$BASE/../.uv-cache
XDG_CACHE_HOME=$BASE/../.cache
PLAYWRIGHT_BROWSERS_PATH=$BASE/../.pw-browsers
```

`test_e2e_w3.py` proves the complete chain: the headless browser submits the
login form, `auth.login()` calls the generated binding
(`frontend/bindings/.../app.ts`), which POSTs to `/wails/runtime`, Wails3
dispatches to `App.Login`, the mock Immich `POST /api/auth/login` succeeds,
and the app flips to the Gallery where `GetAssets` + `GetThumbnail`
round-trip again. Then thumbnails render.

## Notes

- Backend config / SQLite persist under `$BASE/.home/.config/immich-desktop/`.
  Delete that dir (`scripts/run_server.sh --reset`) to get a clean login flow.
- The `wails3 dev` (desktop/webview) path still needs a real or virtual X
  display (GTK4), and crashes headless — so prefer **server mode** here.
- Mock Immich server: `tests/mock_immich.py` (login, version, search/metadata,
  albums, thumbnails, originals, upload).