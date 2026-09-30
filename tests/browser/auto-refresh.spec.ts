import { expect, test } from "@playwright/test"

import { installOwnerAPI } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"

async function setVisibility(page: import("@playwright/test").Page, state: "visible" | "hidden") {
  await page.evaluate((next) => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => next })
    document.dispatchEvent(new Event("visibilitychange"))
  }, state)
}

test("read-only screens re-read persisted state while visible and pause in a background tab", async ({ page }) => {
  await page.clock.install()
  const recorder = await installOwnerAPI(page, baseURL)
  let reads = 0
  page.on("request", (request) => {
    if (request.method() === "GET" && new URL(request.url()).pathname === "/api/v1/requests") reads += 1
  })

  await page.goto("/requests")
  await expect(page.getByRole("heading", { name: "Requests", level: 1 })).toBeVisible()
  await expect.poll(() => reads).toBe(1)

  await page.clock.fastForward(16_000)
  await expect.poll(() => reads).toBe(2)

  await setVisibility(page, "hidden")
  await page.clock.fastForward(60_000)
  await page.waitForTimeout(200)
  expect(reads).toBe(2)

  await setVisibility(page, "visible")
  await expect.poll(() => reads).toBe(3)
  expect(recorder.unexpected).toEqual([])
})
