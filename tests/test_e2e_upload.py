#!/usr/bin/env python3
# /// script
# dependencies = [
#   "playwright>=1.0",
# ]
# ///
"""End-to-end UPLOAD test on Wails3 server mode (headless browser, no GTK).

Drives the real Svelte UI: Settings -> paste a watch-folder path -> backend
scan -> SQLite queue -> UploadQueue worker -> multipart upload -> mock Immich.
Verifies:
  - every sample file arrives exactly once and the queue flips to done
  - INTEGRITY: sha256(source file) == sha256(mock record) == sha256(bytes
    the mock stored on disk) -- byte-for-byte, never size-only comparisons
  - injected server failures mark items failed; Retry All recovers them
  - re-adding a watched folder does not duplicate uploads

Run:  ./scripts/run_server.sh &          # app server on :3823
      uv run tests/test_e2e_upload.py
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

from _util import (SERVER_URL, EMAIL, PASSWORD, PNG_1PX, preflight,
                   wait_http_ok, free_port, sha256_bytes)

MOCK_PORT = free_port()
IMMICH_URL = "http://127.0.0.1:%d" % MOCK_PORT
UPLOAD_TIMEOUT_S = 120


def http_json(method, path, payload=None):
    req = urllib.request.Request(
        "http://127.0.0.1:%d%s" % (MOCK_PORT, path),
        data=json.dumps(payload).encode() if payload is not None else None,
        method=method,
        headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as resp:
        return json.load(resp)


def make_samples():
    """Temp watch folder holding 3 image files of distinct content."""
    d = tempfile.mkdtemp(prefix="upload-e2e-watch-")
    blob = b""
    seed = b"upload-e2e-seed"
    while len(blob) < 64 * 1024:
        seed = hashlib.sha256(seed).digest()
        blob += seed
    # Unique-per-run names: crashed earlier runs leave zombie queue rows whose
    # basenames would otherwise collide with ours in the shared queue UI.
    tok = "%d-%d" % (os.getpid(), int(time.time()))
    samples = {
        "beach-%s.png" % tok: PNG_1PX,
        "city-%s.png" % tok: PNG_1PX + blob[:4096],
        "hike-%s.jpg" % tok: blob,
    }
    paths, hashes = {}, {}
    for name, data in samples.items():
        p = os.path.join(d, name)
        with open(p, "wb") as fh:
            fh.write(data)
        paths[name] = p
        hashes[name] = sha256_bytes(data)
    return d, paths, hashes, tok


def start_mock():
    here = os.path.dirname(os.path.abspath(__file__))
    logf = open("/tmp/mock_immich_upload_e2e.log", "w")
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
                sys.exit("[test] mock immich died at startup; see "
                         "/tmp/mock_immich_upload_e2e.log")
            time.sleep(0.2)
    sys.exit("[test] mock immich did not become ready")


def poll_records(min_count):
    recs = http_json("GET", "/_test/uploads")["uploads"]
    return recs if len(recs) >= min_count else None


def verify_integrity(records, paths, src_hashes):
    """THE core assertion: source bytes == recorded hash == bytes on disk."""
    by_name = {}
    for r in records:
        by_name[r["originalFileName"]] = r
    ok = True
    for name in sorted(paths):
        rec = by_name.get(name)
        if rec is None:
            print("   MISSING upload record for %s" % name)
            ok = False
            continue
        with open(rec["storedPath"], "rb") as fh:
            disk = fh.read()
        h_src = src_hashes[name]
        h_rec = rec["sha256"]
        h_disk = sha256_bytes(disk)
        src_size = os.path.getsize(paths[name])
        sizes_ok = rec["fileSize"] == src_size == len(disk)
        match = (h_src == h_rec == h_disk) and sizes_ok
        print("   %-10s src=%s.. rec=%s.. disk=%s.. size=%dB %s"
              % (name, h_src[:12], h_rec[:12], h_disk[:12], len(disk),
                 "OK" if match else "MISMATCH"))
        expected_daid = "%s-%d" % (name, src_size)  # Go client format
        if rec["deviceAssetId"] != expected_daid:
            print("      !! deviceAssetId %r != %r"
                  % (rec["deviceAssetId"], expected_daid))
            ok = False
        if not match:
            ok = False
    extra = set(by_name) - set(paths)
    if extra:
        print("   UNEXPECTED uploads: %s" % sorted(extra))
        ok = False
    return ok


async def queue_rows(page):
    rows = page.locator("ul.divide-y li")
    n = await rows.count()
    out = []
    for i in range(n):
        out.append((await rows.nth(i).inner_text()).replace("\n", " ").strip())
    return out


async def all_done_rows(page, names):
    rows = await queue_rows(page)
    done = [r for r in rows
            if any(nm in r and "done" in r.lower() for nm in names)]
    return rows if len(done) >= len(names) else None


async def imgs_over_baseline(page, baseline):
    n = await page.locator("img").count()
    return n if n > baseline else None


async def wait_for(fn, timeout_s, what, diag=None):
    """Poll fn (sync or async) until truthy; raise TimeoutError otherwise.

    On timeout, print optional diagnostics (diag() -> str) so a failure shows
    the state that caused it instead of just a bare timeout."""
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        res = fn()
        if asyncio.iscoroutine(res):
            res = await res
        if res:
            return res
        await asyncio.sleep(0.5)
    detail = ""
    if diag is not None:
        try:
            d = diag()
            if asyncio.iscoroutine(d):
                d = await d
            detail = "\n--- diagnostics ---\n%s" % d
        except Exception as e:  # diagnostics must never mask the real error
            detail = "\n(collecting diagnostics failed: %s)" % e
    raise TimeoutError("timed out after %ss waiting for: %s%s"
                       % (timeout_s, what, detail))


def side_button(page, label):
    """Sidebar button by visible text (accessible names gain queue badges,
    so exact-name matching is unreliable)."""
    return page.locator("aside").get_by_role("button").filter(has_text=label)


async def nav(page, label):
    await side_button(page, label).click()


UPLOAD_PANEL_HEADER = "text=Upload Queue"


async def open_uploads_panel(page):
    """Ensure the Upload Queue panel is OPEN (idempotent).

    The panel is a sidebar TOGGLE (App.svelte showUploadPanel), closed by
    default, and UploadStatus.svelte -- hence every 'ul.divide-y li' row --
    only exists in the DOM while it is open. Detect state by the panel header,
    NOT by row count: an open panel legitimately shows 0 rows once the queue
    drains, so counting rows would toggle a healthy panel shut."""
    if await page.locator(UPLOAD_PANEL_HEADER).count() > 0:
        return
    await side_button(page, "Uploads").click()
    await wait_for(lambda: page.locator(UPLOAD_PANEL_HEADER).count(), 10,
                   "upload queue panel to open")


async def ensure_login(page):
    """Get to a fresh login form: sign out of any stale session first."""
    if await page.locator("#server").count() > 0:
        return
    print("   stale session detected -> signing out")
    await nav(page, "Settings")
    await page.get_by_role("button", name="Sign out").click()
    await wait_for(lambda: page.locator("#server").count(), 15,
                   "login form after sign-out")


async def add_watch_folder(page, path):
    # Two inputs share the placeholder (watched folders / downloads);
    # the watched-folders one comes first in DOM order.
    box = page.locator('input[placeholder^="Or paste a folder path"]').first
    await box.fill(path)
    await page.get_by_role("button", name="Add", exact=True).click()


async def remove_folders(page, dirs):
    """Best-effort teardown: unwatch the temp dirs this run registered."""
    if page is None:
        return
    for d in dirs:
        try:
            await nav(page, "Settings")
            row = page.locator("li").filter(has_text=d)
            if await row.count() == 0:
                continue
            await row.first.get_by_title("Remove folder").click()
            await page.wait_for_timeout(500)
        except Exception as e:
            print("   (teardown: %s: %s)" % (d, e))


async def gallery_after_login(page):
    b = await page.inner_text("body")
    return b if "Gallery" in b else None


async def main():
    await wait_http_ok(SERVER_URL)
    mock_proc = start_mock()
    tmp_dirs = []
    try:
        async with async_playwright() as p:
            await preflight(p)
            browser = await p.chromium.launch(headless=True)
            page = await browser.new_page()
            console_errors = []
            page.on("console.error", lambda e: console_errors.append(str(e)))

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
            await wait_for(lambda: gallery_after_login(page), 25,
                           "Gallery after login")
            print("   LOGIN OK -> Gallery")

            baseline_imgs = await page.locator("img").count()
            print("   baseline gallery imgs:", baseline_imgs)

            print("== 3. Add watch folder via Settings ==")
            http_json("POST", "/_test/reset")
            watch_dir, paths, src_hashes, tok = make_samples()
            tmp_dirs.append(watch_dir)
            await nav(page, "Settings")
            await add_watch_folder(page, watch_dir)
            await wait_for(lambda: page.get_by_text(watch_dir).count(), 15,
                           "folder listed under Watched Folders")
            print("   folder registered:", watch_dir)

            print("== 4. Wait for all 3 uploads to land on the mock ==")
            records = await wait_for(lambda: poll_records(3),
                                     UPLOAD_TIMEOUT_S, "3 upload records")
            by_name = {}
            for r in records:
                by_name[r["originalFileName"]] = r
            if set(by_name) != set(paths):
                print("   !! uploaded set mismatch: %s" % sorted(by_name))
                return 1
            print("   mock received:", sorted(by_name))

            print("== 5. Queue drains for our files ==")
            # MarkDone() DELETEs the row (db/sqlite.go:128), so a completed upload
            # is observable only as *absence* from the queue -- there is no 'done'
            # row to wait for. Also note the queue panel is closed by default
            # (App.svelte showUploadPanel=false), so it must be opened first or
            # ul.divide-y li matches nothing and this step hangs forever.
            await open_uploads_panel(page)
            names = list(paths)

            async def our_rows_gone():
                # NOTE: must return a truthy sentinel when drained. Returning the
                # (empty) row list made wait_for see a falsy [] and time out even
                # though the queue had fully drained.
                rows = await queue_rows(page)
                leftover = [r for r in rows if any(nm in r for nm in names)]
                return None if leftover else (rows or ["<no rows>"])
            async def drain_diag():
                rows = await queue_rows(page)
                pending = http_json("GET", "/_test/uploads")["uploads"]
                return ("queue rows (%d): %s\nmock uploads: %s"
                        % (len(rows), rows[:10],
                           sorted(r["originalFileName"] for r in pending)))
            leftover_rows = await wait_for(lambda: our_rows_gone(),
                                           UPLOAD_TIMEOUT_S,
                                           "our queue rows to drain (== done)",
                                           diag=drain_diag)
            print("   our rows drained; other rows:", len(leftover_rows),
                  "(leftovers from earlier runs are expected)")

            print("== 6. INTEGRITY: source vs record vs bytes-on-disk ==")
            if not verify_integrity(records, paths, src_hashes):
                return 1

            print("== 7. Gallery shows the new assets ==")
            await nav(page, "Gallery")
            grew = await wait_for(
                lambda: imgs_over_baseline(page, baseline_imgs),
                30, "gallery img count > baseline")
            print("   gallery imgs: %d -> %s" % (baseline_imgs, grew))

            print("== 8. Failure injection + Retry All ==")
            retry_dir = tempfile.mkdtemp(prefix="upload-e2e-retry-")
            tmp_dirs.append(retry_dir)
            retry_data = PNG_1PX + b"retry-bytes"
            retry_sha = sha256_bytes(retry_data)
            # Unique name for the same reason as the samples: a bare
            # "retry-me.png" collides with rows left by earlier runs, whose temp
            # dirs are already deleted -> spurious 'no such file' retries and
            # duplicate-match ambiguity in the assertions below.
            retry_name = "retry-me-%s.png" % tok
            with open(os.path.join(retry_dir, retry_name), "wb") as fh:
                fh.write(retry_data)
            # Arm the failure BEFORE adding the folder: AddFolder() scans
            # synchronously enough that injecting afterwards can miss the file.
            http_json("POST", "/_test/fail_next", {"count": 1})
            await nav(page, "Settings")
            await add_watch_folder(page, retry_dir)
            await open_uploads_panel(page)
            await wait_for(lambda: page.locator("ul.divide-y li")
                           .filter(has_text=retry_name)
                           .filter(has_text="failed").count(), 60,
                           "queue item marked failed")
            print("   item marked failed (injected 500)")
            http_json("POST", "/_test/fail_next", {"count": 0})
            await page.get_by_role("button", name="Retry All").click()

            def retry_landed():
                recs = http_json("GET", "/_test/uploads")["uploads"]
                hits = [r for r in recs
                        if r["originalFileName"] == retry_name]
                return hits[0] if len(hits) == 1 else None
            # Retry All -> RetryFailed() clears retry_count (db/sqlite.go:149), so
            # the 1-minute retryDelay is bypassed; the worker's ticker (10s) is the
            # only latency. Keep headroom well above that.
            rec = await wait_for(retry_landed, 90, "retry-me.png uploaded once")
            with open(rec["storedPath"], "rb") as fh:
                disk = fh.read()
            if not (sha256_bytes(disk) == rec["sha256"] == retry_sha
                    and disk == retry_data):
                print("   !! RETRY INTEGRITY FAIL")
                return 1
            print("   retry OK; byte-for-byte integrity verified")

            print("== 9. Idempotency: re-add folder, expect no duplicates ==")
            # Scope the check to THIS run's files. Global uniqueness is not the
            # invariant under test: rows left behind by earlier crashed runs are
            # legitimately re-uploaded, and counting them here would turn a
            # healthy run into a false failure.
            def our_upload_names():
                recs = http_json("GET", "/_test/uploads")["uploads"]
                return [r["originalFileName"] for r in recs
                        if r["originalFileName"] in paths]

            def our_counts_stable():
                names = our_upload_names()
                if len(names) != len(set(names)):
                    raise AssertionError("duplicate uploads of our files: %s" % names)
                return names

            before = our_counts_stable()
            if sorted(before) != sorted(paths):
                print("   !! expected exactly our 3 files, got %s" % sorted(before))
                return 1
            await nav(page, "Settings")
            await add_watch_folder(page, watch_dir)
            deadline = time.time() + 15
            while time.time() < deadline:
                our_counts_stable()  # raises the moment a duplicate appears
                time.sleep(1)
            after = our_counts_stable()
            print("   no duplicates: our %d files still uploaded exactly once"
                  % len(after))

            print("\nJS console errors:", console_errors if console_errors else "none")

            # Unwatch our temp folders while the page is still alive, so no queue
            # row survives pointing at a directory we are about to delete, then
            # remove the dirs themselves. (Clearing the list without deleting here
            # left the dirs behind: the finally-block below would find it empty.)
            await remove_folders(page, tmp_dirs)
            for d in tmp_dirs:
                shutil.rmtree(d, ignore_errors=True)
            tmp_dirs.clear()

            await browser.close()
            print("\nALL UPLOAD E2E CHECKS PASSED")
            return 0
    finally:
        mock_proc.terminate()
        # dirs still present here = the run aborted before the happy-path teardown
        for d in tmp_dirs:
            shutil.rmtree(d, ignore_errors=True)


if __name__ == "__main__":
    # Line-buffer so the progress log survives a hard failure/timeout.
    try:
        sys.stdout.reconfigure(line_buffering=True)
    except (AttributeError, OSError):
        pass
    sys.exit(asyncio.run(main()))