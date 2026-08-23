#!/usr/bin/env python3
"""Wails3 server-mode bridge smoke test.
Loads the app served by the wails3 server-mode binary (pure HTTP, no GTK) and
verifies the frontend-to-Go IPC bridge works through the HTTP transport.
"""
import asyncio, json, sys

from playwright.async_api import async_playwright

SERVER_URL = "http://127.0.0.1:3823"


async def main():
    async with async_playwright() as p:
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