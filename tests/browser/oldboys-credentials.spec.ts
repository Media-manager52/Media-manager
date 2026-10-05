import { expect, test } from "@playwright/test"
import { installOwnerAPI } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"
const credentials = { api_token: "browser-fixture-api", pass_key: "browser-fixture-pid", rss_key: "browser-fixture-rid" }

test("owner saves all three OldBoys credentials without retaining or displaying values", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  let configured = false
  let saves = 0
  await page.route(`${baseURL}/api/v1/settings/oldboys/credentials`, async (route) => {
    const request = route.request()
    if (request.method() === "PUT") {
      expect(request.postDataJSON()).toEqual(credentials)
      configured = true; saves++
    } else expect(request.method()).toBe("GET")
    await route.fulfill({ status: 200, json: { data: { available: true, configured } } })
  })
  await page.goto("/settings/connections")
  await expect(page.getByText("No OldBoys credentials saved yet.")).toBeVisible()
  for (const [label, value] of [["API Token", credentials.api_token], ["Pass Key (PID)", credentials.pass_key], ["RSS Key (RID)", credentials.rss_key]]) {
    await expect(page.getByLabel(label, { exact: true })).toHaveAttribute("type", "password")
    await page.getByLabel(label, { exact: true }).fill(value)
  }
  await page.getByRole("button", { name: "Save OldBoys credentials", exact: true }).click()
  await expect(page.getByText("Credentials saved. OldBoys is awaiting validation for Danish TV and films.")).toBeVisible()
  for (const label of ["API Token", "Pass Key (PID)", "RSS Key (RID)"]) await expect(page.getByLabel(label, { exact: true })).toHaveValue("")
  const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }))
  const text = await page.locator("body").innerText()
  for (const secret of Object.values(credentials)) { expect(storage).not.toContain(secret); expect(text).not.toContain(secret) }
  await page.reload()
  await expect(page.getByRole("button", { name: "Replace OldBoys credentials", exact: true })).toBeVisible()
  for (const label of ["API Token", "Pass Key (PID)", "RSS Key (RID)"]) await expect(page.getByLabel(label, { exact: true })).toHaveValue("")
  expect(saves).toBe(1)
  expect(recorder.unexpected).toEqual([])
})

test("provider credential errors never echo secrets into the form", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  await page.route(`${baseURL}/api/v1/settings/oldboys/credentials`, async (route) => {
    if (route.request().method() === "PUT") await route.fulfill({ status: 400, json: { error: { message: credentials.api_token } } })
    else await route.fulfill({ status: 200, json: { data: { available: true, configured: false } } })
  })
  await page.goto("/settings/connections")
  await expect(page.getByText("No OldBoys credentials saved yet.")).toBeVisible()
  await page.getByLabel("API Token", { exact: true }).fill(credentials.api_token)
  await page.getByLabel("Pass Key (PID)", { exact: true }).fill(credentials.pass_key)
  await page.getByLabel("RSS Key (RID)", { exact: true }).fill(credentials.rss_key)
  await page.getByRole("button", { name: "Save OldBoys credentials", exact: true }).click()
  await expect(page.getByRole("alert")).toContainText("Could not save credentials.")
  expect(await page.locator("body").innerText()).not.toContain(credentials.api_token)
  expect(recorder.unexpected).toEqual([])
})
