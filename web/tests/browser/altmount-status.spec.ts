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
  await expect(page.getByText(/mistet drevet efter en genstart af AltMount. Brug "Lad Media Manager rette Plex" nedenfor/)).toBeVisible()
  await expect(page.getByRole("link", { name: "Se vejledningen" })).toHaveAttribute("href", /README\.md#altmount/)
  await page.goto("/")
  await expect(page.getByText("Plex i stuen kan ikke se filerne")).toBeVisible()
})

test("Ret Plex shows what changes and fixes Plex with a Cosmos token that is not kept", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/system")
  const fix = page.getByRole("button", { name: "Vis hvad der ændres" })
  await expect(fix).toBeDisabled()
  await page.getByLabel("Cosmos API-nøgle").fill("cosmos-token-1")
  await fix.click()
  await expect(page.getByText("Det her bliver ændret i plex:")).toBeVisible()
  await expect(page.getByText("Plex får AltMounts filer i /mnt/altmount, og følger med, når AltMount genstarter.")).toBeVisible()
  await expect(page.getByText("Plex-biblioteket \"Film\" får mappen /libraries/movies.")).toBeVisible()
  await page.getByRole("button", { name: "Ret Plex" }).click()
  await expect(page.getByText("Plex er rettet.")).toBeVisible()
  await expect(page.getByText("Cosmos-nøglen er ikke gemt. Du kan slette den i Cosmos nu.")).toBeVisible()
  const sent = writes.filter((write) => write.path === "/api/v1/setup/plex-access")
  expect(sent.map((write) => (write.body as { apply: boolean }).apply)).toEqual([false, true])
  expect(sent[1].body).toMatchObject({ cosmos_api_key: "cosmos-token-1", cosmos_host: "127.0.0.1:4174" })
})
