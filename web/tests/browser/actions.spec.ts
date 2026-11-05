import AxeBuilder from "@axe-core/playwright"
import { expect, test } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

test("a workflow action asks first and then sends exactly the server's request", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/handling")
  await page.getByRole("button", { name: "Tjek AltMount igen" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Henter ikke en ny NZB")
  expect(writes).toEqual([])
  await dialog.getByRole("button", { name: "Tjek AltMount igen" }).click()
  await expect(page.getByRole("status").filter({ hasText: "sat i gang" })).toBeVisible()
  expect(writes).toEqual([{ method: "POST", path: `/api/v1/workflows/${ids.attention}/reconcile-altmount`, body: null }])
})

test("unavailable actions are disabled and explain why", async ({ page }) => {
  await mockApi(page)
  await page.goto(`/arbejde/${ids.attention}`)
  await expect(page.getByRole("button", { name: "Søg nu" })).toBeDisabled()
  await expect(page.getByText("AltMount arbejder stadig på et job. Vent, til det er færdigt.")).toBeVisible()
})

test("cancelling can be abandoned without sending anything", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto(`/arbejde/${ids.attention}`)
  await page.getByRole("button", { name: "Annullér" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Fortryd" }).click()
  await expect(page.getByRole("dialog")).toBeHidden()
  expect(writes).toEqual([])
})

test("a pending Seerr request can be approved after confirming", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/anmodninger")
  await page.getByRole("radio", { name: /Skal godkendes/ }).click()
  await page.getByRole("button", { name: "Godkend" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Godkend" }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ method: "POST", path: "/api/v1/requests/req-1/approve" })
})

test("adding a movie sends verified identities from the detail lookup", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/tilfoej")
  await page.getByLabel("Titel").fill("Nyfilmen")
  await page.getByRole("button", { name: "Søg", exact: true }).click()
  await page.getByRole("button", { name: /Nyfilmen/ }).click()
  await page.getByRole("button", { name: "Tilføj film" }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({
    method: "POST",
    path: "/api/v1/media",
    body: { kind: "movie", title: "Nyfilmen", year: 2025, search_immediately: true, identities: [{ namespace: "tmdb", value: "99", state: "verified", source: "seerr_metadata" }] },
  })
  await expect(page).toHaveURL(new RegExp(`/bibliotek/${ids.movie}$`))
})

test("adding a title that is already in the library opens it instead", async ({ page }) => {
  await mockApi(page)
  await page.route("**/api/v1/media", (route) => route.request().method() === "POST"
    ? route.fulfill({ status: 409, json: { error: { code: "media_exists", message: "this title is already in the library", hint: `Open GET /api/v1/media/${ids.movie} and add searches or seasons there.` } } })
    : route.fallback())
  await page.goto("/tilfoej")
  await page.getByLabel("Titel").fill("Nyfilmen")
  await page.getByRole("button", { name: "Søg", exact: true }).click()
  await page.getByRole("button", { name: /Nyfilmen/ }).click()
  await page.getByRole("button", { name: "Tilføj film" }).click()
  await expect(page.getByRole("status").filter({ hasText: "Nyfilmen er allerede i biblioteket" })).toBeVisible()
  await expect(page).toHaveURL(new RegExp(`/bibliotek/${ids.movie}$`))
})

test("library and profile are chosen from described choices", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/tilfoej")
  await page.getByLabel("Titel").fill("Nyfilmen")
  await page.getByRole("button", { name: "Søg", exact: true }).click()
  await page.getByRole("button", { name: /Nyfilmen/ }).click()
  await page.getByRole("button", { name: /^Bibliotek/ }).click()
  const libraries = page.getByRole("dialog", { name: "Bibliotek" })
  await expect(libraries.getByRole("radio", { name: /Automatisk/ })).toHaveAttribute("aria-checked", "true")
  await expect(libraries).toContainText("Lige nu: Film.")
  const axe = await new AxeBuilder({ page }).include("dialog[open]").withTags(["wcag2a", "wcag2aa"]).analyze()
  expect(axe.violations).toEqual([])
  await libraries.getByRole("radio", { name: /^Film 4K/ }).click()
  await expect(libraries).toBeHidden()
  await expect(page.getByRole("button", { name: /^Bibliotek Film 4K/ })).toBeVisible()
  await page.getByRole("button", { name: /^Kvalitetsprofil/ }).click()
  const profiles = page.getByRole("dialog", { name: "Kvalitetsprofil" })
  await expect(profiles).toContainText("Kræver danske undertekster eller tale")
  await profiles.getByRole("radio", { name: /^Movie Danish Subs/ }).click()
  await page.getByRole("button", { name: "Tilføj film" }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ method: "POST", path: "/api/v1/media", body: { root_id: "root-4k", quality_profile_id: ids.profile } })
})

test("deleting a Seerr rule needs a confirmation", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/indstillinger/seerr")
  await page.getByRole("button", { name: "Slet reglen for /media/Movies" }).click()
  expect(writes).toEqual([])
  await page.getByRole("dialog").getByRole("button", { name: "Slet regel" }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ method: "DELETE", path: `/api/v1/settings/integrations/${ids.seerr}/seerr-routes/route-1` })
})

test("the theme choice is remembered", async ({ page }) => {
  await mockApi(page)
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto("/")
  await page.getByRole("radio", { name: "Mørkt tema" }).click()
  await page.reload()
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark")
})

test("adding Plex saves the token encrypted and tests the connection", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/indstillinger/forbindelser")
  await page.getByRole("button", { name: "Tilføj manuelt" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Plex-token").fill("plex-token-123")
  await dialog.getByRole("button", { name: "Tilføj og test" }).click()
  await expect(dialog.getByText("Forbindelsen virker.", { exact: false })).toBeVisible()
  expect(writes.map((write) => `${write.method} ${write.path}`)).toEqual([
    "POST /api/v1/settings/integrations",
    `PUT /api/v1/settings/integrations/${ids.attention}/secret`,
    `POST /api/v1/settings/integrations/${ids.attention}/check`,
  ])
  expect(writes[0].body).toMatchObject({ kind: "plex", is_default: false, config: { base_url: "http://plex:32400", allow_http: true } })
  expect(writes[1].body).toEqual({ value: "plex-token-123" })
})

test("Log ind med Plex connects the server Media Manager found", async ({ page, context }) => {
  const writes = await mockApi(page)
  await context.route("https://app.plex.tv/**", (route) => route.fulfill({ contentType: "text/html", body: "<title>Plex</title>" }))
  await page.goto("/indstillinger/forbindelser")
  const [popup] = await Promise.all([page.waitForEvent("popup"), page.getByRole("button", { name: "Log ind med Plex" }).click()])
  await expect.poll(() => popup.url()).toContain("app.plex.tv/auth")
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText("Vælg din server")).toBeVisible()
  await expect(dialog.getByRole("radio", { name: /Plex-containeren/ })).toBeChecked()
  await dialog.getByRole("button", { name: "Forbind" }).click()
  await expect(dialog.getByText("Plex er tilføjet.")).toBeVisible()
  await expect(dialog.getByText("Forbindelsen virker.", { exact: false })).toBeVisible()
  expect(writes.map((write) => `${write.method} ${write.path}`)).toEqual(["POST /api/v1/setup/plex/login", "POST /api/v1/setup/plex/login/login1/connect"])
  expect(writes[1].body).toEqual({ server_id: "machine-1", uri: "http://plex:32400" })
})

test("a library folder is added by picking a Plex library", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/indstillinger/biblioteker")
  await page.getByRole("button", { name: "Tilføj mappe" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Plex-bibliotek").selectOption("7")
  await expect(dialog.getByLabel("Mappen som Media Manager ser den")).toHaveValue("/media/Film 4K")
  await dialog.getByRole("button", { name: "Tilføj", exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ method: "POST", path: "/api/v1/settings/library-roots", body: { media_kind: "movie", name: "Film 4K", plex_instance_id: ids.plex, plex_library_id: "7", plex_visible_root: "/media/Film 4K", edition: "4k", is_default: false } })
})

test("a 4K folder can share a Plex library with the 1080p folder", async ({ page }) => {
  const writes = await mockApi(page)
  await page.route("**/api/v1/settings/library-roots", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await route.fulfill({ json: { data: [{ id: ids.root, media_kind: "movie", name: "Movies", destination_root: "/media/movies/1080p", plex_visible_root: "/media/movies/1080p", source_visible_root: "/mnt/altmount", plex_instance_id: ids.plex, plex_library_id: "12", is_default: true, enabled: true, edition: "standard" }] } })
  })
  await page.route("**/api/v1/settings/integrations/*/plex-libraries", (route) => route.fulfill({ json: { data: [
    { key: "12", title: "Movies", type: "movie", locations: ["/media/movies/1080p", "/media/movies/2160p"] },
    { key: "13", title: "Klassikere", type: "movie", locations: ["/media/klassikere"] },
    { key: "14", title: "Fælles", type: "movie", locations: ["/media/film/2160p", "/media/film/1080p"] },
  ] } }))
  await page.goto("/indstillinger/biblioteker")
  await page.getByRole("button", { name: "Tilføj mappe" }).click()
  const dialog = page.getByRole("dialog")
  // The library is still free: only its 1080p folder is in use.
  await expect(dialog.getByRole("option", { name: "Movies", exact: true })).toBeEnabled()
  await expect(dialog.getByRole("option", { name: "Fælles", exact: true })).toBeEnabled()
  // A fresh library with 2160p listed first still starts with its 1080p folder.
  await dialog.getByLabel("Plex-bibliotek").selectOption("14")
  await expect(dialog.getByLabel("Mappen som Plex ser den")).toHaveValue("/media/film/1080p")
  await expect(dialog.getByRole("radio", { name: "Almindelig" })).toBeChecked()
  await dialog.getByLabel("Mappen som Plex ser den").selectOption("/media/film/2160p")
  await expect(dialog.getByRole("radio", { name: /^4K/ })).toBeChecked()
  await dialog.getByLabel("Plex-bibliotek").selectOption("12")
  await expect(dialog.getByLabel("Mappen som Plex ser den")).toHaveValue("/media/movies/2160p")
  await expect(dialog.getByRole("option", { name: "/media/movies/1080p (i brug)" })).toHaveAttribute("disabled", "")
  await expect(dialog.getByRole("radio", { name: /^4K/ })).toBeChecked()
  await expect(dialog.getByLabel("Navn i Media Manager")).toHaveValue("Movies 4K")
  await expect(dialog.getByText("Plex samler så de to filer som to versioner af samme film.")).toBeVisible()
  await dialog.getByRole("button", { name: "Tilføj", exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ method: "POST", path: "/api/v1/settings/library-roots", body: { media_kind: "movie", name: "Movies 4K", plex_library_id: "12", plex_visible_root: "/media/movies/2160p", destination_root: "/media/movies/2160p", edition: "4k", is_default: false } })
})

test("Brug anbefalet opsætning shows the proposal before creating it", async ({ page }) => {
  const writes = await mockApi(page)
  let created = false
  // After creating, every library step is done; the result must stay visible.
  await page.route("**/api/v1/setup/status", (route) => route.fulfill({ json: { data: { complete: false, secret_storage_enabled: true, servarr_enabled: false, seerr_intake_enabled: false, steps: [
    { id: "plex", done: true, optional: false, detail: "Plex" }, { id: "altmount", done: true, optional: false, detail: "AltMount" }, { id: "indexer", done: true, optional: false, detail: "" },
    { id: "movie_root", done: created, optional: false, detail: "" }, { id: "series_root", done: created, optional: false, detail: "" },
    { id: "movie_profile", done: created, optional: false, detail: "" }, { id: "series_profile", done: true, optional: false, detail: "" },
  ] } } }))
  const root = { media_kind: "movie", name: "Film", destination_root: "/libraries/movies", plex_visible_root: "/data/movies", source_visible_root: "/mnt/altmount", is_default: true, enabled: true }
  await page.route("**/api/v1/setup/recommended", async (route) => {
    const apply = JSON.parse(route.request().postData() ?? "{}").apply === true
    created = created || apply
    writes.push({ method: "POST", path: "/api/v1/setup/recommended", body: { apply } })
    await route.fulfill({ json: { data: { applied: apply, steps: [
      { id: "movie_root", state: apply ? "created" : "create", name: "Film", root, source_proven: true },
      { id: "movie_profile", state: apply ? "created" : "create", name: "Dansk tekst eller tale" },
      { id: "series_root", state: "blocked", reason: "no_plex_library" },
      { id: "series_profile", state: "exists", name: "Dansk tekst eller tale" },
    ] } } })
  })
  await page.goto("/kom-i-gang")
  await page.getByRole("button", { name: "Vis forslag" }).click()
  await expect(page.getByText("Plex-biblioteket \"Film\" i /data/movies; Media Manager lægger filerne i /libraries/movies.", { exact: false })).toBeVisible()
  await expect(page.getByText("Plex har intet seriebibliotek", { exact: false })).toBeVisible()
  expect(writes).toEqual([{ method: "POST", path: "/api/v1/setup/recommended", body: { apply: false } }])
  await page.getByRole("button", { name: "Opret det" }).click()
  await expect(page.getByText("Oprettet \"Dansk tekst eller tale\" som standard", { exact: false })).toBeVisible()
  await expect(page.getByRole("heading", { name: "7 af 7 trin klaret" })).toBeVisible()
  await expect(page.getByText("Færdig.", { exact: false })).toBeVisible()
  expect(writes.map((write) => write.body)).toEqual([{ apply: false }, { apply: true }])
})

test("picking a known indexer fills its name and API address", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/kilder")
  await page.getByRole("button", { name: "Tilføj indexer" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("combobox", { name: /^Indexer/ }).selectOption("NZBgeek")
  await expect(dialog.getByLabel("Navn", { exact: true })).toHaveValue("NZBgeek")
  await expect(dialog.getByLabel("API-adresse")).toHaveValue("https://api.nzbgeek.info/api")
  await dialog.getByLabel("API-nøgle").fill("geek-key")
  await dialog.getByRole("button", { name: "Tilføj og godkend" }).click()
  await expect.poll(() => writes.length).toBeGreaterThan(0)
  expect(writes[0]).toMatchObject({ method: "POST", path: "/api/v1/settings/integrations", body: { kind: "newznab", name: "NZBgeek", config: { base_url: "https://api.nzbgeek.info/api" } } })
})

test("the setup checklist links to what is missing", async ({ page }) => {
  await mockApi(page)
  await page.goto("/kom-i-gang")
  await expect(page.getByRole("heading", { name: "4 af 7 trin klaret" })).toBeVisible()
  await page.getByRole("link", { name: "Sæt op: Forbind AltMount" }).click()
  await expect(page).toHaveURL(/\/indstillinger\/forbindelser$/)
})

const movieOnDisk = {
  media: { id: ids.movie, kind: "movie", title: "Testfilmen", year: 2024, identities: [{ namespace: "tmdb", value: "1" }], metadata: {} },
  workflows: [{ id: ids.attention, media_id: ids.movie, media_kind: "movie", title: "Testfilmen", variant: "default", edition: "standard", root_id: ids.root, state: "plex_confirmed", revision: 3, terminal_at: "2026-11-03T12:00:00Z", adopted_replacement_confirmation_required: false, created_at: "2026-11-03T12:00:00Z", updated_at: "2026-11-03T12:00:00Z", available_actions: [] }],
  seasons: [],
  files: [{ id: "f1", media_id: ids.movie, media_kind: "movie", title: "Testfilmen", root_name: "Film", destination_relative: "Testfilmen (2024)/Testfilmen (2024).mkv", source_relative: "x", plex_visible_target: "/mnt/altmount/x", state: "current", generation: 1, plex_confirmed: true, variant: "default", origin: "altmount", quality_profile: "Movie Danish Subs", quality_evidence_state: "media_info", quality_evidence: {} }],
}

async function titleOnDisk(page: import("@playwright/test").Page) {
  await page.route(`**/api/v1/media/${ids.movie}/detail`, (route) => route.fulfill({ json: { data: movieOnDisk } }))
  await page.route("**/api/v1/settings/library-roots", (route) => route.fulfill({ json: { data: [
    { id: ids.root, media_kind: "movie", name: "Film", destination_root: "/media/Movies", plex_visible_root: "/media/Movies", source_visible_root: "/mnt/altmount", is_default: true, enabled: true },
    { id: "root-kids", media_kind: "movie", name: "Børnefilm", destination_root: "/media/Kids", plex_visible_root: "/media/Kids", source_visible_root: "/mnt/altmount", is_default: false, enabled: true },
    { id: "root-4k", media_kind: "movie", name: "Film 4K", destination_root: "/media/Film 4K", plex_visible_root: "/media/Film 4K", source_visible_root: "/mnt/altmount", edition: "4k", is_default: false, enabled: true },
  ] } }))
}

test("a title is moved to another library after choosing it", async ({ page }) => {
  const writes = await mockApi(page)
  await titleOnDisk(page)
  await page.goto(`/bibliotek/${ids.movie}`)
  await page.getByRole("button", { name: "Flyt", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Flyt Testfilmen" })
  await expect(dialog).toContainText("Ligger nu i: Film")
  await expect(dialog.getByRole("radio", { name: /Film 4K/ })).toHaveCount(0)
  await expect(dialog.getByRole("radio", { name: /^Film/ })).toBeDisabled()
  expect(writes).toEqual([])
  await dialog.getByRole("radio", { name: /Børnefilm/ }).check()
  await dialog.getByRole("button", { name: "Flyt", exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toEqual({ method: "POST", path: `/api/v1/media/${ids.movie}/relocate`, body: { root_id: "root-kids" } })
  await expect(page.getByRole("status").filter({ hasText: "er flyttet til Børnefilm" })).toBeVisible()
})

test("deleting a title asks first and puts it in the recycle bin", async ({ page }) => {
  const writes = await mockApi(page)
  await titleOnDisk(page)
  const deletes: string[] = []
  page.on("request", (request) => { if (request.method() === "DELETE") deletes.push(request.url()) })
  await page.goto(`/bibliotek/${ids.movie}`)
  await page.getByRole("button", { name: "Slet", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Slet Testfilmen?" })
  await expect(dialog).toContainText("papirkurven i 30 dage")
  await expect(dialog).toContainText("Selve filen i AltMount bliver ikke slettet")
  expect(writes).toEqual([])
  await dialog.getByRole("button", { name: "Slet titel" }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ method: "DELETE", path: `/api/v1/media/${ids.movie}` })
  expect(deletes[0]).toContain("cancel_open_work=true")
  await expect(page).toHaveURL(/\/bibliotek$/)
})

test("a deleted title can be restored from the recycle bin", async ({ page }) => {
  const writes = await mockApi(page)
  await page.route("**/api/v1/recycle", (route) => route.fulfill({ json: { data: [
    { id: "e1", cycle_id: "c1", media_id: ids.movie, media_title: "Testfilmen", root_id: ids.root, entry_kind: "link", purpose: "media_delete", original_relative: "a", recycle_relative: "b", altmount_source_retained: true, purge_after: "2026-12-03T12:00:00Z", state: "retained", confirmation_required: false, restore_attempts: 0, created_at: "2026-11-03T12:00:00Z" },
    { id: "e2", cycle_id: "c0", media_id: ids.series, media_title: "Gammel", root_id: ids.root, entry_kind: "link", purpose: "media_delete", original_relative: "a", recycle_relative: "b", altmount_source_retained: true, purge_after: "2026-12-01T12:00:00Z", state: "restored", confirmation_required: false, restore_attempts: 0, created_at: "2026-11-01T12:00:00Z" },
  ] } }))
  await page.goto("/system/papirkurv")
  await expect(page.getByText("Testfilmen")).toBeVisible()
  await expect(page.getByText("Gammel")).toHaveCount(0)
  await page.getByRole("button", { name: "Gendan" }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ method: "POST", path: "/api/v1/recycle/e1/restore" })
})

const seriesRoot = { id: "root-series", media_kind: "series", name: "Serier", destination_root: "/media/Serier", plex_visible_root: "/media/Serier", source_visible_root: "/mnt/altmount", is_default: true, enabled: true, edition: "standard" }
const seriesProfile = { id: "profile-series", media_kind: "series", name: "Serie dansk tekst", policy_version: 1, policy: {}, upgrades_enabled: true, is_default: true, enabled: true }

test("Tilføj sæson adds a new season next to the series' own folder and profile", async ({ page }) => {
  const writes = await mockApi(page)
  const openSeason = { id: "wf-s1", media_id: ids.series, media_kind: "series", title: "Testserien", variant: "season:01", edition: "standard", state: "waiting", revision: 1, root_id: seriesRoot.id, quality_profile_id: seriesProfile.id, adopted_replacement_confirmation_required: false, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", available_actions: [] }
  await page.route("**/api/v1/settings/library-roots", (route) => route.fulfill({ json: { data: [seriesRoot] } }))
  await page.route("**/api/v1/settings/quality-profiles", (route) => route.fulfill({ json: { data: [seriesProfile] } }))
  await page.route(`**/api/v1/media/${ids.series}/detail`, (route) => route.fulfill({ json: { data: {
    media: { id: ids.series, kind: "series", title: "Testserien", year: 2020, identities: [{ namespace: "tmdb", value: "77" }], metadata: { season_count: 3, genres: [], countries: [], networks: [], aliases: [], sources: [], ratings: [], events: [], observed_at: "2026-01-01T00:00:00Z" } },
    workflows: [openSeason], seasons: [], files: [],
  } } }))
  await page.route("**/api/v1/discovery/tv/77?season=3", (route) => route.fulfill({ json: { data: { kind: "series", title: "Testserien", tmdb_id: 77, identities: [], enrichment_state: "tmdb_via_seerr", provider_states: {}, season: { number: 3, episodes: [{ number: 1, provider_id: "e1", title: "Et", air_date: "2026-01-01" }, { number: 2, provider_id: "e2", title: "To", air_date: "2026-01-08" }] } } } }))
  await page.goto(`/bibliotek/${ids.series}`)
  await page.getByRole("button", { name: "Tilføj sæson" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("button", { name: "1 · i gang" })).toBeDisabled()
  await dialog.getByRole("button", { name: "3", exact: true }).click()
  await expect(dialog.getByLabel("Bibliotek")).toHaveValue(seriesRoot.id)
  await expect(dialog.getByLabel("Kvalitetsprofil")).toHaveValue(seriesProfile.id)
  await dialog.getByRole("button", { name: "Tilføj sæson" }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toEqual({ method: "POST", path: `/api/v1/media/${ids.series}/workflows`, body: {
    root_id: seriesRoot.id, quality_profile_id: seriesProfile.id, search_immediately: true,
    season: { number: 3, monitored: true, search_immediately: true, prefer_season_pack: true, episodes: [{ number: 1, provider_episode_id: "e1", title: "Et", air_date: "2026-01-01" }, { number: 2, provider_episode_id: "e2", title: "To", air_date: "2026-01-08" }] },
  } })
  await expect(page.getByRole("status").filter({ hasText: "Sæson 3 er tilføjet, og søgningen starter nu." })).toBeVisible()
})

test("Hent starts a film again and explains a refusal in Danish", async ({ page }) => {
  const writes = await mockApi(page)
  await page.route(`**/api/v1/media/${ids.movie}/detail`, (route) => route.fulfill({ json: { data: {
    media: { id: ids.movie, kind: "movie", title: "Testfilmen", year: 2024, identities: [{ namespace: "tmdb", value: "1" }] }, workflows: [], seasons: [], files: [],
  } } }))
  let refuse = true
  await page.route(`**/api/v1/media/${ids.movie}/workflows`, async (route) => {
    writes.push({ method: "POST", path: new URL(route.request().url()).pathname, body: JSON.parse(route.request().postData() ?? "null") })
    if (refuse) {
      refuse = false
      return route.fulfill({ status: 409, json: { error: { code: "no_commissioned_indexer", message: "search requires at least one commissioned indexer for this media" } } })
    }
    return route.fulfill({ status: 201, json: { data: { id: "wf-new" } } })
  })
  await page.goto(`/bibliotek/${ids.movie}`)
  await page.getByRole("button", { name: "Hent", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText("Media Manager søger efter filmen")).toBeVisible()
  await dialog.getByRole("button", { name: "Hent filmen" }).click()
  await expect(dialog.getByRole("alert")).toHaveText("Ingen indexer er klar til at søge. Tilføj eller slå en til under Kilder.")
  await dialog.getByRole("button", { name: "Hent filmen" }).click()
  await expect(page.getByRole("status").filter({ hasText: "Testfilmen er sat i gang, og søgningen starter nu." })).toBeVisible()
  expect(writes.map((write) => write.body)).toEqual([
    { root_id: ids.root, quality_profile_id: ids.profile, search_immediately: true },
    { root_id: ids.root, quality_profile_id: ids.profile, search_immediately: true },
  ])
})

test("the library shows each title's status and filters on it", async ({ page }) => {
  await mockApi(page)
  await page.goto("/bibliotek")
  const movie = page.getByRole("link", { name: /Testfilmen/ })
  await expect(movie.getByText("Kræver handling")).toBeVisible()
  await page.getByRole("radiogroup", { name: "Status" }).getByRole("radio", { name: /Kræver handling/ }).click()
  await expect(page).toHaveURL(/status=handling/)
  await expect(movie).toBeVisible()
  await expect(page.getByRole("link", { name: /Testserien/ })).toHaveCount(0)
  await page.getByRole("radiogroup", { name: "Status" }).getByRole("radio", { name: /Ikke hentet/ }).click()
  await expect(page.getByText("Alle titler har noget i Plex eller er i gang.")).toBeVisible()
})
