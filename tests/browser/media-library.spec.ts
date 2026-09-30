import AxeBuilder from "@axe-core/playwright"
import { expect, test, type Page } from "@playwright/test"

const baseURL = "http://127.0.0.1:4174"
const firstID = "11111111-1111-4111-8111-111111111111"
const secondID = "22222222-2222-4222-8222-222222222222"
const firstFileID = "33333333-3333-4333-8333-333333333333"
const seriesID = "99999999-9999-4999-8999-999999999999"
const seriesFileID = "77777777-7777-4777-8777-777777777777"

const media = [
  {
    id: firstID,
    kind: "movie",
    title: "First Browser Movie",
    year: 2024,
    identities: [{ namespace: "tmdb", value: "101", state: "verified" }],
    metadata: {
      overview: "The first browser-test overview.",
      original_language: "en",
      runtime_minutes: 98,
      genres: ["Drama"],
      countries: ["DK"],
      networks: [],
      aliases: [],
      ratings: [{ source: "tmdb", value: 8.1, votes: 200 }],
      sources: [],
      events: [],
      observed_at: "2026-09-01T00:00:00Z",
    },
  },
  {
    id: secondID,
    kind: "movie",
    title: "Second Browser Movie",
    year: 2022,
    identities: [{ namespace: "imdb", value: "tt0000202", state: "verified" }],
    metadata: {
      overview: "The second browser-test overview.",
      original_language: "da",
      genres: ["Comedy"],
      countries: ["DK"],
      networks: [],
      aliases: [],
      ratings: [],
      sources: [],
      events: [],
      observed_at: "2026-09-01T00:00:00Z",
    },
  },
  {
    id: "44444444-4444-4444-8444-444444444444",
    kind: "movie",
    title: "Third Browser Movie",
    year: 2020,
    identities: [],
  },
  {
    id: seriesID,
    kind: "series",
    title: "First Browser Series",
    year: 2021,
    identities: [{ namespace: "tvdb", value: "303", state: "verified" }],
    metadata: {
      overview: "The browser-test series overview.",
      original_language: "da",
      format: "Series",
      status: "Returning",
      release_date: "2021-03-04",
      season_count: 2,
      episode_count: 14,
      genres: ["Drama"],
      countries: ["DK"],
      networks: [{ name: "Browser Network", source: "tvdb" }],
      aliases: [],
      ratings: [{ source: "tvdb", value: 7.8, votes: 100 }],
      sources: [],
      events: [],
      observed_at: "2026-09-01T00:00:00Z",
    },
  },
]

const files = [{
  id: firstFileID,
  media_id: firstID,
  media_kind: "movie",
  title: "First Browser Movie",
  year: 2024,
  root_name: "Movies Primary",
  destination_relative: "First Browser Movie (2024)/First Browser Movie (2024).mkv",
  source_relative: "browser-fixture/output.mkv",
  plex_visible_target: "/movies/First Browser Movie (2024)/First Browser Movie (2024).mkv",
  state: "current",
  generation: 2,
  plex_confirmed: true,
  variant: "movie",
  origin: "altmount",
  quality_profile: "Movie 1080p Danish",
  quality_evidence_state: "media_info",
  quality_evidence: { facts: { resolution: "1080p", audio_codecs: ["eac3"], audio_languages: ["en"], subtitle_languages: ["da"] } },
  source_size_bytes: 1024,
}, {
  id: seriesFileID,
  media_id: seriesID,
  media_kind: "series",
  title: "First Browser Series",
  year: 2021,
  root_name: "TV Primary",
  destination_relative: "First Browser Series (2021) {tvdb-303}/Season 01/First Browser Series - S01E01.mkv",
  source_relative: "browser-series/episode.mkv",
  plex_visible_target: "/tv/First Browser Series (2021) {tvdb-303}/Season 01/First Browser Series - S01E01.mkv",
  state: "current",
  generation: 1,
  plex_confirmed: true,
  variant: "season:01",
  origin: "altmount",
  quality_profile: "TV 1080p Danish",
  quality_evidence_state: "media_info",
  quality_evidence: { facts: { resolution: "1080p", audio_codecs: ["aac"], subtitle_languages: ["da"] } },
  source_size_bytes: 2048,
}]

const wanted = [
  { workflow_id: "55555555-5555-4555-8555-555555555555", media_id: firstID, media_kind: "movie", title: "First Browser Movie", year: 2024, gap: "upgrade", current_state: "plex_confirmed", target_profile: "Movie 1080p Danish", upgrades_enabled: true, variant: "movie", updated_at: "2026-09-01T00:00:00Z" },
  { workflow_id: "66666666-6666-4666-8666-666666666666", media_id: secondID, media_kind: "movie", title: "Second Browser Movie", year: 2022, gap: "missing", current_state: "monitored", target_profile: "Movie 1080p Danish", upgrades_enabled: true, variant: "movie", updated_at: "2026-09-01T00:00:00Z" },
  { workflow_id: "88888888-8888-4888-8888-888888888888", media_id: seriesID, media_kind: "series", title: "First Browser Series", year: 2021, gap: "upgrade", current_state: "plex_confirmed", target_profile: "TV 1080p Danish", upgrades_enabled: true, variant: "season:01", updated_at: "2026-09-01T00:00:00Z" },
  { workflow_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", media_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", media_kind: "movie", title: "Attention Browser Movie", year: 2023, gap: "missing", current_state: "needs_attention", target_profile: "Movie 1080p Danish", upgrades_enabled: true, owner_action_required: true, attention_detail: "Required Danish subtitles were not verified", variant: "movie", updated_at: "2026-09-02T00:00:00Z" },
]

const workflows = [
  { id: wanted[0].workflow_id, media_id: firstID, media_kind: "movie", title: "First Browser Movie", year: 2024, variant: "movie", state: "plex_confirmed", revision: 4, adopted_replacement_confirmation_required: false, created_at: "2026-08-31T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" },
  { id: wanted[1].workflow_id, media_id: secondID, media_kind: "movie", title: "Second Browser Movie", year: 2022, variant: "movie", state: "monitored", revision: 1, adopted_replacement_confirmation_required: false, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" },
  { id: wanted[2].workflow_id, media_id: seriesID, media_kind: "series", title: "First Browser Series", year: 2021, variant: "season:01", state: "plex_confirmed", revision: 5, adopted_replacement_confirmation_required: false, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" },
]

let unexpectedRequests: string[] = []

async function installLocalAPI(page: Page) {
  unexpectedRequests = []
  await page.route("**/*", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.origin !== baseURL) {
      unexpectedRequests.push(`${request.method()} ${url.origin}${url.pathname}`)
      await route.abort("blockedbyclient")
      return
    }
    if (!url.pathname.startsWith("/api/v1/")) {
      await route.continue()
      return
    }

    const path = `${url.pathname}${url.search}`
    let data: unknown
    if (path === "/api/v1/media?limit=200&offset=0") data = media
    else if (path === "/api/v1/files?limit=300") data = files
    else if (path === "/api/v1/wanted?limit=300") data = wanted
    else if (path === "/api/v1/workflows?limit=300&active=false") data = workflows
    else if (path === "/api/v1/workflows?limit=300&active=true") data = workflows
    else if (path === "/api/v1/system/health") data = { database_state: "ready", schema_version: 30, expected_schema: 30 }
    else if (path === "/api/v1/system/info") data = { version: "browser-test" }
    else {
      unexpectedRequests.push(`${request.method()} ${path}`)
      await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
      return
    }
    await route.fulfill({ status: 200, json: { data } })
  })
}

test.beforeEach(async ({ page }) => {
  await installLocalAPI(page)
})

test.afterEach(() => {
  expect(unexpectedRequests).toEqual([])
})

test("desktop library interactions use complete local data", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 853 })
  await page.goto("/media")

  await expect(page.getByRole("heading", { name: "Movies" })).toBeVisible()
  await expect(page.getByLabel("3 movies")).toHaveText("3")
  await expect(page.getByRole("button", { name: "All Movies 3" })).toHaveAttribute("aria-pressed", "true")

  const firstMovie = page.getByRole("button", { name: /First Browser Movie, 2024/ })
  const secondMovie = page.getByRole("button", { name: /Second Browser Movie, 2022/ })
  await firstMovie.focus()
  await page.keyboard.press("Enter")
  const firstInspector = page.getByRole("complementary", { name: "Details for First Browser Movie" })
  await expect(firstInspector).toBeVisible()
  await expect(firstInspector.getByRole("heading", { name: "First Browser Movie" })).toBeVisible()
  await expect(firstInspector.getByText("The first browser-test overview.")).toBeVisible()

  await secondMovie.click()
  const secondInspector = page.getByRole("complementary", { name: "Details for Second Browser Movie" })
  await expect(secondInspector.getByRole("heading", { name: "Second Browser Movie" })).toBeVisible()
  await expect(secondInspector.getByText("The second browser-test overview.")).toBeVisible()
  await expect(secondInspector.getByText("The first browser-test overview.")).toHaveCount(0)

  await page.getByRole("button", { name: "List view" }).click()
  await expect(page.getByRole("button", { name: "List view" })).toHaveAttribute("aria-pressed", "true")
  await page.getByRole("button", { name: "Grid view" }).click()
  await expect(page.getByRole("button", { name: "Grid view" })).toHaveAttribute("aria-pressed", "true")

  await page.getByPlaceholder("Search movies").fill("First Browser")
  await expect(page.getByText("1 movie", { exact: true })).toBeVisible()
  await firstMovie.click()
  await page.getByRole("tab", { name: "Files" }).click()
  const managedFile = page.getByRole("link", { name: /First Browser Movie \(2024\)\/First Browser Movie \(2024\)\.mkv/ })
  await expect(managedFile).toHaveAttribute("href", `/files/${firstFileID}`)
  await page.getByRole("tab", { name: "Metadata" }).click()
  await expect(firstInspector.getByText("tmdb", { exact: true })).toBeVisible()
  await expect(firstInspector.getByText("101", { exact: true })).toBeVisible()
})

test("TV Shows library uses real series data and exact detail navigation", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 853 })
  await page.goto("/media/series")

  await expect(page.getByRole("heading", { name: "TV Shows" })).toBeVisible()
  await expect(page.getByLabel("1 TV show")).toHaveText("1")
  await expect(page.getByRole("button", { name: "All TV Shows 1" })).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByRole("button", { name: /First Browser Series, 2021/ })).toBeVisible()
  await expect(page.getByRole("button", { name: /First Browser Movie/ })).toHaveCount(0)

  await page.getByRole("button", { name: /First Browser Series, 2021/ }).click()
  const inspector = page.getByRole("complementary", { name: "Details for First Browser Series" })
  await expect(inspector.getByText("The browser-test series overview.")).toBeVisible()
  await expect(inspector.getByText("Browser Network")).toBeVisible()
  await expect(inspector.getByText("2", { exact: true })).toBeVisible()
  await expect(inspector.getByRole("link", { name: "Open TV show details" })).toHaveAttribute("href", `/media/series/${seriesID}`)

  await page.getByPlaceholder("Search TV shows").fill("tvdb 303")
  await expect(page.getByText("1 TV show", { exact: true })).toBeVisible()
  await page.getByPlaceholder("Search TV shows").fill("missing series")
  await expect(page.getByText("No TV shows matched")).toBeVisible()
})

test("library has no serious accessibility violations", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 853 })
  await page.goto("/media")
  await expect(page.getByRole("button", { name: /First Browser Movie, 2024/ })).toBeVisible()

  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze()
  const serious = results.violations.filter((violation) => violation.impact === "serious" || violation.impact === "critical")
  expect(serious).toEqual([])
})

test("mobile layout fits the viewport and exposes existing navigation", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/media")

  await expect(page.getByRole("navigation", { name: "Mobile navigation" })).toBeVisible()
  await expect(page.getByRole("button", { name: /First Browser Movie, 2024/ })).toBeVisible()
  const dimensions = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth,
    body: document.body.scrollWidth,
  }))
  expect(dimensions.document).toBeLessThanOrEqual(dimensions.viewport)
  expect(dimensions.body).toBeLessThanOrEqual(dimensions.viewport)

  await page.getByRole("navigation", { name: "Mobile navigation" }).getByRole("button", { name: "More" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByRole("link", { name: /Media Manager/ })).toBeVisible()
  await expect(sheet.getByRole("navigation", { name: "All pages" }).getByRole("link", { name: "Control room" })).toBeVisible()
})

test("library exposes density control, advanced filters, and one-click reset", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/media")
  await expect(page.getByRole("heading", { name: "Movies" })).toBeVisible()

  const density = page.getByLabel("Grid density")
  await expect(density).toHaveValue("compact")
  await density.selectOption("comfortable")
  await expect(density).toHaveValue("comfortable")

  await page.getByRole("button", { name: "More Filters" }).click()
  await page.getByLabel("Availability").selectOption("available")
  await expect(page.getByRole("button", { name: /Availability: Available/ })).toBeVisible()
  await expect(page.getByText("1 movie", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Clear all" }).click()
  await expect(page.getByRole("button", { name: /Availability: Available/ })).toHaveCount(0)
  await expect(page.getByText("3 movies", { exact: true })).toBeVisible()
})

test("command palette navigates pages and the bell shows real owner attention", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 853 })
  await page.goto("/media")
  await expect(page.getByRole("heading", { name: "Movies" })).toBeVisible()

  await page.getByRole("button", { name: /Go to page…/ }).click()
  const palette = page.getByRole("dialog", { name: "Go to page" })
  await expect(palette).toBeVisible()
  await expect(palette.getByText("TV Shows", { exact: true })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(palette).toBeHidden()

  await page.getByRole("button", { name: "Owner attention" }).click()
  const popover = page.getByText("Owner attention", { exact: true })
  await expect(popover).toBeVisible()
  await expect(page.getByText("Attention Browser Movie", { exact: false })).toBeVisible()
  await expect(page.getByText("Required Danish subtitles were not verified", { exact: false })).toBeVisible()
  const viewAll = page.getByRole("link", { name: "View all in Wanted" })
  await expect(viewAll).toHaveAttribute("href", "/wanted")
})
