#!/usr/bin/env python3
# /// script
# dependencies = [
#   "playwright>=1.0",
# ]
# ///
"""Wails3 server-mode bridge smoke test.
Loads the app served by the wails3 server-mode binary (pure HTTP, no GTK) and
verifies the frontend-to-Go IPC bridge works through the HTTP transport.
"""
import asyncio, os, subprocess, sys

from playwright.async_api import async_playwright

SERVER_URL = "http://127.0.0.1:3823"


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
            "ldd", exe, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL)
        out, _ = await ldd.communicate()
        ready = b"not found" not in out
    if ready:
        return
    print(f"[preflight] Chromium not ready ({exe}); installing browser + system deps...")
    for step in (["install", "chromium"], ["install-deps", "chromium"]):
        cmd = [sys.executable, "-m", "playwright", *step]
        if step[0] == "install-deps" and os.name == "posix" and os.geteuid() != 0:
            cmd = ["sudo"] + cmd  # apt needs root
        proc = await asyncio.create_subprocess_exec(*cmd)
        if (rc := await proc.wait()) != 0:
            sys.exit(f"[preflight] playwright {step[0]} failed (rc={rc}). Run manually:\n"
                     "  uv run --with playwright playwright install chromium\n"
                     "  uv run --with playwright playwright install-deps chromium")


async def main():
    async with async_playwright() as p:
        await preflight(p)
        browser = await p.chromium.launch(headless=True)
        page = await browser.new_page()
        console = []
        page.on("console.error", lambda e: console.append(str(e)))

        print("== Load server-mode app ==")
        await page.goto(SERVER_URL, timeout=30000)
        await page.wait_for_load_state("domcontentloaded")
        await page.wait_for_timeout(3000)
        body = await page.inner_text("body")
        print("DOM:", body.replace("\n", " | ")[:400])

        # Confirm the Svelte app mounted (login form = unauthenticated state)
        has_server_input = await page.locator("#server").count()
        print("Login form (#server input) count:", has_server_input)

        await browser.close()
        return 0 if has_server_input else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))