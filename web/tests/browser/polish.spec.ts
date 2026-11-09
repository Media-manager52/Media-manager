import { expect, test, type Page } from "@playwright/test"

import { mockApi } from "./mock-api"

const longPath = `/media/Serier/${"EnMegetLangMappeUdenMellemrum".repeat(12)}/fil.mkv`
const repair = (id: number, state: string, reason = "missing_required_audio", detail = "replacement confirmed in Plex; the old file was retired") => ({
  id: `r-${id}`, state, reason, detail, title: `Reparation ${id}`, altmount_file_path: "/a/b.mkv", created_at: "2026-11-09T10:00:00Z",
})
const json = (data: unknown) => ({ contentType: "application/json", body: JSON.stringify({ data }) })

async function noSidewaysScroll(page: Page) {
  const { scroll, view } = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, view: document.documentElement.clientWidth }))
  expect(scroll).toBeLessThanOrEqual(view)
}

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
  await mockApi(page)
})

test("repairs speak Danish, put open cases first and never scroll sideways", async ({ page }) => {
  const finished = Array.from({ length: 60 }, (_, index) => repair(index, "repaired", "corrupted"))
  const open = repair(99, "needs_attention", "missing_required_audio", `Plex has no single item at ${longPath}`)
  await page.route("**/api/v1/repairs", (route) => route.fulfill(json([...finished, open])))
  await page.goto("/system/reparationer")

  const rows = page.getByRole("main").getByRole("listitem")
  await expect(rows.first()).toContainText("Reparation 99")
  await expect(rows.first()).toContainText("Kræver handling")
  await expect(rows.first()).toContainText("Plex har ikke ét bestemt emne for filen")
  await expect(rows.nth(1)).toContainText("Repareret")
  await expect(rows.nth(1)).toContainText("Erstatningen er bekræftet i Plex")
  await noSidewaysScroll(page)

  await expect(rows).toHaveCount(50)
  await page.getByRole("button", { name: "Vis flere (11 tilbage)" }).click()
  await expect(rows).toHaveCount(61)
  await expect(page.getByRole("button", { name: /Vis flere/ })).toHaveCount(0)
})

test("the activity log uses plain words and keeps the code as a tooltip", async ({ page }) => {
  await page.route("**/api/v1/activity*", (route) => route.fulfill(json([
    { id: "1", kind: "workflow_transition", actor: "worker", action: "workflow.queued", aggregate_type: "workflow", aggregate_id: "w", detail: {}, occurred_at: "2026-11-09T10:00:00Z" },
  ])))
  await page.goto("/system/aktivitet")
  await expect(page.getByText("Status: Henter")).toHaveAttribute("title", "workflow.queued")
  await expect(page.getByText("Baggrundsarbejde")).toBeVisible()
})

test("a title that no longer exists offers a way back", async ({ page }) => {
  await page.route("**/api/v1/media/gone/**", (route) => route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "not_found", message: "not found" } }) }))
  await page.goto("/bibliotek/gone")
  await expect(page.getByText("Titlen findes ikke længere")).toBeVisible()
  await page.getByRole("link", { name: "Gå til Bibliotek" }).click()
  await expect(page).toHaveURL(/\/bibliotek$/)
})

test("a work page that no longer exists offers a way back", async ({ page }) => {
  await page.route("**/api/v1/workflows/gone*", (route) => route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "not_found", message: "not found" } }) }))
  await page.goto("/arbejde/gone")
  await expect(page.getByText("Dette arbejde findes ikke længere")).toBeVisible()
  await expect(page.getByRole("link", { name: "Se alt arbejde" })).toBeVisible()
})

test("a refused title request still offers a retry, not a false not-found", async ({ page }) => {
  await page.route("**/api/v1/media/broken/**", (route) => route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: { code: "invalid_id", message: "bad" } }) }))
  await page.goto("/bibliotek/broken")
  await expect(page.getByRole("alert")).toBeVisible()
  await expect(page.getByText("Titlen findes ikke længere")).toHaveCount(0)
})

test("phone controls are big enough to hit", async ({ page, isMobile }) => {
  test.skip(!isMobile, "only the phone layout has these sizes")
  await page.goto("/fejl")
  const menu = await page.getByRole("button", { name: "Åbn menu" }).boundingBox()
  expect(menu?.height).toBeGreaterThanOrEqual(40)
  expect(menu?.width).toBeGreaterThanOrEqual(40)
  const filter = await page.getByRole("radio").first().boundingBox()
  expect(filter?.height).toBeGreaterThanOrEqual(40)
})
