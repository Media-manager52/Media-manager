import { expect, test } from "@playwright/test"
import { installOwnerAPI, ownerFixtureIDs } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"

test("V2 navigation opens separate views and preserves the current console", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  await page.goto("/v2")
  await expect(page.getByRole("heading", { name: "Your library, in motion." })).toBeVisible()
  await expect(page.getByRole("heading", { name: "First Browser Movie", level: 3 }).first()).toBeVisible()
  await page.screenshot({ path: ".sites-runtime/v2-preview-desktop.png", fullPage: true })
  await page.getByRole("navigation", { name: "V2 navigation" }).getByRole("link", { name: "Library", exact: true }).click()
  await expect(page).toHaveURL(/\/v2\/library$/)
  await expect(page.getByRole("heading", { name: "A home for every story." })).toBeVisible()
  await page.getByRole("link", { name: "Current UI", exact: true }).click()
  await expect(page).toHaveURL(`${baseURL}/`)
  await expect(page.getByRole("heading", { name: "Overview", exact: true, level: 1 })).toBeVisible()
  expect(recorder.unexpected).toEqual([])
})

test("V2 library filters real titles and uses reachable local artwork fallbacks", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  await page.goto("/v2/library")
  await expect(page.getByRole("heading", { name: "First Browser Series" })).toBeVisible()
  await page.getByRole("button", { name: "Movies", exact: true }).click()
  await expect(page.getByRole("heading", { name: "First Browser Series" })).toHaveCount(0)
  await page.getByRole("searchbox", { name: "Search this view" }).fill("Second Browser")
  await expect(page.locator(".v2-poster-card")).toHaveCount(1)
  await expect(page.locator(".v2-poster-card img")).toHaveAttribute("src", /movie-placeholder/)
  await expect.poll(() => page.locator(".v2-poster-card img").evaluate((image) => (image as HTMLImageElement).naturalWidth)).toBeGreaterThan(0)
  await page.getByRole("searchbox").fill("no matching title")
  await expect(page.getByText("No titles match these filters.")).toBeVisible()
  expect(recorder.unexpected).toEqual([])
})

test("V2 waiting requests distinguish approval, admission and actual search schedules", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  const next = "2026-10-02T15:30:00Z"
  await page.route("**/api/v1/requests?*", (route) => route.fulfill({ json: { data: [{
    id: ownerFixtureIDs.request, request_id: "412", seerr_instance_id: ownerFixtureIDs.seerr,
    seerr_state: "2", media_kind: "movie", title: "Waiting Browser Movie", year: 2024,
    media_id: ownerFixtureIDs.movieA, lifecycle: "waiting", workflow_count: 1,
    confirmed_count: 0, attention_count: 0, waiting_reason: "no eligible release yet",
    next_search_at: next, updated_at: "2026-10-01T00:00:00Z", requested_at: "2026-10-01T00:00:00Z",
  }] } }))
  await page.goto("/v2/requests")
  const card = page.locator(".v2-request-card")
  await expect(card.getByText("Waiting", { exact: true })).toBeVisible()
  await expect(card.getByText("Seerr: Approved", { exact: true })).toBeVisible()
  await expect(card.getByText("Added to Media Manager", { exact: true })).toBeVisible()
  await expect(card.getByText("Added to Media Manager. No eligible release matched the latest search.")).toBeVisible()
  await expect(card.locator("time")).toHaveAttribute("datetime", next)
  await expect(card.getByText("Plex confirmed", { exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "Available", exact: true }).click()
  await expect(page.getByText("No requests match these filters.")).toBeVisible()
  expect(recorder.unexpected).toEqual([])
})

test("V2 attention inbox shows exact failures and routes recovery to the owner console", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/v2/attention")
  await expect(page.locator(".v2-attention-card")).toHaveCount(1)
  await expect(page.getByText("Required Danish subtitles were not verified", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Recovery options" })).toHaveAttribute("href", "/activity/queue")
  await expect(page.getByRole("link", { name: "Review title" })).toHaveAttribute("href", `/media/movies/${ownerFixtureIDs.mediaAttention}`)
  await page.screenshot({ path: ".sites-runtime/v2-preview-mobile.png", fullPage: true })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  expect(recorder.unexpected).toEqual([])
})

test("V2 errors stay visible and refresh makes only read requests", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  await page.route("**/api/v1/requests?*", (route) => route.fulfill({ status: 503, json: { error: { code: "unavailable", message: "Request data unavailable" } } }))
  await page.goto("/v2/requests")
  await expect(page.getByRole("alert")).toContainText("Request data unavailable")
  await expect(page.locator(".v2-request-card")).toHaveCount(0)
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByRole("alert")).toBeVisible()
  expect(recorder.unexpected).toEqual([])
})

test("V2 theme switch uses and preserves the browser theme preference", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  await page.addInitScript(() => localStorage.setItem("mm-theme", "light"))
  await page.goto("/v2")
  await expect(page.locator("html")).not.toHaveClass(/dark/)
  await page.getByRole("button", { name: "Switch color theme" }).click()
  await expect(page.locator("html")).toHaveClass(/dark/)
  expect(await page.evaluate(() => localStorage.getItem("mm-theme"))).toBe("dark")
  expect(recorder.unexpected).toEqual([])
})

test("V2 pagination resets after a search instead of stranding matching titles", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  await page.route("**/api/v1/media?*", (route) => route.fulfill({ json: { data: Array.from({ length: 30 }, (_, index) => ({
    id: `11111111-1111-4111-8111-${String(index + 1).padStart(12, "0")}`,
    kind: "movie", title: `Pagination Movie ${String(index + 1).padStart(2, "0")}`, year: 2024, identities: [],
  })) } }))
  await page.goto("/v2/library")
  await expect(page.locator(".v2-poster-card")).toHaveCount(24)
  await page.getByRole("button", { name: "Next", exact: true }).click()
  await expect(page.locator(".v2-poster-card")).toHaveCount(6)
  await expect(page.getByText("Page 2 of 2")).toBeVisible()
  await page.getByRole("searchbox").fill("Movie 01")
  await expect(page.locator(".v2-poster-card")).toHaveCount(1)
  await expect(page.getByRole("heading", { name: "Pagination Movie 01" })).toBeVisible()
  expect(recorder.unexpected).toEqual([])
})
