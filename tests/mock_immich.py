#!/usr/bin/env python3
"""Mock Immich server — implements the REST endpoints that immichdesktopsync's
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
"""
import io, json, os, re, sys, uuid, hashlib
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

ASSETS = []
for i in range(3):
    ASSETS.append({
        "id": f"asset-{i+1}",
        "type": "IMAGE",
        "originalFileName": f"photo-{i+1}.png",
        "originalPath": f"library/photo-{i+1}.png",
        "checksum": hashlib.md5(f"photo-{i+1}".encode()).hexdigest(),
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
    })

ALBUMS = [{
    "id": "album-1",
    "albumName": "Trip",
    "description": "mock album",
    "albumThumbnailAssetId": "asset-1",
    "assetCount": 2,
}]

UPLOADED = {}  # assetId -> bytes (for originals)

MIME_TYPES = {".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
              ".gif": "image/gif", ".webp": "image/webp", ".heic": "image/heic",
              ".mp4": "video/mp4"}

def asset_index(asset_id):
    for idx, a in enumerate(ASSETS):
        if a["id"] == asset_id:
            return idx
    return -1


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
        return self.headers.get("Authorization", "") == f"Bearer {TOKEN}"

    def log_message(self, fmt, *args):
        print(f"[mock-immich] {self.command} {self.path} -> {fmt % args}", flush=True)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length) if length else b""

        if self.path == "/api/auth/login":
            # allow unauthenticated login
            body = json.loads(raw)
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
            self._json({"assets": {"total": len(ASSETS), "count": len(ASSETS),
                                   "page": 1, "pages": 1, "hasNext": False,
                                   "items": ASSETS}})
        elif self.path == "/api/assets/check":
            req = json.loads(raw)
            results = []
            for a in req.get("assets", []):
                # every deviceAssetId is considered "new" (so we can upload)
                results.append({"id": a.get("id"), "action": "accept"})
            self._json({"results": results})
        elif re.match(r"^/api/assets$", self.path) and self.headers.get("Content-Type", "").startswith("multipart"):
            # parse multipart upload
            boundary = self.headers["Content-Type"].split("boundary=")[-1].encode()
            parts = raw.split(b"--" + boundary)
            device_asset_id = None
            ext = ".png"
            for part in parts:
                if b"deviceAssetId" in part and b'name="deviceAssetId"' in part:
                    m = re.search(rb'\r\n\r\n(.*?)\r\n', part, re.S)
                    if m:
                        device_asset_id = m.group(1).decode()
                        ext = "." + device_asset_id.split(".")[-1].split("-")[0]
            asset_id = f"asset-{len(UPLOADED)+99}"
            UPLOADED[asset_id] = ext
            ASSETS.append({
                "id": asset_id, "type": "IMAGE" if ext != ".mp4" else "VIDEO",
                "originalFileName": device_asset_id or "upload.png",
                "originalPath": f"library/{asset_id}{ext}",
                "checksum": "mock", "fileCreatedAt": "2024-02-01T00:00:00.000Z",
                "fileModifiedAt": "2024-02-01T00:00:00.000Z",
                "localDateTime": "2024-02-01T00:00:00.000Z", "duration": "0:00:00.000000",
                "isFavorite": False, "createdAt": "2024-02-01T00:00:00.000Z",
                "updatedAt": "2024-02-01T00:00:00.000Z",
            })
            self._json({"id": asset_id})
        else:
            self._json({"message": f"unknown POST {self.path}"}, 404)

    def do_GET(self):
        if not self._auth() and self.path != "/api/server-info/version":
            self._json({"message": "Not authenticated"}, 401)
            return

        # server version needs no auth in real immich; allow always
        if self.path == "/api/server-info/version":
            self._json({"major": 1, "minor": 120, "patch": 0})
        elif self.path == "/api/users/me":
            self._json({"id": "u-1", "email": "test@example.com", "name": "Test User"})
        elif self.path == "/api/albums":
            body = []
            for a in globals().get("ALBUMS", []):
                body.append(a)
            self._json(body)
        elif m := re.match(r"^/api/albums/(.+)$", self.path):
            album_id = m.group(1)
            aid = next((a["id"] for a in (globals().get("ALBUMS") or []) if a["id"] == album_id), None)
            if aid is None:
                self._json({"message": "not found"}, 404)
            else:
                # return the first asset as the album's single asset
                album_assets = ASSETS[:1]
                # wrap in ApiResponse shape
                self._json({"assets": {"items": album_assets, "total": len(album_assets), "count": len(album_assets), "hasNext": False, "pages": 1, "page": 1}})
        elif m := re.match(r"^/api/assets/(.+)/thumbnail", self.path):
            asset_id = m.group(1)
            # always return the PNG thumbnail
            self._bytes(PNG, "image/png")
        elif m := re.match(r"^/api/assets/(.+)/original", self.path):
            asset_id = m.group(1)
            self._file(PNG, "image/png")
        elif m := re.match(r"^/api/assets/(.+)$", self.path):
            asset_id = m.group(1)
            idx = asset_index(asset_id)
            if idx < 0 and asset_id in UPLOADED:
                self._json({"id": asset_id, "type": "IMAGE", "originalFileName": "upload.png"})
            elif idx >= 0:
                self._json(ASSETS[idx])
            else:
                self._json({"message": "asset not found: "+asset_id}, 404)
        else:
            self._json({"error": f"unknown GET {self.path}"}, 404)

    def _file(self, data, ctype):
        self._ntp_bytes(data, ctype)

    def _ntp_bytes(self, b, ctype):
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)


if __name__ == "__main__":
    srv = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print(f"Mock Immich server listening on http://127.0.0.1:{PORT}", flush=True)
    srv.serve_forever()