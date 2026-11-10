import { expect, test } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

const now = "2026-01-01T00:00:00Z"

test("a library folder can be renamed and an unused one deleted; the default has no Slet", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/indstillinger/biblioteker")
  await expect(page.getByRole("button", { name: "Slet Film 4K" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Slet Film", exact: true })).toHaveCount(0)

  await page.getByRole("button", { name: "Omdøb Film 4K" }).click()
  const rename = page.getByRole("dialog")
  await expect(rename).toContainText("Mapper, filnavne og Plex-biblioteket er de samme som før.")
  await expect(rename.getByRole("button", { name: "Gem navn" })).toBeDisabled()
  await rename.getByLabel("Mappens navn").fill(" Film UHD ")
  await rename.getByRole("button", { name: "Gem navn" }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ method: "PATCH", path: "/api/v1/settings/library-roots/root-4k", body: { name: "Film UHD" } })

  await page.getByRole("button", { name: "Slet Film 4K" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Selve mappen på disken og Plex-biblioteket bliver ikke rørt.")
  expect(writes).toHaveLength(1)
  await dialog.getByRole("button", { name: "Slet mappe" }).click()
  await expect.poll(() => writes.length).toBe(2)
  expect(writes[1]).toMatchObject({ method: "DELETE", path: "/api/v1/settings/library-roots/root-4k" })
})

test("a folder that has held titles says why it stays", async ({ page }) => {
  await mockApi(page)
  await page.route("**/api/v1/settings/library-roots/root-4k", (route) => route.fulfill({ status: 409, json: { error: { code: "library_root_in_use", message: "in use" } } }))
  await page.goto("/indstillinger/biblioteker")
  await page.getByRole("button", { name: "Slet Film 4K" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Slet mappe" }).click()
  await expect(dialog.getByRole("alert")).toContainText("kan slås fra, men ikke slettes")
})

test("only a connection that is switched off offers Slet", async ({ page }) => {
  const writes = await mockApi(page)
  await page.route("**/api/v1/settings/integrations", (route) => route.request().method() === "GET" ? route.fulfill({ json: { data: [
    { id: ids.plex, kind: "plex", name: "Plex", enabled: true, is_default: true, config: { base_url: "http://plex:32400", allow_http: true }, created_at: now, updated_at: now },
    { id: "old-seerr", kind: "seerr", name: "Gammel Seerr", enabled: false, is_default: false, config: { base_url: "http://old:5055", allow_http: true }, created_at: now, updated_at: now },
  ] } }) : route.fallback())
  await page.goto("/indstillinger/forbindelser")
  await expect(page.getByRole("button", { name: "Slet Gammel Seerr" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Slet Plex" })).toHaveCount(0)
  await page.getByRole("button", { name: "Slet Gammel Seerr" }).click()
  expect(writes).toEqual([])
  await page.getByRole("dialog").getByRole("button", { name: "Slet forbindelse" }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toMatchObject({ method: "DELETE", path: "/api/v1/settings/integrations/old-seerr" })
})

test("an indexer that is switched off can be deleted, and one with history says why not", async ({ page }) => {
  await mockApi(page)
  const usage = { queries_used: 0, queries_remaining: 100, background_queries_remaining: 50, grabs_used: 0, grabs_remaining: 10 }
  const indexer = (id: string, name: string, enabled: boolean) => ({ id, name, kind: "newznab", enabled, commissioned: true, budget: { query_limit: 100, grab_limit: 10, background_query_limit: 50, minimum_interval_ms: 3000, window_seconds: 86400, source: "owner_stated" }, downloads_30d: { finished: 0, verified: 0 }, usage, pacing: { consecutive_failures: 0 }, search_outcomes_24h: [] })
  await page.route("**/api/v1/indexers", (route) => route.fulfill({ json: { data: [indexer("on", "Aktiv", true), indexer("off", "Gammel indexer", false)] } }))
  await page.route("**/api/v1/settings/integrations/off", (route) => route.fulfill({ status: 409, json: { error: { code: "integration_in_use", message: "in use" } } }))
  await page.goto("/kilder")
  await expect(page.getByRole("button", { name: "Slet Aktiv" })).toHaveCount(0)
  await page.getByRole("button", { name: "Slet Gammel indexer" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Slet indexer" }).click()
  await expect(dialog.getByRole("alert")).toContainText("Lad den være slået fra i stedet.")
})
