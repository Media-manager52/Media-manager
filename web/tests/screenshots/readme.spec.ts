import { expect, test } from "@playwright/test"

import { demoSeriesID, mockDemo } from "./demo-data"

// Regenerates the README screenshots in docs/images from invented demo data:
//   cd web && npm run build && npm run screenshots
// Not part of the normal browser suite (playwright.config.ts runs tests/browser).

const shots: Array<{ file: string; path: string; heading: string; scheme: "light" | "dark" }> = [
  { file: "overview.png", path: "/", heading: "Oversigt", scheme: "light" },
  { file: "overview-dark.png", path: "/", heading: "Oversigt", scheme: "dark" },
  { file: "library.png", path: "/bibliotek", heading: "Bibliotek", scheme: "light" },
  { file: "title.png", path: `/bibliotek/${demoSeriesID}`, heading: "Havnefogeden", scheme: "light" },
  { file: "attention.png", path: "/handling", heading: "Kræver handling", scheme: "light" },
  { file: "calendar.png", path: "/kalender", heading: "Kalender", scheme: "light" },
]

for (const shot of shots) {
  test(shot.file, async ({ page }) => {
    await page.emulateMedia({ colorScheme: shot.scheme })
    await mockDemo(page)
    await page.goto(shot.path)
    await expect(page.getByRole("heading", { level: 1, name: shot.heading })).toBeVisible()
    await page.waitForLoadState("networkidle")
    await page.evaluate(async () => {
      await document.fonts.ready
      await Promise.all(Array.from(document.images, (image) => (image.complete ? undefined : new Promise((done) => { image.onload = image.onerror = done }))))
    })
    await page.screenshot({ path: `../docs/images/${shot.file}` })
  })
}
