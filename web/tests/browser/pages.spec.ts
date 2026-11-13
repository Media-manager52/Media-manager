import AxeBuilder from "@axe-core/playwright"
import { expect, test } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

const pages: Array<[string, string]> = [
  ["/", "Oversigt"],
  ["/handling", "Kræver handling"],
  ["/bibliotek", "Bibliotek"],
  ["/bibliotek/mangler", "Find manglende"],
  ["/bibliotek/mangler?vis=aeldre", "Find manglende"],
  ["/bibliotek/mangler?vis=film", "Find manglende"],
  [`/bibliotek/${ids.movie}`, "Testfilmen"],
  [`/bibliotek/${ids.series}`, "Testserien"],
  ["/tilfoej", "Tilføj film eller serie"],
  ["/anmodninger", "Anmodninger"],
  ["/arbejde?vis=handling", "Arbejde"],
  [`/arbejde/${ids.attention}`, "Testfilmen · Film"],
  ["/kalender", "Kalender"],
  ["/kilder", "Kilder"],
  ["/lister", "Lister"],
  ["/indstillinger/profiler", "Indstillinger"],
  ["/indstillinger/biblioteker", "Indstillinger"],
  ["/indstillinger/seerr", "Indstillinger"],
  ["/indstillinger/notifikationer", "Indstillinger"],
  ["/indstillinger/forbindelser", "Indstillinger"],
  ["/indstillinger/usenet-servere", "Indstillinger"],
  ["/indstillinger/overtag", "Indstillinger"],
  ["/kom-i-gang", "Kom i gang"],
  ["/system", "System"],
  ["/system/koe", "System"],
  ["/system/backup", "System"],
  ["/system/reparationer", "System"],
  ["/system/aktivitet", "System"],
  ["/fejl", "Fejllog"],
]

for (const [path, heading] of pages) {
  test(`${path} renders without errors or accessibility violations`, async ({ page }) => {
    const errors: string[] = []
    page.on("pageerror", (error) => errors.push(error.message))
    await mockApi(page)
    await page.goto(path)
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible()
    await page.waitForLoadState("networkidle")
    expect(errors).toEqual([])
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, "no horizontal page scroll").toBeLessThanOrEqual(1)
    const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa"]).analyze()
    const serious = results.violations.filter((violation) => violation.impact === "serious" || violation.impact === "critical")
    expect(serious.map((violation) => `${violation.id}: ${violation.nodes.map((node) => node.target.join(" ")).join(", ")}`)).toEqual([])
  })
}

test("old English addresses still land on the new pages", async ({ page }) => {
  await mockApi(page)
  await page.goto("/activity/queue")
  await expect(page).toHaveURL(/\/arbejde$/)
  await page.goto("/media/series")
  await expect(page).toHaveURL(/\/bibliotek\?type=serier$/)
})

test("unknown addresses show a Danish not-found page", async ({ page }) => {
  await mockApi(page)
  await page.goto("/findes-ikke")
  await expect(page.getByRole("heading", { level: 1, name: "Siden findes ikke" })).toBeVisible()
})
