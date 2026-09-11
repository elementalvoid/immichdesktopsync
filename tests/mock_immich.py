#!/usr/bin/env python3
# /// script
# dependencies = []
# ///
"""Mock Immich server - implements the REST endpoints that immichdesktopsync's
Go client calls, so the full go<->js flow can be tested headlessly.

Endpoints:
  POST /api/auth/login            -> accessToken + user
  GET  /api/users/me              -> user
  GET  /api/server-info/version   -> {major,minor,patch}
  POST /api/search/metadata       -> paginated assets
  GET  /api/albums                -> album list
  GET  /api/albums/:id            -> album assets
  POST /api/assets/check          -> which deviceAssetIds already exist
  POST /api/assets                -> multipart upload, returns {id}
  GET  /api/assets/:id            -> asset info
  GET  /api/assets/:id/thumbnail?size=  -> image bytes
  GET  /api/assets/:id/original   -> file bytes

Test-control endpoints (no auth, used by tests/test_e2e_upload.py):
  GET  /_test/uploads             -> JSON list of captured upload records,
                                     each including sha256 + storedPath of the
                                     bytes written to disk (integrity checks)
  POST /_test/reset               -> clear uploads, restore seeded state
  POST /_test/fail_next           -> body {"count": n}: next n uploads fail 500

Uploaded files are written verbatim to a fresh temp directory on disk so tests
can hash-compare source vs received (byte-for-byte integrity).

It is stdlib-only, so it runs under bare uv run too (no third-party install).
"""
import atexit, hashlib, json, os, re, signal, shutil, sys, tempfile, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 22841
TOKEN = "mock-immich-token-abc123"
EMAIL = "test@example.com"
PASSWORD = "secret"

# A tiny valid PNG (1x1 red pixel)
PNG = bytes.fromhex(
    "89504e470d0a1a0a0000000d4948445200000001000000010806000000"
    "1f15c4890000000d4944415478da63606060003030c402000000ffff03"
    "bc0d5c070000000049454e44ae426082"
)



def _seed_assets():
    return [
        {
            "id": "asset-" + str(i + 1),
            "type": "IMAGE",
            "originalFileName": "photo-" + str(i + 1) + ".png",
            "originalPath": "library/photo-" + str(i + 1) + ".png",
            "checksum": hashlib.md5(("photo-" + str(i + 1)).encode()).hexdigest(),
            "fileCreatedAt": "2024-01-01T12:00:00.000Z",
            "fileModifiedAt": "2024-01-01T12:05:00.000Z",
            "localDateTime": "2024-01-01T12:00:00.000Z",
            "duration": "0:00:00.000000",
            "isFavorite": False,
            "createdAt": "2024-01-01T12:00:00.000Z",
            "updatedAt": "2024-01-01T12:00:00.000Z",
            "exifInfo": {
                "fileSizeInByte": 70,
                "exifImageWidth": 1,
                "exifImageHeight": 1,
                "model": "MockCam",
            },
        }
        for i in range(3)
    ]


# ---- mutable state (LOCK-guarded; server is threaded) ----------------------
LOCK = threading.Lock()
STATE = {
    "assets": _seed_assets(),
    "uploaded": {},   # assetId -> upload record
    "fail_next": 0,   # injected 500s remaining for POST /api/assets
}
UPLOAD_DIR = tempfile.mkdtemp(prefix="mock-immich-uploads-")

ALBUMS = [{
    "id": "album-1",
    "albumName": "Trip",
    "description": "mock album",
    "albumThumbnailAssetId": "asset-1",
    "assetCount": 2,
}]


def reset_state():
    """POST /_test/reset: back to pristine seed state."""
    shutil.rmtree(UPLOAD_DIR, ignore_errors=True)
    os.makedirs(UPLOAD_DIR, exist_ok=True)
    STATE["assets"] = _seed_assets()
    STATE["uploaded"] = {}
    STATE["fail_next"] = 0


def asset_index(asset_id):
    for idx, a in enumerate(STATE["assets"]):
        if a["id"] == asset_id:
            return idx
    return -1


def parse_multipart(raw, boundary):
    """Return [(name, filename, body_bytes)] for every multipart part."""
    delim = b"--" + boundary
    crlf = b"\r\n"
    out = []
    for chunk in raw.split(delim)[1:]:
        if chunk.startswith(b"--"):          # closing marker
            break
        if not chunk.startswith(crlf):
            continue
        chunk = chunk[2:]                    # strip CRLF after boundary line
        head, sep, body = chunk.partition(crlf + crlf)
        if not sep:
            continue
        if body.endswith(crlf):
            body = body[:-2]
        name = filename = None
        for line in head.split(crlf):
            text = line.decode("utf-8", errors="replace")
            m = re.search(r'name="([^"]*)"', text)
            if m:
                name = m.group(1)
            m = re.search(r'filename="([^"]*)"', text)
            if m:
                filename = m.group(1)
        if name:
            out.append((name, filename, body))
    return out


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _bytes(self, b, ctype, code=200):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def _auth(self):
        return self.headers.get("Authorization", "") == "Bearer " + TOKEN

    def log_message(self, fmt, *args):
        print("[mock-immich] %s %s -> %s" % (self.command, self.path, fmt % args),
              flush=True)

    # ------------------------------------------------------------ control ---
    def _control_post(self):
        """Handle /_test/* POST endpoints; True when consumed."""
        if self.path == "/_test/reset":
            with LOCK:
                reset_state()
            self._json({"ok": True})
            return True
        if self.path == "/_test/fail_next":
            try:
                body = json.loads(self._raw or b"{}")
            except ValueError:
                body = {}
            with LOCK:
                STATE["fail_next"] = int(body.get("count", 1))
            self._json({"ok": True, "fail_next": STATE["fail_next"]})
            return True
        return False

    def _control_get(self):
        """Handle /_test/* GET endpoints; True when consumed."""
        if self.path == "/_test/uploads":
            with LOCK:
                recs = list(STATE["uploaded"].values())
            self._json({"uploadDir": UPLOAD_DIR, "uploads": recs})
            return True
        return False

    # ------------------------------------------------------------ uploads ---
    def handle_upload(self, raw):
        ctype = self.headers.get("Content-Type", "")
        boundary = ctype.split("boundary=")[-1].encode()
        parts = parse_multipart(raw, boundary)

        fields = {}
        file_body = b""
        file_name = "upload.bin"
        for name, filename, body in parts:
            if name == "assetData":
                file_body = body
                file_name = filename or file_name
            else:
                fields[name] = body.decode("utf-8", errors="replace").strip()

        size = len(file_body)
        sha256 = hashlib.sha256(file_body).hexdigest()

        # Injected-failure mode (retry testing): burn one 500, store nothing.
        with LOCK:
            if STATE["fail_next"] > 0:
                STATE["fail_next"] -= 1
                print("[mock-immich] INJECTED FAILURE (%s, %d bytes, remaining=%d)"
                      % (file_name, size, STATE["fail_next"]), flush=True)
                self._json({"message": "injected failure"}, 500)
                return

            ext = os.path.splitext(file_name)[1] or ".bin"
            asset_id = "asset-%d" % (len(STATE["uploaded"]) + 99)
            stored_path = os.path.join(UPLOAD_DIR, asset_id + ext)
            # Byte-for-byte durable copy - the integrity-check artifact.
            with open(stored_path, "wb") as fh:
                fh.write(file_body)

            record = {
                "assetId": asset_id,
                "deviceAssetId": fields.get("deviceAssetId", ""),
                "deviceId": fields.get("deviceId", ""),
                "originalFileName": file_name,
                "fileSize": size,
                "sha256": sha256,
                "storedPath": stored_path,
                "contentType": ctype,
            }
            STATE["uploaded"][asset_id] = record
            STATE["assets"].append({
                "id": asset_id,
                "type": "VIDEO" if ext == ".mp4" else "IMAGE",
                "originalFileName": file_name,
                "originalPath": "library/" + asset_id + ext,
                "checksum": sha256[:32],
                "fileCreatedAt": fields.get("fileCreatedAt", "2024-02-01T00:00:00.000Z"),
                "fileModifiedAt": fields.get("fileModifiedAt", "2024-02-01T00:00:00.000Z"),
                "localDateTime": "2024-02-01T00:00:00.000Z",
                "duration": "0:00:00.000000",
                "isFavorite": fields.get("isFavorite") == "true",
                "createdAt": "2024-02-01T00:00:00.000Z",
                "updatedAt": "2024-02-01T00:00:00.000Z",
                "exifInfo": {"fileSizeInByte": size, "model": "MockUpload"},
            })

        print("[mock-immich] UPLOAD asset=%s file=%s size=%d sha256=%s path=%s"
              % (asset_id, file_name, size, sha256, stored_path), flush=True)
        self._json({"id": asset_id})

    # -------------------------------------------------------------- verbs ---
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        self._raw = self.rfile.read(length) if length else b""

        if self.path.startswith("/_test/"):
            if self._control_post():
                return
            self._json({"message": "unknown control " + self.path}, 404)
            return

        if self.path == "/api/auth/login":
            # allow unauthenticated login
            body = json.loads(self._raw)
            if body.get("password") != PASSWORD:
                self._json({"message": "Invalid password"}, 401)
                return
            self._json({"accessToken": TOKEN, "userId": "u-1",
                        "userEmail": body.get("email"), "name": "Test User"})
            return

        if not self._auth():
            self._json({"message": "Not authenticated"}, 401)
            return

        if self.path == "/api/search/metadata":
            with LOCK:
                items = list(STATE["assets"])
            self._json({"assets": {"total": len(items), "count": len(items),
                                   "page": 1, "pages": 1, "hasNext": False,
                                   "items": items}})
        elif self.path == "/api/assets/check":
            req = json.loads(self._raw)
            results = []
            for a in req.get("assets", []):
                # every deviceAssetId is considered "new" (so we can upload)
                results.append({"id": a.get("id"), "action": "accept"})
            self._json({"results": results})
        elif self.path == "/api/assets" and                 self.headers.get("Content-Type", "").startswith("multipart"):
            self.handle_upload(self._raw)
        else:
            self._json({"message": "unknown POST " + self.path}, 404)

    def do_GET(self):
        if self.path.startswith("/_test/"):
            if self._control_get():
                return
            self._json({"message": "unknown control " + self.path}, 404)
            return

        # server version needs no auth in real immich; allow always
        if self.path != "/api/server-info/version" and not self._auth():
            self._json({"message": "Not authenticated"}, 401)
            return

        if self.path == "/api/server-info/version":
            self._json({"major": 1, "minor": 120, "patch": 0})
        elif self.path == "/api/users/me":
            self._json({"id": "u-1", "email": EMAIL, "name": "Test User"})
        elif self.path == "/api/albums":
            self._json(list(ALBUMS))
        elif m := re.match(r"^/api/albums/(.+)$", self.path):
            aid = next((a["id"] for a in ALBUMS if a["id"] == m.group(1)), None)
            if aid is None:
                self._json({"message": "not found"}, 404)
            else:
                album_assets = STATE["assets"][:1]
                self._json({"assets": {"items": album_assets,
                                       "total": len(album_assets),
                                       "count": len(album_assets),
                                       "hasNext": False, "pages": 1, "page": 1}})
        elif m := re.match(r"^/api/assets/(.+)/thumbnail", self.path):
            self._bytes(PNG, "image/png")
        elif m := re.match(r"^/api/assets/(.+)/original", self.path):
            rec = STATE["uploaded"].get(m.group(1))
            if rec and os.path.exists(rec["storedPath"]):
                # Serve exactly what was received - integrity round trip.
                with open(rec["storedPath"], "rb") as fh:
                    self._bytes(fh.read(), "application/octet-stream")
            else:
                self._bytes(PNG, "image/png")
        elif m := re.match(r"^/api/assets/(.+)$", self.path):
            idx = asset_index(m.group(1))
            if idx >= 0:
                self._json(STATE["assets"][idx])
            elif m.group(1) in STATE["uploaded"]:
                self._json({"id": m.group(1), "type": "IMAGE",
                            "originalFileName": "upload.png"})
            else:
                self._json({"message": "asset not found: " + m.group(1)}, 404)
        else:
            self._json({"error": "unknown GET " + self.path}, 404)


def _cleanup(*_args):
    """Remove the upload dir so repeated test runs don't leak temp dirs.

    test_e2e_upload.py terminates this process with SIGTERM, so hook both the
    signal and normal exit; the stored files are only needed for the duration
    of a run (the test reads them for integrity checks before finishing)."""
    shutil.rmtree(UPLOAD_DIR, ignore_errors=True)
    sys.exit(0)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, _cleanup)
    signal.signal(signal.SIGINT, _cleanup)
    atexit.register(lambda: shutil.rmtree(UPLOAD_DIR, ignore_errors=True))
    srv = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print("Mock Immich server listening on http://127.0.0.1:%d (uploads -> %s)"
          % (PORT, UPLOAD_DIR), flush=True)
    srv.serve_forever()
