import { expect, test, type Page } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

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

test("the menu is grouped, counts films and series, and Ctrl+K jumps to search", async ({ page }) => {
  test.skip(test.info().project.name !== "desktop", "the grouped menu and top search are the desktop layout")
  await page.goto("/")
  const menu = page.getByRole("navigation", { name: "Hovedmenu" })
  for (const group of ["Bibliotek", "Opgaver", "Opsætning"]) await expect(menu.getByText(group, { exact: true })).toBeVisible()
  await menu.getByRole("link", { name: /^Serier/ }).click()
  await expect(page).toHaveURL(/\/bibliotek\?type=serier$/)
  await expect(menu.getByRole("link", { name: /^Serier/ })).toHaveClass(/font-semibold/)
  await expect(menu.getByRole("link", { name: /^Film/ })).not.toHaveClass(/font-semibold/)
  await page.locator("body").press("Control+k")
  await expect(page.getByRole("searchbox", { name: "Søg i dine titler" })).toBeFocused()
})

test("the library names the kind, notes what is not followed and shows status under the poster", async ({ page }) => {
  await page.goto("/bibliotek?type=serier")
  await expect(page.getByRole("heading", { level: 1, name: "Serier" })).toBeVisible()
  await expect(page.getByText("1 serie har nye sæsoner, som du ikke følger endnu")).toBeVisible()
  await page.goto("/bibliotek?type=film")
  await expect(page.getByRole("heading", { level: 1, name: "Film" })).toBeVisible()
  const tile = page.getByRole("link", { name: /Testfilmen/ })
  await expect(tile.getByText("Kræver handling")).toBeVisible()
  await page.getByRole("link", { name: "Se hvad der mangler" }).click()
  await expect(page).toHaveURL(/\/bibliotek\/mangler\?vis=film$/)
})

test("the title page shows the picture, the facts, who made it and the cast", async ({ page }) => {
  await page.goto(`/bibliotek/${ids.movie}`)
  await expect(page.getByRole("heading", { level: 1, name: "Testfilmen" })).toBeVisible()
  const facts = page.getByRole("list", { name: "Fakta" })
  await expect(facts.getByText("1 t 41 min")).toBeVisible()
  await expect(page.getByText("En film, der kun findes i tests.")).toBeVisible()
  await expect(page.getByText("Ida Instruktør")).toBeVisible()
  const cast = page.getByRole("region", { name: "Medvirkende" })
  await expect(cast.getByText("Anna Skuespiller")).toBeVisible()
  await expect(cast.getByText("Naboen")).toBeVisible()
  await page.getByRole("navigation", { name: "Brødkrumme" }).getByRole("link", { name: "Film" }).click()
  await expect(page).toHaveURL(/\/bibliotek\?type=film$/)
})


test("cases and requests are picture cards with their actions on the card", async ({ page }) => {
  await page.goto("/handling")
  const card = page.locator("#main li").filter({ hasText: "Testfilmen" }).first()
  await expect(card.getByRole("link", { name: "Detaljer" })).toBeVisible()
  await expect(card.getByRole("link", { name: /Testfilmen/ })).toBeVisible()
  await page.goto("/anmodninger")
  await page.getByRole("radio", { name: /Skal godkendes/ }).click()
  const request = page.locator("#main li").filter({ hasText: "Ønskefilmen" })
  await expect(request.getByText("Ønsket af Mor")).toBeVisible()
  await expect(request.getByRole("button", { name: "Godkend" })).toBeVisible()
  await request.getByRole("button", { name: "Afvis" }).click()
  await expect(page.getByRole("dialog", { name: "Afvis Ønskefilmen?" })).toBeVisible()
})
