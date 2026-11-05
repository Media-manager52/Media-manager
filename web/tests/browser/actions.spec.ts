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
  await expect(page.getByText("An AltMount job is active; wait for it to settle.")).toBeVisible()
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
