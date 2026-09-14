import { defineConfig } from "@playwright/test"

const port = process.env.MEDIA_MANAGER_LIVE_PORT ?? ""

if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65_535) {
  throw new Error("A valid live-test port is required")
}

const baseURL = `http://127.0.0.1:${port}`

export default defineConfig({
  testDir: "./tests/live-browser",
  outputDir: ".sites-runtime/live-playwright-results",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: "line",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL,
    actionTimeout: 8_000,
    navigationTimeout: 15_000,
    screenshot: "off",
    trace: "off",
    video: "off",
  },
  projects: [
    { name: "desktop", use: { browserName: "chromium", viewport: { width: 1280, height: 853 } } },
    { name: "tablet", use: { browserName: "chromium", viewport: { width: 820, height: 1180 } } },
    { name: "mobile", use: { browserName: "chromium", viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
})
