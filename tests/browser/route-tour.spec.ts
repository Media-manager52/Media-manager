import AxeBuilder from "@axe-core/playwright"
import { expect, test, type Page } from "@playwright/test"
import path from "node:path"

import { routeTitles } from "../../lib/media-manager-routes"
import { installOwnerAPI, ownerFixtureIDs } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"
const screenshotDir = process.env.MM_TOUR_SCREENSHOTS

const routes = [
  ...Object.keys(routeTitles),
  `/media/movies/${ownerFixtureIDs.movieA}`,
  `/media/series/${ownerFixtureIDs.series}`,
  `/files/${ownerFixtureIDs.fileA}`,
  `/sources/indexers/${ownerFixtureIDs.indexer}`,
]

const viewports = [
  { name: "desktop", width: 1440, height: 900, theme: "dark", axe: true },
  { name: "light", width: 1440, height: 900, theme: "light", axe: true },
  { name: "mobile", width: 390, height: 844, theme: "dark", axe: false },
] as const

async function settle(page: Page) {
  await page.waitForLoadState("networkidle")
  // Screens render persisted state after their loaders resolve; wait until no
  // loading placeholder remains so every assertion sees the settled screen.
  await expect(page.getByText(/^Loading/)).toHaveCount(0, { timeout: 10_000 })
}

for (const viewport of viewports) {
  test.describe(`${viewport.name} route tour`, () => {
    for (const route of routes) {
      test(`${route} renders cleanly`, async ({ page }) => {
        await page.setViewportSize({ width: viewport.width, height: viewport.height })
        await page.addInitScript((theme) => { window.localStorage.setItem("mm-theme", theme) }, viewport.theme)
        const recorder = await installOwnerAPI(page, baseURL)
        const errors: string[] = []
        page.on("pageerror", (error) => errors.push(error.message))
        page.on("console", (message) => { if (message.type() === "error") errors.push(message.text()) })

        await page.goto(route)
        await settle(page)

        await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible()
        if (route === "/intelligence/releases") {
          await expect(page.getByRole("columnheader", { name: "Last seen" })).toBeVisible()
          await expect(page.getByRole("columnheader", { name: "Evidence" })).toBeVisible()
          await expect(page.getByRole("columnheader", { name: "Observed" })).toHaveCount(0)
          await expect(page.getByText("Why this decision").first()).toBeVisible()
        }
        if (route === "/settings/profiles") {
          await expect(page.getByRole("columnheader", { name: "Preset" })).toBeVisible()
        }
        const overflow = await page.evaluate(() => ({ viewport: window.innerWidth, document: document.documentElement.scrollWidth }))
        expect(overflow.document, "page must not scroll horizontally").toBeLessThanOrEqual(overflow.viewport)

        if (viewport.axe) {
          const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze()
          const serious = results.violations
            .filter((violation) => violation.impact === "serious" || violation.impact === "critical")
            .map((violation) => `${violation.id}: ${violation.nodes.slice(0, 3).map((node) => node.target.join(" ")).join(" | ")}`)
          expect(serious).toEqual([])
        }

        if (screenshotDir) {
          const name = route === "/" ? "overview" : route.slice(1).replaceAll("/", "_")
          await page.screenshot({ path: path.join(screenshotDir, `${viewport.name}-${name}.png`), fullPage: true })
        }

        // Failed requests to blocked artwork hosts surface as console errors in
        // some engines; only application errors fail the tour.
        expect(errors.filter((message) => !message.includes("net::ERR_BLOCKED_BY_CLIENT"))).toEqual([])
        expect(recorder.unexpected).toEqual([])
      })
    }
  })
}
