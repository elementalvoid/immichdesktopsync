#!/usr/bin/env python3
# /// script
# dependencies = [
#   "playwright>=1.0",
# ]
# ///
"""Full end-to-end test on Wails3 server mode (no GTK).
Drives the real Svelte UI in a headless browser against the wails3 HTTP server.
Exercises: login form -> Go Login -> mock Immich -> Gallery -> thumbnails.

Run with:  uv run tests/test_e2e_w3.py  (playwright auto-installed per PEP 723)
"""
import asyncio, json, sys, os

from playwright.async_api import async_playwright

SERVER_URL = "http://127.0.0.1:3823"
IMMICH_URL = "http://127.0.0.1:22841"
EMAIL = "test@example.com"
PASSWORD = "secret"


async def dump(page):
    body = await page.inner_text("body")
    return body.replace("\n", " | ")[:500]


async def main():
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        page = await browser.new_page()
        console = []
        page.on("console.error", lambda e: console.append(str(e)))

        print("== 1. Load server ==")
        await page.goto(SERVER_URL, timeout=30000)
        await page.wait_for_load_state("domcontentloaded")
        await page.wait_for_timeout(2500)
        print("   DOM:", await dump(page))

        # The app may already be authenticated (token persisted from an earlier
        # run -> Gallery shows directly), or show the Login form. Handle both.
        if await page.locator("#server").count() == 0:
            print("   no login form (already authenticated or gallery shown)")
        else:
            print("   login form present")
            print("\n== 2. Login ==")
            await page.locator("#server").fill(IMMICH_URL)
            await page.locator("#email").fill(EMAIL)
            await page.locator("#password").fill(PASSWORD)
            await page.locator("#password").press("Enter")
            await page.wait_for_timeout(4000)
        body = await page.inner_text("body")
        print("   DOM after login:", await dump(page))
        if "Gallery" in body and "Uploads" in body:
            print("   LOGIN OK -> Gallery")
        else:
            print("   !! not gallery:", body[:300])
            await browser.close()
            return 1

        print("\n== 3. Gallery thumbnails ==")
        for _ in range(15):
            imgs = await page.locator("img").count()
            if imgs > 0:
                print(f"   Found {imgs} img elements"); break
            await page.wait_for_timeout(1000)
        else:
            print("   no imgs found")

        print("\nJS console errors:", console if console else "none")
        await browser.close()
        return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))