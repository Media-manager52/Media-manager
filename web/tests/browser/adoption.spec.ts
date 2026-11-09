import { expect, test, type Page } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

const root = { id: "root1", media_kind: "movie", name: "Film", destination_root: "/libraries/movies", plex_visible_root: "/media/Film", source_visible_root: "/mnt", is_default: true, enabled: true, edition: "standard" }
const profile = { id: "p1", media_kind: "movie", name: "Dansk", policy_version: 1, policy: { rules: [] }, upgrades_enabled: true, is_default: true, enabled: true }
const movie = { id: ids.movie, kind: "movie", title: "Testfilmen", year: 2024, identities: [] }
const item = { id: "i1", batch_id: "b1", original_relative: "Testfilmen (2024)/Testfilmen.2024.mkv", recognized_companions: ["Testfilmen.2024.da.srt"], unknown_companions: ["noter.txt"], review_state: "needs_attention", execution_state: "pending" }
const batch = (patch: object) => ({ id: "b1", source_root: "/libraries/movies", media_kind: "movie", default_root_id: "root1", default_quality_profile_id: "p1", monitoring_enabled: false, search_immediately: false, replacement_search_authorized: false, state: "review", created_at: "2026-11-09T00:00:00Z", pending_items: 0, approved_items: 0, rejected_items: 0, attention_items: 1, items: [item], ...patch })
const approvedItem = { ...item, review_state: "approved", media_id: ids.movie, proposed_relative: "Testfilmen (2024)/Testfilmen.mkv" }

async function open(page: Page, current: () => object, path = "/indstillinger/overtag?kladde=b1") {
  await mockApi(page)
  const sent: Array<{ path: string; body: unknown }> = []
  await page.route("**/api/v1/settings/library-roots", (route) => route.fulfill({ json: { data: [root] } }))
  await page.route("**/api/v1/settings/quality-profiles", (route) => route.fulfill({ json: { data: [profile] } }))
  await page.route("**/api/v1/media?**", (route) => route.fulfill({ json: { data: [movie] } }))
  await page.route("**/api/v1/adoption**", (route) => {
    const url = new URL(route.request().url())
    if (route.request().method() !== "GET") {
      sent.push({ path: url.pathname, body: route.request().postDataJSON() })
      return route.fulfill({ status: url.pathname.endsWith("/adoption") ? 201 : 200, json: { data: current() } })
    }
    return route.fulfill({ json: { data: url.pathname.endsWith("/adoption") ? [current()] : current() } })
  })
  await page.goto(path)
  return sent
}

test("the scan only reads and sends no search", async ({ page }) => {
  const sent = await open(page, () => batch({}), "/indstillinger/overtag")
  await page.getByRole("button", { name: /^Bibliotek/ }).click()
  await page.getByRole("radio", { name: /Film/ }).click()
  await page.getByRole("button", { name: /^Kvalitetsprofil/ }).click()
  await page.getByRole("radio", { name: /Dansk/ }).click()
  await expect(page.getByRole("textbox", { name: "Mappe" })).toHaveValue("/libraries/movies")
  await page.getByRole("button", { name: "Find filer" }).click()
  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0]).toEqual({ path: "/api/v1/adoption", body: { source_root: "/libraries/movies", root_id: "root1", quality_profile_id: "p1", monitoring_enabled: false, search_immediately: false } })
})

test("each file is reviewed with a suggested title and its companions are shown", async ({ page }) => {
  const sent = await open(page, () => batch({}))
  await expect(page.getByText("Følger med: Testfilmen.2024.da.srt")).toBeVisible()
  await expect(page.getByText("Ukendte filer, som bliver liggende uændret: noter.txt")).toBeVisible()
  const select = page.getByLabel("Titel for Testfilmen (2024)/Testfilmen.2024.mkv")
  await expect(select).toHaveValue(ids.movie)
  expect(sent).toEqual([])
  await page.getByLabel("Nyt navn for Testfilmen (2024)/Testfilmen.2024.mkv").fill("Testfilmen (2024)/Testfilmen.mkv")
  await page.getByRole("button", { name: "Godkend" }).click()
  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0]).toEqual({ path: "/api/v1/adoption/b1/items/i1", body: { review_state: "approved", media_id: ids.movie, root_id: "root1", quality_profile_id: "p1", proposed_relative: "Testfilmen (2024)/Testfilmen.mkv", variant: "default", monitoring_enabled: false, search_immediately: false } })
})

test("the plan cannot be locked while files are unreviewed", async ({ page }) => {
  await open(page, () => batch({ attention_items: 1, approved_items: 1, items: [item, { ...approvedItem, id: "i2" }] }))
  await expect(page.getByRole("button", { name: "Lås planen" })).toBeDisabled()
  await expect(page.getByText("Filer, der mangler gennemgang: 1.")).toBeVisible()
})

test("no more than the server's limit can be approved", async ({ page }) => {
  await open(page, () => batch({ approved_items: 25 }))
  await expect(page.getByRole("button", { name: "Godkend" })).toBeDisabled()
})

test("the preview shows every change before anything moves", async ({ page }) => {
  const sent = await open(page, () => batch({ attention_items: 0, approved_items: 1, items: [approvedItem] }))
  await expect(page.getByText("→ Testfilmen (2024)/Testfilmen.mkv")).toBeVisible()
  await expect(page.getByText("Ingen er flyttet endnu.", { exact: false })).toBeVisible()
  await expect(page.getByRole("button", { name: "Overtag nu" })).toHaveCount(0)
  await page.getByRole("button", { name: "Lås planen" }).click()
  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0].path).toBe("/api/v1/adoption/b1/approve")
})

test("files move only after the exact phrase", async ({ page }) => {
  const sent = await open(page, () => batch({ state: "approved", attention_items: 0, approved_items: 1, items: [approvedItem] }))
  const run = page.getByRole("button", { name: "Overtag nu" })
  await expect(run).toBeDisabled()
  await page.getByLabel("Bekræftelse").fill("execute reviewed adoption")
  await expect(run).toBeDisabled()
  expect(sent).toEqual([])
  await page.getByLabel("Bekræftelse").fill("EXECUTE REVIEWED ADOPTION")
  await run.click()
  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0]).toEqual({ path: "/api/v1/adoption/b1/execute", body: { confirmation: "EXECUTE REVIEWED ADOPTION", adopted_replacement_confirmation: "" } })
})

test("a season must be clear before a series file can be approved", async ({ page }) => {
  await open(page, () => batch({ media_kind: "series", items: [{ ...item, original_relative: "Show/e1.mkv" }] }))
  await expect(page.getByRole("button", { name: "Godkend" })).toBeDisabled()
})

test("a file in a subfolder keeps its place in the library", async ({ page }) => {
  await open(page, () => batch({ source_root: "/libraries/movies/Gamle" }))
  await expect(page.getByLabel("Nyt navn for Testfilmen (2024)/Testfilmen.2024.mkv")).toHaveValue("Gamle/Testfilmen (2024)/Testfilmen.2024.mkv")
})

test("an approved file can be corrected before the plan is locked", async ({ page }) => {
  await open(page, () => batch({ attention_items: 0, approved_items: 1, items: [approvedItem] }))
  await page.getByRole("button", { name: "Ret Testfilmen (2024)/Testfilmen.2024.mkv" }).click()
  await expect(page.getByLabel("Nyt navn for Testfilmen (2024)/Testfilmen.2024.mkv")).toHaveValue("Testfilmen (2024)/Testfilmen.mkv")
  await expect(page.getByRole("button", { name: "Lås planen" })).toBeDisabled()
})

test("the rest of a large folder can be skipped at once", async ({ page }) => {
  const sent = await open(page, () => batch({ attention_items: 2, items: [item, { ...item, id: "i2", original_relative: "B/b.mkv" }] }))
  await page.getByRole("button", { name: "Spring resten over" }).click()
  await expect.poll(() => sent.length).toBe(2)
  expect(sent.map((entry) => entry.body)).toEqual([{ review_state: "rejected" }, { review_state: "rejected" }])
})
