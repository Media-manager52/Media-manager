import { expect, test } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

test("System → AltMount shows the drive, the servers with quota and what is played now", async ({ page }) => {
  await mockApi(page)
  await page.goto("/system/altmount")
  await expect(page.getByText("Kører")).toBeVisible()
  await expect(page.getByText("Hovedserver med et meget langt navn til mobil")).toBeVisible()
  await expect(page.getByText(/Kvote: .* \(82 %\)/)).toBeVisible()
  await expect(page.getByRole("progressbar", { name: "Kvote for Hovedserver med et meget langt navn til mobil" })).toHaveAttribute("aria-valuenow", "82")
  await expect(page.getByText("Reserve")).toBeVisible()
  await expect(page.getByText("Mangler mange artikler")).toBeVisible()
  await expect(page.getByText("Slået fra")).toBeVisible()
  const stream = page.getByRole("link", { name: "Far skal giftes (1941)" })
  await expect(stream).toHaveAttribute("href", new RegExp(ids.movie))
  await expect(page.getByText("Other.File.2020.mkv")).toBeVisible()
  await expect(page.getByText("Henter", { exact: true }).first()).toBeVisible()
})

test("a Plex server that lost AltMount's drive is a warning with the fix", async ({ page }) => {
  await mockApi(page)
  await page.goto("/system")
  await expect(page.getByText("Plex i stuen mangler AltMounts filer i /mnt/altmount")).toBeVisible()
  await expect(page.getByText(/Genstart Plex nu, og giv Plex mappen med rslave/)).toBeVisible()
  await expect(page.getByRole("link", { name: "Se vejledningen" })).toHaveAttribute("href", /README\.md#altmount/)
  await page.goto("/")
  await expect(page.getByText("Plex i stuen kan ikke se filerne")).toBeVisible()
})
