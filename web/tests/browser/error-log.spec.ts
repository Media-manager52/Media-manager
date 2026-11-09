import { expect, test } from "@playwright/test"

import { mockApi } from "./mock-api"

test.beforeEach(async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
  await mockApi(page)
})

test("the error log explains a failure in plain Danish and copies it", async ({ page }) => {
  await page.goto("/")
  const openMenu = page.getByRole("button", { name: "Åbn menu" })
  if (await openMenu.isVisible()) await openMenu.click()
  await page.getByRole("navigation", { name: "Hovedmenu" }).getByRole("link", { name: "Fejllog" }).click()
  await expect(page.getByRole("heading", { name: "Fejllog" })).toBeVisible()
  await expect(page.getByText("Filen havde ikke det sprog, profilen kræver.")).toBeVisible()
  await expect(page.getByRole("link", { name: "Testfilmen" })).toBeVisible()
  await expect(page.getByText("Testfilmen.2024.1080p.WEB")).toBeVisible()
  await expect(page.getByText(/Det sker nu:/)).toBeVisible()

  await page.getByRole("button", { name: "Kopiér listen" }).click()
  await expect(page.getByText("Fejlloggen er kopieret")).toBeVisible()
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied).toContain("Media Manager fejllog: 1 fejl")
  expect(copied).toContain("Kode: subtitle_requirements_unverified")
})

test("the error log filters by area on the server", async ({ page }) => {
  const areas: string[] = []
  await page.route("**/api/v1/errors*", async (route) => {
    areas.push(new URL(route.request().url()).searchParams.get("area") ?? "")
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: { entries: [], total: 0, areas: { backup: 0 }, since: "2026-10-04T12:00:00Z" } }) })
  })
  await page.goto("/fejl")
  await expect(page.getByText("Ingen fejl de seneste 30 dage")).toBeVisible()
  await page.getByRole("radio", { name: /Backup/ }).click()
  await expect(page.getByText("Ingen fejl passer til din søgning")).toBeVisible()
  expect(areas).toContain("backup")
})

test("the mobile shortcuts keep the library and wishes", async ({ page, isMobile }) => {
  test.skip(!isMobile, "only the phone layout has the bottom bar")
  await page.goto("/fejl")
  const shortcuts = page.getByRole("navigation", { name: "Genveje" })
  await expect(shortcuts.getByRole("link", { name: "Bibliotek" })).toBeVisible()
  await expect(shortcuts.getByRole("link", { name: "Ønsker" })).toBeVisible()
})
