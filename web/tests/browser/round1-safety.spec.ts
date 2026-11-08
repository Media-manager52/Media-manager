import { expect, test, type Page } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

test("library status failure keeps the filter, never claims success, and can be retried", async ({ page }) => {
  const writes = await mockApi(page)
  let failed = true
  await page.route("**/api/v1/media/status", (route) => failed
    ? route.fulfill({ status: 503, json: { error: { code: "database_error", message: "status unavailable" } } })
    : route.fulfill({ json: { data: [{ media_id: ids.movie, in_plex: false, missing: 1, attention: true }, { media_id: ids.series, in_plex: true, missing: 0, attention: false }] } }))
  await page.goto("/bibliotek?status=mangler")
  await expect(page.getByRole("radiogroup", { name: "Status" })).toBeVisible()
  await expect(page.getByRole("alert")).toContainText("Kunne ikke hente data")
  await expect(page.getByText("Intet mangler. Alt, der ventes på, ligger i Plex.")).toHaveCount(0)
  await expect(page.getByText("Ingen mangler i den indlæste status.")).toHaveCount(0)
  failed = false
  await page.getByRole("button", { name: "Prøv igen" }).click()
  await expect(page.getByRole("link", { name: /Testfilmen/ })).toBeVisible()
  await expect(page.getByRole("alert")).toHaveCount(0)
  expect(writes).toEqual([])
})

test("pending and incomplete status are unknown rather than an empty successful filter", async ({ page }) => {
  await mockApi(page)
  let release!: () => void
  const pending = new Promise<void>((resolve) => { release = resolve })
  await page.route("**/api/v1/media/status", async (route) => {
    await pending
    await route.fulfill({ json: { data: [] } })
  })
  await page.goto("/bibliotek?status=mangler")
  await expect(page.getByRole("radiogroup", { name: "Status" })).toBeVisible()
  await expect(page.getByText("Henter status…", { exact: true })).toBeVisible()
  await expect(page.getByText(/Alt, der ventes på|Ingen mangler i den indlæste/)).toHaveCount(0)
  release()
  await expect(page.getByText(/Status mangler for 2 titler/)).toBeVisible()
  await expect(page.getByText(/Alt, der ventes på|Ingen mangler i den indlæste/)).toHaveCount(0)
})

test("saved setup does not claim tested connections or a completed acquisition", async ({ page }) => {
  const writes = await mockApi(page)
  await page.route("**/api/v1/setup/status", (route) => route.fulfill({ json: { data: {
    complete: true, secret_storage_enabled: true, servarr_enabled: false, seerr_intake_enabled: false,
    steps: ["plex", "altmount", "indexer", "movie_root", "series_root", "movie_profile", "series_profile"].map((id) => ({ id, done: true, optional: false, detail: "Saved" })),
  } } }))
  await page.goto("/kom-i-gang")
  await expect(page.getByText("Den nødvendige opsætning er gemt")).toBeVisible()
  await expect(page.getByText(/Test forbindelserne/)).toBeVisible()
  await expect(page.getByText("Alt det nødvendige er klar")).toHaveCount(0)
  expect(writes).toEqual([])
})

async function seriesFiles(page: Page, tokens: string[]) {
  const writes = await mockApi(page)
  // Deliberately equal display names: identity must come from root_id/edition.
  const roots = [
    { id: "series-standard", media_kind: "series", name: "Serier", edition: "standard", enabled: true, is_default: true },
    { id: "series-4k", media_kind: "series", name: "Serier", edition: "4k", enabled: true, is_default: false },
  ]
  await page.route("**/api/v1/settings/library-roots", (route) => route.fulfill({ json: { data: roots } }))
  await page.route("**/api/v1/settings/quality-profiles", (route) => route.fulfill({ json: { data: [{ id: "series-profile", media_kind: "series", name: "Dansk", enabled: true, is_default: true }] } }))
  await page.route(`**/api/v1/media/${ids.series}/detail`, (route) => route.fulfill({ json: { data: {
    media: { id: ids.series, kind: "series", title: "Testserien", year: 2020, identities: [{ namespace: "tmdb", value: "77" }], metadata: { season_count: 1 } },
    workflows: [], seasons: [{ number: 1, expected_episodes: 2, episodes: [{ number: 1 }, { number: 2 }] }],
    files: tokens.map((token, index) => ({ id: `file-${index}`, media_id: ids.series, media_kind: "series", title: "Testserien", root_id: roots[0].id, root_name: roots[0].name, edition: "standard", state: "current", plex_confirmed: true, variant: "season:01", origin: "altmount", quality_evidence: {}, destination_relative: `Show (2020)/Season 01/Show (2020) - ${token}.mkv` })),
  } } }))
  await page.goto(`/bibliotek/${ids.series}`)
  await page.getByRole("button", { name: "Tilføj sæson", exact: true }).click()
  return writes
}

test("complete standard season never blocks an absent 4K edition", async ({ page }) => {
  const writes = await seriesFiles(page, ["S01E01", "S01E02"])
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("button", { name: "1 · i Plex", exact: true })).toBeDisabled()
  await dialog.getByLabel("Bibliotek").selectOption("series-4k")
  await expect(dialog.getByRole("button", { name: "1", exact: true })).toBeEnabled()
  expect(writes).toEqual([])
})

test("duplicate files cannot make a partial season look complete", async ({ page }) => {
  await seriesFiles(page, ["S01E01", "S01E01"])
  await expect(page.getByRole("dialog").getByRole("button", { name: "1", exact: true })).toBeEnabled()
})

test("one confirmed multi-episode file covers its exact episode range", async ({ page }) => {
  await seriesFiles(page, ["S01E01-E02"])
  await expect(page.getByRole("dialog").getByRole("button", { name: "1 · i Plex", exact: true })).toBeDisabled()
})
