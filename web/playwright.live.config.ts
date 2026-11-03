import { defineConfig } from "@playwright/test"

// Read-only acceptance against the deployed site through the loopback proxy
// started by scripts/test-live-website.sh. Every non-GET request is refused
// in the browser, so the suite cannot change production.
const port = process.env.MEDIA_MANAGER_LIVE_PORT ?? ""
if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65_535) throw new Error("A valid live-test port is required")

export default defineConfig({
  testDir: "./tests/live",
  outputDir: "../.sites-runtime/live-playwright-results",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: "line",
  timeout: 45_000,
  expect: { timeout: 15_000 },
  use: { baseURL: `http://127.0.0.1:${port}`, actionTimeout: 8_000, navigationTimeout: 20_000, screenshot: "only-on-failure", trace: "off", video: "off", timezoneId: "Europe/Copenhagen" },
  projects: [
    { name: "desktop", use: { browserName: "chromium", viewport: { width: 1280, height: 853 } } },
    { name: "tablet", use: { browserName: "chromium", viewport: { width: 820, height: 1180 } } },
    { name: "mobile", use: { browserName: "chromium", viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
})
