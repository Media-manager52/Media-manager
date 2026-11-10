import { expect, test, type Page } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

const pixel = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
const now = "2026-11-03T12:00:00Z"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

/** The search field: in the menu on a computer, behind the header button on a phone. */
async function openSearch(page: Page) {
  if (test.info().project.name === "mobile") await page.getByRole("button", { name: "Søg i dine titler" }).click()
  return page.getByRole("searchbox", { name: "Søg i dine titler" })
}

test("the overview shows what was most recently added", async ({ page }) => {
  await mockApi(page)
  await page.goto("/")
  const link = page.getByRole("list", { name: "Senest tilføjet" }).getByRole("link", { name: /Testserien/ })
  await expect(link).toContainText("S01E02")
  await link.click()
  await expect(page).toHaveURL(new RegExp(`/bibliotek/${ids.series}$`))
})

test("the overview says so when nothing has been added yet", async ({ page }) => {
  await mockApi(page)
  await page.route("**/api/v1/media/recent*", (route) => route.fulfill({ json: { data: [] } }))
  await page.goto("/")
  await expect(page.getByText("Intet er hentet endnu")).toBeVisible()
})

test("the search field finds a title from any page", async ({ page }) => {
  await mockApi(page)
  await page.goto("/kilder")
  const field = await openSearch(page)
  await field.fill("testf")
  const results = page.getByRole("list", { name: "Fundne titler" })
  await expect(results.getByRole("link")).toHaveCount(1)
  await results.getByRole("link", { name: /Testfilmen/ }).click()
  await expect(page).toHaveURL(new RegExp(`/bibliotek/${ids.movie}$`))
  await expect(page.getByRole("list", { name: "Fundne titler" })).toBeHidden()
})

test("Enter in the search field opens the library with that search", async ({ page }) => {
  await mockApi(page)
  await page.goto("/bibliotek?q=intet")
  await expect(page.getByRole("searchbox", { name: "Søg i biblioteket" })).toHaveValue("intet")
  const field = await openSearch(page)
  await field.fill("serien")
  await field.press("Enter")
  await expect(page).toHaveURL(/\/bibliotek\?q=serien$/)
  await expect(page.getByRole("searchbox", { name: "Søg i biblioteket" })).toHaveValue("serien")
  await expect(page.getByRole("link", { name: /Testserien/ })).toBeVisible()
  await expect(page.getByRole("link", { name: /Testfilmen/ })).toHaveCount(0)
})

test("a search with no match offers to add it as a new title", async ({ page }) => {
  await mockApi(page)
  await page.goto("/")
  const field = await openSearch(page)
  await field.fill("Nyfilmen")
  await expect(page.getByText("Ingen af dine titler passer.")).toBeVisible()
  await page.getByRole("link", { name: 'Find "Nyfilmen" som ny titel' }).click()
  await expect(page).toHaveURL(/\/tilfoej\?q=Nyfilmen$/)
  await expect(page.getByText("Nyfilmen").last()).toBeVisible()
  await expect(page.getByRole("button", { name: /Nyfilmen/ }).or(page.getByRole("link", { name: /Nyfilmen/ })).first()).toBeVisible()
})

test("the library sorts by newest file and filters by folder", async ({ page }) => {
  await mockApi(page)
  await page.route("**/api/v1/media/status", (route) => route.fulfill({ json: { data: [
    { media_id: ids.movie, in_plex: true, missing: 0, attention: false, root_ids: [ids.root], added_at: "2026-11-01T10:00:00Z" },
    { media_id: ids.series, in_plex: true, missing: 0, attention: false, root_ids: [], added_at: "2026-11-02T10:00:00Z" },
  ] } }))
  await page.goto("/bibliotek")
  const titles = page.locator(`a[href^="/bibliotek/"]`)
  await expect(titles).toHaveCount(2)
  await expect(titles.first()).toContainText("Testfilmen")
  await page.getByLabel("Sortér").selectOption("senest-tilfoejet")
  await expect(page).toHaveURL(/sort=senest-tilfoejet/)
  await expect(titles.first()).toContainText("Testserien")
  await expect(page.getByLabel("Mappe").locator("option")).toHaveText(["Alle mapper", "Film (1)"])
  await page.getByLabel("Mappe").selectOption({ label: "Film (1)" })
  await expect(page).toHaveURL(new RegExp(`mappe=${ids.root}`))
  await expect(titles).toHaveCount(1)
  await expect(titles.first()).toContainText("Testfilmen")
})

test("a folder filter never shows titles when the status is unknown", async ({ page }) => {
  await mockApi(page)
  await page.route("**/api/v1/media/status", (route) => route.fulfill({ status: 500, json: { error: { code: "internal", message: "down" } } }))
  await page.goto(`/bibliotek?mappe=${ids.root}`)
  await expect(page.getByText("kræver titlernes status")).toBeVisible()
  await expect(page.locator(`a[href^="/bibliotek/"]`)).toHaveCount(0)
})

const second = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const third = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
function waitingCase(id: string, title: string, extra: unknown[] = []) {
  return {
    id, media_id: ids.movie, media_kind: "movie", title, year: 2024, variant: "default", state: "needs_attention", revision: 2,
    attention_code: "acquisition_attempts_exhausted", attention_detail: "All attempts were used.", owner_action_required: true,
    adopted_replacement_confirmation_required: false, created_at: now, updated_at: now,
    available_actions: [
      { action: "open_round_from_known", operation_id: "openRoundFromKnown", method: "POST", path: `/api/v1/workflows/${id}/rounds/from-known`, effect: "provider", available: true },
      { action: "cancel", operation_id: "cancelWorkflow", method: "POST", path: `/api/v1/workflows/${id}/cancel`, effect: "write", available: true },
      ...extra,
    ],
  }
}

test("one button handles a whole group and reports each case honestly", async ({ page }) => {
  const writes = await mockApi(page)
  const cases = [waitingCase(ids.attention, "Første film"), waitingCase(second, "Anden film"), waitingCase(third, "Tredje film")]
  await page.route("**/api/v1/workflows?*", (route) => route.fulfill({ json: { data: new URL(route.request().url()).searchParams.get("offset") && new URL(route.request().url()).searchParams.get("offset") !== "0" ? [] : cases } }))
  await page.route(`**/api/v1/workflows/${second}/rounds/from-known`, (route) => route.fulfill({ status: 409, json: { error: { code: "conflict", message: "no known release is eligible" } } }))
  await page.goto("/handling")
  await expect(page.getByRole("button", { name: /Annullér for/ })).toHaveCount(0)
  await page.getByRole("button", { name: "Prøv kendte udgivelser for alle 3" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("3 sager, én ad gangen")
  await expect(dialog).toContainText("tæller med i dagens grænser")
  expect(writes).toEqual([])
  await dialog.getByRole("button", { name: "Prøv kendte udgivelser for 3" }).click()
  await expect(dialog.getByRole("status")).toContainText("sat i gang for 2 af 3 sager")
  await expect(dialog).toContainText("1 kunne ikke startes")
  await expect(dialog).toContainText("Anden film:")
  expect(writes.map((write) => write.path)).toEqual([`/api/v1/workflows/${ids.attention}/rounds/from-known`, `/api/v1/workflows/${third}/rounds/from-known`])
  await dialog.getByRole("button", { name: "Luk" }).last().click()
  await expect(dialog).toBeHidden()
})

test("a single case gets no group button, and a group can be abandoned", async ({ page }) => {
  const writes = await mockApi(page)
  await page.goto("/handling")
  await expect(page.getByRole("button", { name: /for alle/ })).toHaveCount(0)
  const cases = [waitingCase(ids.attention, "Første film"), waitingCase(second, "Anden film")]
  await page.route("**/api/v1/workflows?*", (route) => route.fulfill({ json: { data: cases } }))
  await page.reload()
  await page.getByRole("button", { name: "Prøv kendte udgivelser for alle 2" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Fortryd" }).click()
  await expect(page.getByRole("dialog")).toBeHidden()
  expect(writes).toEqual([])
})

test("sources show how many downloads from each indexer were approved", async ({ page }) => {
  await mockApi(page)
  await page.goto("/kilder")
  await expect(page.getByText("6 af 8 (75 %)")).toBeVisible()
  await expect(page.getByRole("progressbar", { name: "Godkendte downloads de seneste 30 dage" })).toBeVisible()
})

test("sources show the newest-uploads reader and let the owner set the page size", async ({ page }) => {
  await mockApi(page)
  const patches: unknown[] = []
  await page.route("**/api/v1/settings/integrations/*", (route) => {
    if (route.request().method() !== "PATCH") return route.fallback()
    patches.push(route.request().postDataJSON())
    return route.fulfill({ json: {} })
  })
  await page.goto("/kilder")
  await expect(page.getByText(/Nye uploads læst .+ · 3 passede til ventende titler sidste døgn/)).toBeVisible()
  await page.getByRole("button", { name: "Rediger" }).first().click()
  const pageSize = page.getByLabel("Resultater pr. svar (højst 500)")
  await expect(pageSize).toHaveValue("100")
  await pageSize.fill("501")
  await expect(page.getByRole("alert")).toContainText("højst 500")
  await pageSize.fill("300")
  await page.getByRole("dialog").getByRole("button", { name: "Gem" }).click()
  await expect.poll(() => patches).toEqual([{ page_limit: 300 }])
})

test("the calendar shows a poster and marks episodes that have not been sent", async ({ page }) => {
  await mockApi(page)
  const day = (offset: number) => {
    const date = new Date(Date.now() + offset * 86_400_000)
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`
  }
  const entry = { media_id: ids.series, media_kind: "series", media_title: "Testserien", workflow_id: ids.season, variant: "season:2:episode:1", season: 2, monitored: true }
  await page.route("**/api/v1/calendar*", (route) => route.fulfill({ json: { data: [
    { ...entry, episode: 1, episode_title: "Sendt", air_date: day(-1), workflow_state: "waiting" },
    { ...entry, episode: 2, episode_title: "Senere", air_date: day(3), workflow_state: "waiting", poster_url: pixel },
    { ...entry, episode: 3, episode_title: "Tidligt ude", air_date: day(3), workflow_state: "plex_confirmed" },
  ] } }))
  await page.goto("/kalender")
  const row = (name: string) => page.getByRole("link", { name: new RegExp(name) })
  await expect(row("Sendt")).toContainText("Venter")
  await expect(row("Senere")).toContainText("Kommer")
  await expect(row("Tidligt ude")).toContainText("I Plex")
  await expect(row("Senere").locator("img")).toHaveAttribute("src", pixel)
  await expect(page.getByText("2 afsnit")).toBeVisible()
})
