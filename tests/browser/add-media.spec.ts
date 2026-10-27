import { expect, test, type Page } from "@playwright/test"

const baseURL = "http://127.0.0.1:4174"
const movieRootID = "11111111-1111-4111-8111-111111111111"
const seriesRootID = "22222222-2222-4222-8222-222222222222"
const movieProfileID = "33333333-3333-4333-8333-333333333333"
const seriesProfileID = "44444444-4444-4444-8444-444444444444"
const clientID = "55555555-5555-4555-8555-555555555555"
const indexerID = "66666666-6666-4666-8666-666666666666"

const metadataBase = {
  genres: ["Drama"],
  countries: ["DK"],
  networks: [],
  aliases: [],
  sources: [],
  ratings: [],
  events: [],
  observed_at: "2026-09-01T00:00:00Z",
}

const libraryRoots = [
  { id: movieRootID, media_kind: "movie", name: "Movies Primary", destination_root: "/libraries/movies", plex_visible_root: "/movies", source_visible_root: "/mnt/altmount", plex_instance_id: "plex", plex_library_id: "1", is_default: true, enabled: true },
  { id: seriesRootID, media_kind: "series", name: "TV Primary", destination_root: "/libraries/tv", plex_visible_root: "/tv", source_visible_root: "/mnt/altmount", plex_instance_id: "plex", plex_library_id: "2", is_default: true, enabled: true },
]

const qualityProfiles = [
  { id: movieProfileID, media_kind: "movie", name: "Movie 1080p Danish", policy_version: 1, policy: {}, upgrades_enabled: true, is_default: true, enabled: true },
  { id: seriesProfileID, media_kind: "series", name: "TV 1080p Danish", policy_version: 1, policy: {}, upgrades_enabled: true, is_default: true, enabled: true },
]

const integrations = [
  { id: clientID, kind: "altmount", name: "AltMount", enabled: true, is_default: true, config: {}, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" },
  { id: indexerID, kind: "newznab", name: "Local test indexer", enabled: true, is_default: true, config: { mode_verified: true }, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" },
]

type Scenario = "movie" | "series"

async function installAddMediaAPI(page: Page, scenario: Scenario) {
  const created: unknown[] = []
  const unexpected: string[] = []

  await page.route("**/*", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.origin !== baseURL) {
      unexpected.push(`${request.method()} ${url.origin}${url.pathname}`)
      await route.abort("blockedbyclient")
      return
    }
    if (!url.pathname.startsWith("/api/v1/")) {
      await route.continue()
      return
    }

    const path = `${url.pathname}${url.search}`
    let data: unknown
    if (request.method() === "GET" && path === "/api/v1/settings/library-roots") data = libraryRoots
    else if (request.method() === "GET" && path === "/api/v1/settings/quality-profiles") data = qualityProfiles
    else if (request.method() === "GET" && path === "/api/v1/settings/integrations") data = integrations
    else if (request.method() === "GET" && path === "/api/v1/system/health") data = { database_state: "ready", schema_version: 30, expected_schema: 30 }
    else if (request.method() === "GET" && path === "/api/v1/system/info") data = { version: "browser-test" }
    else if (request.method() === "GET" && path.startsWith("/api/v1/discovery/search?")) {
      data = scenario === "movie"
        ? { page: 1, totalPages: 1, totalResults: 1, results: [{ id: 101, mediaType: "movie", title: "Browser Add Movie", releaseDate: "2024-02-03", overview: "Verified local movie metadata." }] }
        : { page: 1, totalPages: 1, totalResults: 1, results: [{ id: 202, mediaType: "tv", name: "Browser Add Series", firstAirDate: "2023-04-05", overview: "Verified local series metadata." }] }
    } else if (request.method() === "GET" && path === "/api/v1/discovery/movie/101") {
      data = { kind: "movie", title: "Browser Add Movie", year: 2024, tmdb_id: 101, identities: [{ namespace: "tmdb", value: "101", source: "seerr_metadata" }, { namespace: "imdb", value: "tt0000101", source: "seerr_metadata" }], metadata: { ...metadataBase, overview: "Verified local movie metadata.", original_language: "da", runtime_minutes: 91 }, enrichment_state: "direct_provider_enriched", provider_states: { tmdb: "direct", tvdb: "direct", simkl: "direct" } }
    } else if (request.method() === "GET" && path === "/api/v1/discovery/tv/202") {
      data = { kind: "series", title: "Browser Add Series", year: 2023, tmdb_id: 202, identities: [{ namespace: "tmdb", value: "202", source: "seerr_metadata" }, { namespace: "tvdb", value: "302", source: "seerr_metadata" }], metadata: { ...metadataBase, overview: "Verified local series metadata.", original_language: "da", season_count: 2, episode_count: 5 }, enrichment_state: "direct_provider_enriched", provider_states: { tmdb: "direct", tvdb: "direct", simkl: "direct" } }
    } else if (request.method() === "GET" && path === "/api/v1/discovery/tv/202?season=1") {
      data = { kind: "series", title: "Browser Add Series", year: 2023, tmdb_id: 202, identities: [{ namespace: "tvdb", value: "302", source: "seerr_metadata" }], enrichment_state: "direct_provider_enriched", provider_states: { tmdb: "direct", tvdb: "direct" }, season: { number: 1, episodes: [{ number: 1, provider_id: "tvdb:1001", title: "Pilot", air_date: "2023-04-05" }, { number: 2, provider_id: "tvdb:1002", title: "Second", air_date: "2023-04-12" }] } }
    } else if (request.method() === "GET" && path === "/api/v1/discovery/tv/202?season=2") {
      data = { kind: "series", title: "Browser Add Series", year: 2023, tmdb_id: 202, identities: [{ namespace: "tvdb", value: "302", source: "seerr_metadata" }], enrichment_state: "direct_provider_enriched", provider_states: { tmdb: "direct", tvdb: "direct" }, season: { number: 2, episodes: [{ number: 1, provider_id: "tvdb:2001", title: "Return", air_date: "2024-05-01" }, { number: 2, provider_id: "tvdb:2002", title: "Middle", air_date: "2024-05-08" }, { number: 3, provider_id: "tvdb:2003", title: "Finale", air_date: "2024-05-15" }] } }
    } else if (request.method() === "POST" && path === "/api/v1/media") {
      const body = request.postDataJSON()
      created.push(body)
      data = { media: { id: scenario === "movie" ? "77777777-7777-4777-8777-777777777777" : "88888888-8888-4888-8888-888888888888", ...body }, workflows: [] }
      await route.fulfill({ status: 201, json: { data } })
      return
    } else if (request.method() === "GET" && path === "/api/v1/workflows?limit=300&active=false") data = []
    // The shell loads owner attention on every page.
    else if (request.method() === "GET" && path === "/api/v1/workflows?limit=300&active=true") data = []
    else if (request.method() === "GET" && path === "/api/v1/workflows?attention=true&limit=500&offset=0") data = []
    else if (request.method() === "GET" && path === "/api/v1/wanted?limit=300") data = []
    // The workflow table loads catalog artwork once.
    else if (request.method() === "GET" && path === "/api/v1/media?limit=200&offset=0") data = []
    else {
      unexpected.push(`${request.method()} ${path}`)
      await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
      return
    }
    await route.fulfill({ status: 200, json: { data } })
  })

  return { created, unexpected }
}

async function turnOffImmediateSearch(page: Page) {
  const searchImmediately = page.getByRole("switch", { name: "Search immediately" })
  await expect(searchImmediately).toHaveAttribute("data-state", "checked")
  await searchImmediately.click()
  await expect(searchImmediately).toHaveAttribute("data-state", "unchecked")
}

test("adds a verified movie without starting a search", async ({ page }) => {
  const audit = await installAddMediaAPI(page, "movie")
  await page.goto("/discover")

  await page.getByPlaceholder(/Search movies and TV series/).fill("Browser Add Movie")
  await page.getByRole("button", { name: "Search metadata" }).click()
  await page.getByRole("button", { name: /Browser Add Movie/ }).click()

  await expect(page.getByLabel("Title")).toHaveValue("Browser Add Movie")
  await expect(page.getByLabel("Release year")).toHaveValue("2024")
  await expect(page.getByLabel("TMDB ID")).toHaveValue("101")
  await turnOffImmediateSearch(page)
  await page.getByRole("button", { name: "Add without search" }).click()

  await expect(page).toHaveURL(/\/activity\/queue$/)
  await expect(page.getByRole("heading", { level: 1, name: "Workflows" })).toBeVisible()
  expect(audit.created).toHaveLength(1)
  expect(audit.created[0]).toEqual(expect.objectContaining({
    kind: "movie",
    title: "Browser Add Movie",
    year: 2024,
    search_immediately: false,
    identities: [
      { namespace: "tmdb", value: "101", state: "verified", source: "seerr_metadata" },
      { namespace: "imdb", value: "tt0000101", state: "verified", source: "seerr_metadata" },
    ],
    metadata: expect.objectContaining({ overview: "Verified local movie metadata.", original_language: "da", runtime_minutes: 91 }),
  }))
  expect(audit.created[0]).not.toHaveProperty("seasons")
  expect(audit.unexpected).toEqual([])
})

test("adds exact TV season packs without starting a search", async ({ page }) => {
  const audit = await installAddMediaAPI(page, "series")
  await page.goto("/discover")

  await page.getByPlaceholder(/Search movies and TV series/).fill("Browser Add Series")
  await page.getByRole("button", { name: "Search metadata" }).click()
  await page.getByRole("button", { name: /Browser Add Series/ }).click()

  await expect(page.getByLabel("Media type")).toContainText("TV series")
  await expect(page.getByLabel("Title")).toHaveValue("Browser Add Series")
  await expect(page.getByLabel("Release year")).toHaveValue("2023")
  await expect(page.getByLabel("TMDB ID")).toHaveValue("202")
  // The season box starts empty; nothing can be added until seasons are typed.
  await expect(page.getByLabel("Season numbers")).toHaveValue("")
  await expect(page.getByRole("button", { name: "Add and search" })).toBeDisabled()
  await page.getByLabel("Season numbers").fill("1, one")
  await expect(page.getByRole("status").filter({ hasText: "Use unique season numbers" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Add and search" })).toBeDisabled()
  await page.getByLabel("Season numbers").fill("1, 2")
  await turnOffImmediateSearch(page)
  await page.getByRole("button", { name: "Add without search" }).click()

  await expect(page).toHaveURL(/\/activity\/queue$/)
  await expect(page.getByRole("heading", { level: 1, name: "Workflows" })).toBeVisible()
  expect(audit.created).toHaveLength(1)
  expect(audit.created[0]).toEqual(expect.objectContaining({
    kind: "series",
    title: "Browser Add Series",
    year: 2023,
    search_immediately: false,
    identities: [
      { namespace: "tmdb", value: "202", state: "verified", source: "seerr_metadata" },
      { namespace: "tvdb", value: "302", state: "verified", source: "seerr_metadata" },
    ],
    metadata: expect.objectContaining({ overview: "Verified local series metadata.", season_count: 2, episode_count: 5 }),
    seasons: [
      {
        number: 1,
        monitored: true,
        search_immediately: false,
        prefer_season_pack: true,
        episodes: [
          { number: 1, provider_episode_id: "tvdb:1001", title: "Pilot", air_date: "2023-04-05" },
          { number: 2, provider_episode_id: "tvdb:1002", title: "Second", air_date: "2023-04-12" },
        ],
      },
      {
        number: 2,
        monitored: true,
        search_immediately: false,
        prefer_season_pack: true,
        episodes: [
          { number: 1, provider_episode_id: "tvdb:2001", title: "Return", air_date: "2024-05-01" },
          { number: 2, provider_episode_id: "tvdb:2002", title: "Middle", air_date: "2024-05-08" },
          { number: 3, provider_episode_id: "tvdb:2003", title: "Finale", air_date: "2024-05-15" },
        ],
      },
    ],
  }))
  expect(audit.unexpected).toEqual([])
})

// A manually entered series used to start with "1:10", so one click added a
// blank ten-episode season 1. The box now starts empty and is validated inline.
test("manual TV seasons start empty and must be valid before anything is added", async ({ page }) => {
  const audit = await installAddMediaAPI(page, "series")
  await page.goto("/discover")

  const mediaType = page.getByLabel("Media type")
  await mediaType.click()
  await page.getByRole("option", { name: "TV series" }).click()
  await expect(mediaType).toContainText("TV series")

  const seasons = page.getByLabel("Exact seasons and episode counts")
  const add = page.getByRole("button", { name: "Add and search" })
  await expect(seasons).toHaveValue("")
  await expect(add).toBeDisabled()

  await seasons.fill("1:10, 2")
  await expect(page.getByRole("status").filter({ hasText: "Use season:episode-count" })).toBeVisible()
  await expect(add).toBeDisabled()
  await seasons.fill("1:10, 1:8")
  await expect(page.getByRole("status").filter({ hasText: "must be unique" })).toBeVisible()
  await expect(add).toBeDisabled()

  await seasons.fill("1:10, 2:8")
  await expect(page.locator("#add-media-season-note")).toHaveCount(0)
  await expect(add).toBeEnabled()

  // Switching back to a movie and to TV again starts empty again.
  await mediaType.click()
  await page.getByRole("option", { name: "Movie" }).click()
  await mediaType.click()
  await page.getByRole("option", { name: "TV series" }).click()
  await expect(seasons).toHaveValue("")
  expect(audit.created).toEqual([])
  expect(audit.unexpected).toEqual([])
})
