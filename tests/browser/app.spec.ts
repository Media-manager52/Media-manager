import AxeBuilder from "@axe-core/playwright"
import { expect, test, type Page } from "@playwright/test"
import path from "node:path"

import { installOwnerAPI, ownerFixtureIDs as ids } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"
const screenshotDir = process.env.MM_APP_SCREENSHOTS

const routes = [
  "/",
  "/library",
  "/library?type=series",
  `/library/${ids.movieA}`,
  `/library/${ids.mediaAttention}`,
  `/library/${ids.series}`,
  "/requests",
  "/needs-you",
  "/add",
  "/settings",
]

const viewports = [
  { name: "desktop", width: 1440, height: 900 },
  { name: "phone", width: 390, height: 844 },
] as const

async function settle(page: Page) {
  await page.waitForLoadState("networkidle")
  await expect(page.getByRole("status").filter({ hasText: /^Loading|^Checking your library/ })).toHaveCount(0, { timeout: 10_000 })
}

function trackErrors(page: Page) {
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  page.on("console", (message) => { if (message.type() === "error" && !/Failed to load resource/.test(message.text())) errors.push(message.text()) })
  return errors
}

for (const viewport of viewports) {
  test.describe(`${viewport.name} everyday app`, () => {
    for (const route of routes) {
      test(`${route} renders cleanly`, async ({ page }) => {
        await page.setViewportSize({ width: viewport.width, height: viewport.height })
        const recorder = await installOwnerAPI(page, baseURL)
        const errors = trackErrors(page)
        await page.goto(route)
        await settle(page)
        await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible()
        const overflow = await page.evaluate(() => ({ viewport: window.innerWidth, document: document.documentElement.scrollWidth }))
        expect(overflow.document, "page must not scroll sideways").toBeLessThanOrEqual(overflow.viewport)
        const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze()
        const serious = results.violations
          .filter((violation) => violation.impact === "serious" || violation.impact === "critical")
          .map((violation) => `${violation.id}: ${violation.nodes.slice(0, 3).map((node) => node.target.join(" ")).join(" | ")}`)
        expect(serious).toEqual([])
        if (screenshotDir) await page.screenshot({ path: path.join(screenshotDir, `${viewport.name}-${route.replace(/[/?=]+/g, "_") || "home"}.png`), fullPage: true })
        expect(errors).toEqual([])
        expect(recorder.unexpected.filter((entry) => !entry.includes("image.tmdb.org"))).toEqual([])
      })
    }
  })
}

test("home puts owner decisions first and shows work on the way in plain words", async ({ page }) => {
  await installOwnerAPI(page, baseURL)
  await page.goto("/")
  await settle(page)
  const needs = page.getByRole("region", { name: "Needs you" })
  await expect(needs.getByText("The download didn’t pass the check")).toBeVisible()
  await expect(needs.getByText("Required Danish subtitles were not verified")).toBeVisible()
  // The fixture lists no server actions, so the card offers the expert console instead of inventing one.
  await expect(needs.getByRole("link", { name: "See all options" })).toHaveAttribute("href", `/media/movies/${ids.mediaAttention}`)
  await expect(page.getByRole("link", { name: "Nordic Winter Film, Finding a copy" })).toBeVisible()
  await expect(page.getByRole("link", { name: "Needs you: 1 item" })).toBeVisible()
  await page.getByRole("region", { name: "Movies" }).getByRole("link", { name: /^First Browser Movie/ }).click()
  await expect(page).toHaveURL(`${baseURL}/library/${ids.movieA}`)
  await expect(page.getByRole("heading", { level: 1, name: "First Browser Movie" })).toBeVisible()
})

test("library switches kind and status filters and keeps them in the address", async ({ page }) => {
  await installOwnerAPI(page, baseURL)
  await page.goto("/library")
  await settle(page)
  await expect(page.getByRole("link", { name: /^Attention Browser Movie/ })).toBeVisible()
  await page.getByRole("button", { name: /^Needs you/ }).click()
  await expect(page.getByRole("link", { name: /Browser Movie/ })).toHaveCount(1)
  await expect(page).toHaveURL(`${baseURL}/library?show=needs`)
  await page.getByRole("button", { name: /^TV shows/ }).click()
  await page.getByRole("button", { name: /^All/ }).click()
  await expect(page.getByRole("link", { name: /^Harbour Detectives/ })).toBeVisible()
  await expect(page).toHaveURL(`${baseURL}/library?type=series`)
  await page.getByRole("searchbox", { name: "Search your library" }).fill("harbour")
  await expect(page.getByRole("link", { name: /^First Browser Series/ })).toHaveCount(0)
})

test("a stopped title explains why, shows where it stopped and keeps expert detail folded", async ({ page }) => {
  await installOwnerAPI(page, baseURL)
  await page.goto(`/library/${ids.mediaAttention}`)
  await settle(page)
  await expect(page.getByRole("note")).toContainText("The download didn’t pass the check")
  await expect(page.getByRole("list", { name: "Progress: Needs you" }).getByRole("listitem").filter({ hasText: "Checking" })).toHaveAttribute("aria-current", "step")
  await expect(page.getByText("needs_attention · subtitle_unverified")).toBeHidden()
  await page.getByText("Details for experts").click()
  await expect(page.getByText("needs_attention · subtitle_unverified")).toBeVisible()
  await expect(page.getByRole("link", { name: "Open in the expert console" })).toHaveAttribute("href", `/media/movies/${ids.mediaAttention}`)
})

test("a title in Plex lists what the owner has in plain words", async ({ page }) => {
  await installOwnerAPI(page, baseURL)
  await page.goto(`/library/${ids.movieA}`)
  await settle(page)
  const have = page.getByRole("region", { name: "What you have" })
  await expect(have).toContainText("1080p")
  await expect(have).toContainText("English audio")
  await expect(have).toContainText("Danish subtitles")
  await expect(have).toContainText("Confirmed in Plex")
  await expect(page.getByText("Asked for by Owner")).toBeVisible()
})

test("server-listed actions run exactly as described and Stop asks first", async ({ page }) => {
  const actionPath = `/api/v1/workflows/${ids.wfAttention}`
  const calls: string[] = []
  await installOwnerAPI(page, baseURL, { transform: (path, data) => path !== "/api/v1/workflows" ? data : (data as Array<Record<string, unknown>>).map((workflow) => workflow.id === ids.wfAttention ? { ...workflow, available_actions: [
    { action: "open_round_from_known", operation_id: "openKnownAcquisitionRound", method: "POST", path: `${actionPath}/acquisition-rounds/from-known`, effect: "provider", available: true },
    { action: "search", operation_id: "searchWorkflow", method: "POST", path: `${actionPath}/search`, effect: "provider", available: false, reason: "Round exhausted" },
    { action: "cancel", operation_id: "cancelWorkflow", method: "POST", path: `${actionPath}/cancel`, effect: "write", available: true },
  ] } : workflow) })
  await page.route(`**${actionPath}/**`, async (route) => {
    calls.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`)
    await route.fulfill({ status: 200, json: { data: { status: "queued", id: ids.wfAttention } } })
  })
  await page.goto("/needs-you")
  await settle(page)
  await expect(page.getByRole("button", { name: "Search now" })).toHaveCount(0)
  await page.getByRole("button", { name: "Try other copies" }).click()
  await expect(page.getByRole("status").filter({ hasText: "Try other copies: done for Attention Browser Movie" })).toBeVisible()
  await page.getByRole("button", { name: "Stop", exact: true }).click()
  expect(calls).toEqual([`POST ${actionPath}/acquisition-rounds/from-known`])
  await page.getByRole("button", { name: "Keep going" }).click()
  await page.getByRole("button", { name: "Stop", exact: true }).click()
  await page.getByRole("button", { name: "Yes, stop" }).click()
  await expect.poll(() => calls).toEqual([`POST ${actionPath}/acquisition-rounds/from-known`, `POST ${actionPath}/cancel`])
})

test("requests waiting for approval can be approved from Needs you", async ({ page }) => {
  const pending = { id: `${ids.seerr}:777`, request_id: "777", seerr_instance_id: ids.seerr, media_kind: "movie", title: "Pending Browser Movie", year: 2025, requester: "Family", requested_at: "2026-09-29T10:00:00Z", updated_at: "2026-09-29T10:00:00Z", seerr_state: "1", lifecycle: "not_approved", workflow_count: 0, confirmed_count: 0, attention_count: 0, season_count: 0, available_seasons: 0 }
  const decisions: string[] = []
  await installOwnerAPI(page, baseURL, { transform: (path, data) => path === "/api/v1/requests" ? [pending, ...(data as unknown[])] : data })
  await page.route("**/api/v1/requests/**", async (route) => {
    const url = new URL(route.request().url())
    if (route.request().method() === "POST") {
      decisions.push(url.pathname)
      await route.fulfill({ status: 200, json: { data: { status: "approved", request_id: "777", seerr_status: 2 } } })
      return
    }
    await route.fallback()
  })
  await page.goto("/needs-you")
  await settle(page)
  await expect(page.getByText("Family asked for it")).toBeVisible()
  await page.getByRole("button", { name: "Approve Pending Browser Movie" }).click()
  await expect.poll(() => decisions).toEqual([`/api/v1/requests/${encodeURIComponent(pending.id)}/approve`])
  await expect(page.getByRole("status").filter({ hasText: "Approved Pending Browser Movie" })).toBeVisible()
  await page.goto("/requests")
  await settle(page)
  await page.getByRole("button", { name: /^Waiting for your OK/ }).click()
  await expect(page.getByRole("link", { name: "First Browser Movie" })).toHaveCount(0)
  await expect(page.getByText("Pending Browser Movie")).toBeVisible()
})

test("add verifies the identity and sends exact season snapshots", async ({ page }) => {
  await installOwnerAPI(page, baseURL)
  const created: Array<Record<string, unknown>> = []
  await page.route("**/api/v1/discovery/**", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/api/v1/discovery/search") {
      await route.fulfill({ json: { data: { page: 1, totalPages: 1, totalResults: 3, results: [
        { id: 101, mediaType: "movie", title: "First Browser Movie", releaseDate: "2024-03-01" },
        { id: 900, mediaType: "movie", title: "Brand New Film", releaseDate: "2025-05-05", overview: "Not in the library yet." },
        { id: 901, mediaType: "tv", name: "Brand New Show", firstAirDate: "2023-01-01" },
      ] } } })
      return
    }
    if (url.pathname === "/api/v1/discovery/movie/900") {
      await route.fulfill({ json: { data: { kind: "movie", title: "Brand New Film", year: 2025, tmdb_id: 900, identities: [{ namespace: "tmdb", value: "900", state: "verified", source: "tmdb" }], enrichment_state: "tmdb_via_seerr", provider_states: {} } } })
      return
    }
    if (url.pathname === "/api/v1/discovery/tv/901") {
      const season = url.searchParams.get("season")
      await route.fulfill({ json: { data: { kind: "series", title: "Brand New Show", year: 2023, tmdb_id: 901, identities: [{ namespace: "tmdb", value: "901", state: "verified", source: "tmdb" }], metadata: { season_count: 2, genres: [], countries: [], networks: [], aliases: [], ratings: [], sources: [], events: [], observed_at: "2026-09-01T00:00:00Z" }, enrichment_state: "tmdb_via_seerr", provider_states: {}, ...(season ? { season: { number: Number(season), episodes: [{ number: 1, provider_id: `ep-${season}-1`, title: "One", air_date: "2023-01-01" }] } } : {}) } } })
      return
    }
    await route.fallback()
  })
  await page.route("**/api/v1/media", async (route) => {
    if (route.request().method() !== "POST") { await route.fallback(); return }
    const input = route.request().postDataJSON() as Record<string, unknown>
    created.push(input)
    await route.fulfill({ json: { data: { media: { id: input.kind === "movie" ? "12121212-1212-4212-8212-121212121212" : "13131313-1313-4313-8313-131313131313", kind: input.kind, title: input.title, identities: [] }, workflows: [] } } })
  })
  await page.goto("/add")
  await settle(page)
  await page.getByRole("searchbox", { name: "Search for a movie or show" }).fill("browser")
  await page.getByRole("button", { name: "Search", exact: true }).click()
  await expect(page.getByText("In your library")).toBeVisible()
  await page.getByRole("button", { name: "Add Brand New Film" }).click()
  await expect(page.getByText(/^Added ·/)).toBeVisible()
  await page.getByRole("button", { name: "Choose seasons of Brand New Show" }).click()
  await page.getByRole("button", { name: "Season 2" }).click()
  await page.getByRole("button", { name: "Add 1 season" }).click()
  await expect.poll(() => created.length).toBe(2)
  expect(created[0]).toMatchObject({ kind: "movie", title: "Brand New Film", year: 2025, search_immediately: true, identities: [{ namespace: "tmdb", value: "900", state: "verified" }] })
  expect(created[0]).not.toHaveProperty("root_id")
  expect(created[1]).toMatchObject({ kind: "series", title: "Brand New Show", seasons: [{ number: 2, monitored: true, prefer_season_pack: true, episodes: [{ number: 1, provider_episode_id: "ep-2-1" }] }] })
})

test("the expert console stays one click away and links back", async ({ page }) => {
  await installOwnerAPI(page, baseURL)
  await page.goto("/settings")
  await settle(page)
  await page.getByRole("link", { name: "Open expert console" }).click()
  await expect(page).toHaveURL(`${baseURL}/console`)
  await expect(page.getByRole("link", { name: "Back to the new app" })).toHaveAttribute("href", "/")
})
