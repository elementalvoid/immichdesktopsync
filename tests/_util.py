"""Shared helpers for the headless Playwright e2e tests.

Imported by tests/test_e2e_w3.py and tests/test_e2e_upload.py (a script's own
directory is on sys.path when run via `uv run tests/....py`).
"""
import asyncio
import hashlib
import os
import socket
import sys
import time

# A tiny valid PNG (1x1 red pixel) - same bytes the mock server seeds with.
PNG_1PX = bytes.fromhex(
    "89504e470d0a1a0a0000000d4948445200000001000000010806000000"
    "1f15c4890000000d4944415478da63606060003030c402000000ffff03"
    "bc0d5c070000000049454e44ae426082"
)

SERVER_URL = "http://127.0.0.1:3823"
EMAIL = "test@example.com"
PASSWORD = "secret"


def sha256_bytes(b):
    return hashlib.sha256(b).hexdigest()


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


async def wait_http_ok(url, timeout_s=30.0):
    """Block until url answers <400, or exit with a hint."""
    import urllib.error
    import urllib.request
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as resp:
                if resp.status < 400:
                    return
        except (urllib.error.URLError, OSError):
            pass
        await asyncio.sleep(0.5)
    sys.exit("[test] %s never came up. Start it first:\n"
             "  ./scripts/run_server.sh --reset   # app server on :3823" % url)


async def preflight(p):
    """Ensure Chromium can actually launch: download the browser and, if its
    shared libraries are missing, install its system dependencies. Both are
    one-time steps, so this makes the test self-provisioning on fresh machines
    instead of failing with 'error while loading shared libraries'."""
    exe = p.chromium.executable_path
    ready = os.path.exists(exe)
    if ready and os.name == "posix":
        # Cheap detection of unresolved libs (libnspr4, libnss3, ...) without
        # having to parse playwright launch exceptions.
        ldd = await asyncio.create_subprocess_exec(
            "ldd", exe, stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.DEVNULL)
        out, _ = await ldd.communicate()
        ready = b"not found" not in out
    if ready:
        return
    print("[preflight] Chromium not ready (%s); installing browser + system deps..." % exe)
    for step in (["install", "chromium"], ["install-deps", "chromium"]):
        cmd = [sys.executable, "-m", "playwright", *step]
        if step[0] == "install-deps" and os.name == "posix" and os.geteuid() != 0:
            cmd = ["sudo"] + cmd  # apt needs root
        proc = await asyncio.create_subprocess_exec(*cmd)
        if (rc := await proc.wait()) != 0:
            sys.exit("[preflight] playwright %s failed (rc=%d). Run manually:\n"
                     "  uv run --with playwright playwright install chromium\n"
                     "  uv run --with playwright playwright install-deps chromium"
                     % (step[0], rc))
