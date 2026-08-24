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

Tooling paths used here. `$BASE` below is the repository root (where this
README lives):

```
Wails3 CLI: $BASE/.tools/wails3   (stable copy of `go install .../wails3@v3.0.0-beta.9`;
            scripts/run_server.sh prepends this dir to PATH)
```

Go and npm keep their default caches (`~/go`, `~/.cache/go-build`, `~/.npm`);
set `GOPATH`/`GOCACHE`/`npm_config_cache` yourself if you prefer them in-repo.

## Build + run (server mode) — idiomatic wails3

Use the **wails3 CLI + project Taskfile** rather than hand-rolled `go build`.
The `wails3-plus` Taskfile ships ready-made server tasks.

**Stable / scripting path** — embedded frontend, no live vite needed:

```bash
# From the repository root (cd "$(git rev-parse --show-toplevel)"):
wails3 task common:build:server DEV=true     # builds bin/ImmichDesktopSync-server
wails3 task common:run:server DEV=true       # builds (if needed) + runs it
```

**Hot-reload dev path** — pulls up the Vite dev server on :9245 and the app
HTTP server on :3823 (`EXTRA_TAGS=server` flows into the dev build's `-tags`):

```bash
EXTRA_TAGS=server wails3 dev
```

The app listens on `127.0.0.1:3823` (`WAILS_SERVER_PORT` overrides). For a
wrapper that handles env + optional mock server + reset, use
`./scripts/run_server.sh [--mock] [--reset] [--dev]`.

> **Toolchain env tip:** `wails3 dev`/`task` fork `go`/`node`/`npm`
> subprocesses, so those must resolve from your ambient PATH. The wrapper only
> prepends `$BASE/.tools` — the project Taskfile needs it there because it
> invokes `wails3 generate bindings` by name. Beware mise-style shims that try
> to re-resolve `latest` over the network (put the real binary dir first if
> that bites you).

---

## Running the browser tests with uv (preferred)

Each test carries a **PEP 723 inline dependency header**, so `uv run` creates a
managed env and installs `playwright` automatically — **no uv cache env var
needed** (uv uses its default cache location under your home dir).

```bash
# From the repository root:
uv run tests/test_server_smoke.py
uv run tests/test_e2e_w3.py
```

**Browser setup is automatic.** Each test runs a preflight that downloads the
headless Chromium and, if its shared libraries are missing (`libnspr4`,
`libnss3`, …), installs the browser's system dependencies on first run
(`playwright install` / `playwright install-deps`; the latter needs root or
passwordless sudo). To do either step manually instead:

```bash
uv run --with playwright playwright install chromium
uv run --with playwright playwright install-deps chromium
```

Playwright stores it in its default location (`~/.cache/ms-playwright`), so no
`PLAYWRIGHT_BROWSERS_PATH` override is needed either — both uv and playwright
manage their own caches.

> This is a change from requiring hand-maintained venv/cache dirs: you do not
> need to create `.pv`, `.uv-cache`, `.pw-browsers`, or point any
> `UV_CACHE_DIR`/`PLAYWRIGHT_BROWSERS_PATH` at them.

`test_e2e_w3.py` proves the complete chain: the headless browser submits the
login form, `auth.login()` calls the generated binding
(`frontend/bindings/.../app.ts`), which POSTs to `/wails/runtime`, Wails3
dispatches to `App.Login`, the mock Immich `POST /api/auth/login` succeeds,
and the app flips to the Gallery where `GetAssets` + `GetThumbnail`
round-trip again. Then thumbnails render.

## Notes

- Backend config / SQLite persist under `$HOME/.config/immich-desktop/`
  (`backend/config.go` resolves it via `os.UserHomeDir()`).
  Delete that dir (`scripts/run_server.sh --reset`) to get a clean login flow.
- The `wails3 dev` (desktop/webview) path still needs a real or virtual X
  display (GTK4), and crashes headless — so prefer **server mode** here.
- Mock Immich server: `tests/mock_immich.py` (login, version, search/metadata,
  albums, thumbnails, originals, upload). It is stdlib-only, so it runs under
  bare `uv run` too (no third-party install).