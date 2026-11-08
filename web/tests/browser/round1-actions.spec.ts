import { expect, test, type Page } from "@playwright/test"

import { ids, mockApi, type Recorded } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

const file = {
  id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", media_id: ids.movie, media_kind: "movie", title: "Testfilmen", root_name: "Film", origin: "altmount", state: "current", plex_confirmed: true,
  destination_relative: "Testfilmen (2024)/Testfilmen (2024).mkv", source_relative: "media-manager/release a/Movie [DA].mkv", quality_evidence: {},
}

async function publishedFiles(page: Page, files = [file]) {
  const writes = await mockApi(page)
  await page.route(`**/api/v1/media/${ids.movie}/detail`, (route) => route.fulfill({ json: { data: {
    media: { id: ids.movie, kind: "movie", title: "Testfilmen", year: 2024, identities: [], metadata: {} }, workflows: [], seasons: [], files,
  } } }))
  return writes
}

test("replacement can be abandoned without any write", async ({ page }) => {
  const writes = await publishedFiles(page)
  await page.goto(`/bibliotek/${ids.movie}`)
  await page.getByRole("button", { name: "Erstat dårlig fil", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText(file.destination_relative)
  await expect(dialog).toContainText("Den nuværende fil beholdes")
  await expect(dialog).toContainText("Find bedre udgave")
  expect(writes).toEqual([])
  await dialog.getByRole("button", { name: "Fortryd" }).click()
  await expect(dialog).toBeHidden()
  expect(writes).toEqual([])
})

for (const [outcome, message] of [
  ["opened", "Søgningen efter en erstatning er sat i gang."],
  ["attached", "Der søges allerede efter en erstatning til titlen."],
  ["already_open", "Der er allerede en erstatningssag for denne fil."],
  ["needs_attention", "Filen kan ikke erstattes automatisk."],
] as const) {
  test(`replacement reports ${outcome} and sends the selected immutable file identity`, async ({ page }) => {
    const writes = await publishedFiles(page)
    await page.route("**/api/v1/repairs", (route) => {
      if (route.request().method() === "GET") return route.fallback()
      writes.push({ method: "POST", path: "/api/v1/repairs", body: route.request().postDataJSON() })
      return route.fulfill({ json: { data: { outcome } } })
    })
    await page.goto(`/bibliotek/${ids.movie}`)
    await page.getByRole("button", { name: "Erstat dårlig fil", exact: true }).click()
    expect(writes).toEqual([])
    await page.getByRole("dialog").getByRole("button", { name: "Søg efter erstatning" }).click()
    await expect(page.getByRole("status").filter({ hasText: message })).toBeVisible()
    expect(writes).toEqual([{ method: "POST", path: "/api/v1/repairs", body: { file_id: file.id, altmount_file_path: file.source_relative } }])
    await expect(page.getByText(file.destination_relative, { exact: true })).toBeVisible()
    await expect(page.getByRole("link", { name: "Se status", exact: true })).toHaveAttribute("href", "/system/reparationer")
  })
}

for (const [status, code, message] of [
  [404, "not_published", "Filen er ikke længere en aktuel AltMount-fil"],
  [409, "publication_in_flight", "Filen er stadig ved at blive udgivet"],
] as const) {
  test(`replacement explains HTTP ${status} without claiming success`, async ({ page }) => {
    const writes = await publishedFiles(page)
    await page.route("**/api/v1/repairs", (route) => {
      writes.push({ method: "POST", path: "/api/v1/repairs", body: route.request().postDataJSON() })
      return route.fulfill({ status, json: { error: { code, message: "English server detail" } } })
    })
    await page.goto(`/bibliotek/${ids.movie}`)
    await page.getByRole("button", { name: "Erstat dårlig fil", exact: true }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Søg efter erstatning" }).click()
    await expect(page.getByRole("alert")).toContainText(message)
    expect(writes).toHaveLength(1)
    await expect(page.getByRole("dialog")).toBeVisible()
  })
}

test("adopted files and publications without Plex confirmation cannot use AltMount repair", async ({ page }) => {
  const writes = await publishedFiles(page, [{ ...file, origin: "adoption" }, { ...file, id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", plex_confirmed: false }])
  await page.goto(`/bibliotek/${ids.movie}`)
  await expect(page.getByRole("heading", { name: "Filer i biblioteket" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Erstat dårlig fil", exact: true })).toHaveCount(0)
  expect(writes).toEqual([])
})

async function addSeries(page: Page): Promise<{ writes: Recorded[]; snapshots: number[] }> {
  const writes = await mockApi(page)
  const snapshots: number[] = []
  await page.route("**/api/v1/discovery/search?**", (route) => route.fulfill({ json: { data: { results: [{ id: 77, mediaType: "tv", name: "Nyserie", firstAirDate: "2024-01-01" }], page: 1, totalPages: 1, totalResults: 1 } } }))
  await page.route("**/api/v1/discovery/tv/77*", (route) => {
    const number = Number(new URL(route.request().url()).searchParams.get("season") ?? 0)
    if (number) snapshots.push(number)
    return route.fulfill({ json: { data: { kind: "series", title: "Nyserie", year: 2024, tmdb_id: 77, identities: [{ namespace: "tmdb", value: "77", source: "tmdb" }], metadata: { season_count: 3 }, ...(number ? { season: { number, episodes: [{ number: 1, provider_id: `s${number}e1`, title: "Et", air_date: "2024-01-01" }] } } : {}) } } })
  })
  await page.goto("/tilfoej")
  await page.getByLabel("Titel").fill("Nyserie")
  await page.getByRole("button", { name: "Søg", exact: true }).click()
  await page.getByRole("button", { name: /Nyserie/ }).click()
  return { writes, snapshots }
}

for (const mode of ["latest", "custom", "all"] as const) {
  test(`series intake ${mode} has deliberate scope and exact frozen episode snapshots`, async ({ page }) => {
    const { writes, snapshots } = await addSeries(page)
    const expected = mode === "latest" ? [3] : mode === "custom" ? [1, 3] : [1, 2, 3]
    await expect(page.getByRole("radio", { name: "Seneste sæson", exact: true })).toBeChecked()
    if (mode === "custom") {
      await page.getByRole("radio", { name: "Vælg sæsoner", exact: true }).check()
      await expect(page.getByRole("button", { name: "Tilføj serie", exact: true })).toBeDisabled()
      for (const number of expected) await page.getByRole("button", { name: String(number), exact: true }).click()
    }
    if (mode === "all") await page.getByRole("radio", { name: "Alle sæsoner", exact: true }).check()
    await expect(page.getByText("Nye sæsoner tilføjes ikke automatisk.", { exact: false })).toBeVisible()
    await expect(page.getByRole("status").filter({ hasText: "Valgt:" })).toContainText(`(${expected.length} ${expected.length === 1 ? "sæson" : "sæsoner"})`)
    if (mode === "all") await page.getByRole("checkbox", { name: "Begynd at søge med det samme" }).uncheck()
    expect(writes).toEqual([])
    await page.getByRole("button", { name: "Tilføj serie", exact: true }).click()
    await expect.poll(() => writes.length).toBe(1)
    expect(snapshots).toEqual(expected)
    expect(writes[0]).toMatchObject({ method: "POST", path: "/api/v1/media", body: {
      kind: "series", search_immediately: mode !== "all", seasons: expected.map((number) => ({ number, monitored: true, search_immediately: mode !== "all", prefer_season_pack: true, episodes: [{ number: 1, provider_episode_id: `s${number}e1`, title: "Et", air_date: "2024-01-01" }] })),
    } })
  })
}
