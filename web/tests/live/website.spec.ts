import { expect, test, type Page } from "@playwright/test"

// Read-only tour of the deployed site with real data.
async function readOnly(page: Page): Promise<string[]> {
  const problems: string[] = []
  await page.route("**/*", (route) => (route.request().method() === "GET" || route.request().method() === "HEAD" ? route.continue() : route.abort()))
  page.on("pageerror", (error) => problems.push(`page error: ${error.message}`))
  page.on("response", (response) => {
    if (response.url().includes("/api/") && response.status() >= 400) problems.push(`HTTP ${response.status()} ${new URL(response.url()).pathname}`)
  })
  return problems
}

const pages: Array<[string, string]> = [
  ["/", "Oversigt"],
  ["/handling", "Kræver handling"],
  ["/bibliotek", "Bibliotek"],
  ["/anmodninger", "Anmodninger"],
  ["/arbejde", "Arbejde"],
  ["/arbejde?vis=venter", "Arbejde"],
  ["/kalender", "Kalender"],
  ["/kilder", "Kilder"],
  ["/indstillinger/profiler", "Indstillinger"],
  ["/indstillinger/biblioteker", "Indstillinger"],
  ["/indstillinger/seerr", "Indstillinger"],
  ["/system", "System"],
  ["/system/koe", "System"],
  ["/system/backup", "System"],
  ["/system/reparationer", "System"],
]

for (const [path, heading] of pages) {
  test(`live ${path} loads real data`, async ({ page }) => {
    const problems = await readOnly(page)
    await page.goto(path)
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible()
    await page.waitForLoadState("networkidle")
    await expect(page.getByRole("alert")).toHaveCount(0)
    expect(problems).toEqual([])
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1)
  })
}

test("live title and workflow pages open from the library and attention list", async ({ page }) => {
  const problems = await readOnly(page)
  await page.goto("/bibliotek")
  await page.locator("main ul li a").first().click()
  await expect(page.getByRole("link", { name: /Bibliotek/ })).toBeVisible()
  await page.goto("/handling")
  const details = page.getByRole("link", { name: "Detaljer" }).first()
  if (await details.count()) {
    await details.click()
    await expect(page.getByRole("heading", { name: "Hvad kan du gøre?" })).toBeVisible()
  }
  expect(problems).toEqual([])
})
