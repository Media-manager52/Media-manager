import { expect, test } from "@playwright/test"

import { mockApi } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

test("the owner adds a Usenet server with a tested login and changes one without retyping its password", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/indstillinger/usenet-servere")
  await expect(page.getByText("Hovedudbyder")).toBeVisible()
  await expect(page.getByText("backup.example.test", { exact: true })).toBeVisible()
  // The only active server can be neither turned off nor deleted.
  await expect(page.getByRole("button", { name: "Slet Hovedudbyder" })).toBeDisabled()
  await expect(page.getByRole("button", { name: "Slet backup.example.test" })).toBeEnabled()

  await page.getByRole("button", { name: "Tilføj server" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("button", { name: "Test login" })).toBeDisabled()
  await dialog.getByLabel("Adresse").fill("nntps://eu.example.test:563")
  await expect(dialog.getByLabel("Adresse")).toHaveValue("eu.example.test")
  await dialog.getByLabel("Brugernavn").fill("me")
  await dialog.getByRole("button", { name: "Gem" }).click()
  await expect(dialog.getByRole("alert")).toContainText("Skriv din adgangskode")
  expect(writes).toHaveLength(0)
  await dialog.getByLabel("Adgangskode").fill("pw-typed")
  await dialog.getByRole("button", { name: "Test login" }).click()
  await expect(dialog.getByRole("status")).toContainText("Login virker (svartid 23 ms)")
  await dialog.getByRole("button", { name: "Gem" }).click()
  await expect.poll(() => writes.length).toBe(2)
  expect(writes[0]).toMatchObject({ method: "POST", path: "/api/v1/settings/usenet-servers/test", body: { host: "eu.example.test", port: 563, username: "me", password: "pw-typed", tls: true } })
  expect(writes[1]).toMatchObject({ method: "POST", path: "/api/v1/settings/usenet-servers", body: { host: "eu.example.test", port: 563, username: "me", password: "pw-typed", max_connections: 10, tls: true, enabled: true } })
  await expect(page.getByRole("dialog")).toHaveCount(0)

  await page.getByRole("button", { name: "Rediger" }).first().click()
  const edit = page.getByRole("dialog")
  await expect(edit.getByLabel("Adgangskode")).toHaveValue("")
  await edit.getByLabel("Forbindelser").fill("30")
  await edit.getByRole("button", { name: "Gem" }).click()
  await expect.poll(() => writes.length).toBe(3)
  expect(writes[2]).toEqual({ method: "PATCH", path: "/api/v1/settings/usenet-servers/provider_1", body: { max_connections: 30 } })

  await page.getByRole("button", { name: "Flyt backup.example.test op" }).click()
  await expect.poll(() => writes.length).toBe(4)
  expect(writes[3]).toEqual({ method: "PUT", path: "/api/v1/settings/usenet-servers/order", body: { ids: ["provider_2", "provider_1"] } })
})
