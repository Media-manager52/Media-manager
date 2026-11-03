import { defineConfig, devices } from "@playwright/test"

// Browser tests run the production bundle behind the production server with a
// mocked API (tests/browser/mock-api.ts), so they need no backend or secrets.
export default defineConfig({
  testDir: "./tests/browser",
  outputDir: "test-results",
  fullyParallel: true,
  workers: process.env.CI ? 2 : 4,
  retries: 0,
  reporter: "line",
  use: { baseURL: "http://127.0.0.1:4174", screenshot: "only-on-failure", trace: "retain-on-failure", timezoneId: "Europe/Copenhagen", locale: "da-DK" },
  projects: [
    { name: "desktop", use: { ...devices["Desktop Chrome"] } },
    { name: "mobile", use: { ...devices["Pixel 7"] } },
    { name: "dark", testMatch: /pages\.spec\.ts/, use: { ...devices["Desktop Chrome"], colorScheme: "dark" } },
  ],
  webServer: {
    command: "PORT=4174 HOST=127.0.0.1 node server.mjs",
    url: "http://127.0.0.1:4174/",
    reuseExistingServer: false,
    timeout: 60_000,
  },
})
