#!/usr/bin/env python3
# /// script
# dependencies = [
#   "playwright>=1.0",
# ]
# ///
"""End-to-end DOWNLOAD test on Wails3 server mode (headless browser, no GTK).

Seeds a large original on an ephemeral mock, then drives the real UI:
  login -> Settings (Downloads Folder) -> Gallery -> Lightbox ->
  Download original.

The assertion is byte-for-byte: sha256(downloaded file) == sha256(original on
the mock). This exercises the streamed GetOriginal -> io.Copy path (task 2b)
on a large asset. Memory flatness is proven by the Go unit tests; a browser
test cannot sample the app process heap.

Run:  ./scripts/run_server.sh --reset &          # app server on :3823
      uv run tests/test_e2e_download.py
"""
import asyncio
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

from playwright.async_api import async_playwright

from _util import SERVER_URL, EMAIL, PASSWORD, free_port, preflight, wait_http_ok

MOCK_PORT = free_port()
IMMICH_URL = "http://127.0.0.1:%d" % MOCK_PORT
LARGE_BYTES = 50 << 20
LOG = "/tmp/mock_immich_download_e2e.log"


def http_json(method, path, payload=None):
    req = urllib.request.Request(
        "http://127.0.0.1:%d%s" % (MOCK_PORT, path),
        data=json.dumps(payload).encode() if payload is not None else None,
        method=method,
        headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.load(resp)


def start_mock():
    here = os.path.dirname(os.path.abspath(__file__))
    logf = open(LOG, "w")
    proc = subprocess.Popen(
        [sys.executable, os.path.join(here, "mock_immich.py"), str(MOCK_PORT)],
        stdout=logf, stderr=subprocess.STDOUT)
    deadline = time.time() + 15
    while time.time() < deadline:
        try:
            http_json("GET", "/_test/uploads")
            return proc
        except Exception:
            if proc.poll() is not None:
                sys.exit("[test] mock immich died at startup; see " + LOG)
            time.sleep(0.2)
    sys.exit("[test] mock immich did not become ready")


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


async def wait_for(fn, timeout_s, what):
    deadline = time.time() + timeout_s
    last = None
    while time.time() < deadline:
        res = fn()
        if asyncio.iscoroutine(res):
            res = await res
        last = res
        if res:
            return res
        await asyncio.sleep(0.25)
    raise TimeoutError("timed out after %ss waiting for: %s (last=%r)"
                       % (timeout_s, what, last))


async def nav(page, label):
    await page.locator("aside").get_by_role("button").filter(has_text=label).click()


async def ensure_login(page):
    """Get to a fresh login form: sign out of any stale session first."""
    if await page.locator("#server").count() > 0:
        return
    print("   stale session detected -> signing out")
    await nav(page, "Settings")
    await page.get_by_role("button", name="Sign out").click()
    await wait_for(lambda: page.locator("#server").count(), 15,
                   "login form after sign-out")


async def main():
    await wait_http_ok(SERVER_URL)
    mock_proc = start_mock()
    downloads = tempfile.mkdtemp(prefix="download-e2e-")
    try:
        name = "large-%d.png" % os.getpid()
        seeded = http_json("POST", "/_test/seed_file",
                           {"name": name, "size": LARGE_BYTES, "only": True})
        print("== seeded %s: %d bytes, sha256 %s.."
              % (seeded["name"], seeded["size"], seeded["sha256"][:16]))

        async with async_playwright() as p:
            await preflight(p)
            browser = await p.chromium.launch(headless=True)
            page = await browser.new_page()
            console_errors = []
            page.on("console.error", lambda e: console_errors.append(str(e)))
            try:
                print("== 1. Load app ==")
                await page.goto(SERVER_URL, timeout=30000)
                await page.wait_for_load_state("domcontentloaded")
                await page.wait_for_timeout(2000)
                await ensure_login(page)

                print("== 2. Login against ephemeral mock (%s) ==" % IMMICH_URL)
                await page.locator("#server").fill(IMMICH_URL)
                await page.locator("#email").fill(EMAIL)
                await page.locator("#password").fill(PASSWORD)
                await page.locator("#password").press("Enter")
                # After a sign-out the app stays on Settings, so the Gallery is
                # not mounted until it is selected. Wait for the shell, then go.
                await wait_for(lambda: page.locator("aside").count(), 30,
                               "app shell after login")
                await nav(page, "Gallery")
                await wait_for(lambda: page.locator("img[alt=thumbnail]").count(),
                               30, "Gallery thumbnails after login")
                print("   LOGIN OK -> Gallery")

                print("== 3. Set downloads folder ==")
                await nav(page, "Settings")
                box = page.locator('input[placeholder^="Or paste a folder path"]').last
                await box.fill(downloads)
                await page.get_by_role("button", name="Set", exact=True).click()
                await wait_for(lambda: page.get_by_text(downloads).count(), 15,
                               "downloads folder to be saved")
                print("   downloads folder:", downloads)

                print("== 4. Open the seeded asset in the Lightbox ==")
                await nav(page, "Gallery")
                await page.locator("img[alt=thumbnail]").first.click()
                await wait_for(lambda: page.locator('button[title="Download original"]').count(),
                               15, "lightbox download button")
                print("   lightbox open")

                print("== 5. Download the original ==")
                before = set(os.listdir(downloads))
                await page.locator('button[title="Download original"]').click()
                await wait_for(lambda: page.get_by_text("Saved", exact=True).count(),
                               30, "Saved confirmation")

                # DownloadAsset resolves only after the file is closed, so a
                # new file is complete once "Saved" is on screen.
                def new_file():
                    fresh = set(os.listdir(downloads)) - before
                    return next(iter(fresh)) if fresh else None

                fname = await wait_for(new_file, 15, "downloaded file")
                path = os.path.join(downloads, fname)
                size = os.path.getsize(path)
                digest = sha256_file(path)
                print("   %s: %d bytes, sha256 %s.." % (fname, size, digest[:16]))

                if size != LARGE_BYTES or digest != seeded["sha256"]:
                    sys.exit("[test] FAIL: downloaded %d bytes sha256 %s, want %d bytes sha256 %s"
                             % (size, digest, LARGE_BYTES, seeded["sha256"]))
                if console_errors:
                    print("   JS console errors:", console_errors)
                print("\nDOWNLOAD E2E PASSED: %d bytes, byte-for-byte match" % LARGE_BYTES)
            finally:
                await browser.close()
    finally:
        mock_proc.terminate()
        try:
            mock_proc.wait(timeout=5)
        except Exception:
            mock_proc.kill()
        shutil.rmtree(downloads, ignore_errors=True)


if __name__ == "__main__":
    asyncio.run(main())
