import { expect, test, type Page } from "@playwright/test"

const baseOrigin = new URL(`http://127.0.0.1:${process.env.MEDIA_MANAGER_LIVE_PORT}`).origin
const outDir = process.env.MM_LIVE_SHOT_DIR ?? "/tmp/live-shots"

async function settle(page: Page) {
  await page.waitForLoadState("domcontentloaded")
  await page.waitForLoadState("networkidle", { timeout: 10_000 }).catch(() => {})
  await page.waitForTimeout(400)
}

for (const [name, path] of [
  ["overview", "/"],
  ["movies", "/media"],
  ["tv", "/media/series"],
  ["requests", "/requests"],
  ["wanted", "/wanted"],
  ["workflows", "/activity/queue"],
  ["indexers", "/sources/indexers"],
  ["connections", "/settings/connections"],
  ["health", "/system/health"],
] as const) {
  test(`live screenshot ${name}`, async ({ page }) => {
    await page.route("**/*", async (route) => {
      const url = new URL(route.request().url())
      if (url.protocol === "data:" || url.protocol === "blob:") { await route.continue(); return }
      const proxied = url.searchParams.get("url")
      if (url.origin === baseOrigin && proxied) {
        try { if (new URL(proxied).origin !== baseOrigin) { await route.abort("blockedbyclient"); return } } catch { /* relative */ }
      }
      if (url.origin !== baseOrigin) { await route.abort("blockedbyclient"); return }
      if (route.request().method() !== "GET" && route.request().method() !== "HEAD") { await route.abort("blockedbyclient"); return }
      await route.continue()
    })
    await page.setViewportSize({ width: 1920, height: 1080 })
    const response = await page.goto(path, { waitUntil: "domcontentloaded" })
    expect(response?.status()).toBe(200)
    await settle(page)
    await page.screenshot({ path: `${outDir}/${name}.png`, fullPage: true })
  })
}
