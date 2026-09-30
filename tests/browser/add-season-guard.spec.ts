import { expect, test } from "@playwright/test"

import { installOwnerAPI, ownerFixtureIDs } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"

// The title page's "Add season" box used to be prefilled with "1:10", so one
// click added a blank ten-episode season 1 that searched at once, even when
// season 1 was already in Plex.
test("adding a season starts empty and asks before searching a season Plex already has", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  await page.goto(`/media/series/${ownerFixtureIDs.series}`)
  await expect(page.getByRole("heading", { level: 1, name: "First Browser Series" })).toBeVisible()

  const box = page.getByRole("textbox", { name: "Season and episode count" })
  const add = page.getByRole("button", { name: "Add season" })
  await expect(box).toHaveValue("")
  await expect(add).toBeDisabled()

  await box.fill("one")
  await expect(page.getByRole("status").filter({ hasText: "Use season:episode-count" })).toBeVisible()
  await expect(add).toBeDisabled()

  // Season 1 is Plex-confirmed in the fixture: warn and require confirmation.
  await box.fill("1:8")
  const note = page.getByRole("status").filter({ hasText: "Season 1 is already in Plex" })
  await expect(note).toBeVisible()
  await expect(add).toBeDisabled()
  await note.getByRole("checkbox", { name: "Search again anyway" }).check()
  await expect(add).toBeEnabled()

  // Changing the season resets the confirmation; a new season needs none.
  await box.fill("3:6")
  await expect(page.getByRole("status").filter({ hasText: "already in Plex" })).toHaveCount(0)
  await expect(add).toBeEnabled()

  expect(recorder.unexpected).toEqual([])
})
