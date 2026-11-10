import { expect, test } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

test("Find manglende lists new seasons and adds them like the title page", async ({ page }) => {
  await mockApi(page)
  const created: Array<{ path: string; body: unknown }> = []
  // The series' own folder and profile, which new seasons must keep.
  await page.route("**/api/v1/settings/library-roots", (route) => route.fulfill({ json: { data: [{ id: "series-root", media_kind: "series", name: "Serier", destination_root: "/media/Serier", plex_visible_root: "/media/Serier", source_visible_root: "/mnt/altmount", is_default: true, enabled: true }] } }))
  await page.route("**/api/v1/settings/quality-profiles", (route) => route.fulfill({ json: { data: [{ id: "series-profile", media_kind: "series", name: "Dansk", policy_version: 1, policy: {}, upgrades_enabled: true, is_default: true, enabled: true }] } }))
  await page.route("**/api/v1/discovery/tv/500?season=*", (route) => route.fulfill({ json: { data: { season: { number: 4, episodes: [{ number: 1, provider_id: "e1", title: "Første", air_date: "2026-01-01" }] } } } }))
  await page.route(`**/api/v1/media/${ids.series}/workflows`, async (route) => {
    created.push({ path: new URL(route.request().url()).pathname, body: route.request().postDataJSON() })
    await route.fulfill({ status: 201, json: { data: { id: "w-new", state: "waiting" } } })
  })
  await page.goto("/bibliotek")
  await page.locator("#main").getByRole("link", { name: "Find manglende" }).click()
  await expect(page.getByRole("heading", { level: 1, name: "Find manglende" })).toBeVisible()
  await expect(page.getByText("Sæson 4 og 5")).toBeVisible()
  await page.getByRole("button", { name: "Tilføj 2 sæsoner" }).click()
  await expect(page.getByText("Tilføjet", { exact: true })).toBeVisible()
  expect(created.map((entry) => (entry.body as { season: { number: number } }).season.number)).toEqual([4, 5])
  expect(created[0].body).toMatchObject({ root_id: "series-root", quality_profile_id: "series-profile", search_immediately: true })

  await page.getByRole("radio", { name: /Ældre sæsoner/ }).click()
  await expect(page.getByText("Sæson 1 blev stoppet før")).toBeVisible()
  await expect(page.getByRole("button", { name: /^Tilføj alle/ })).toHaveCount(0)
})
