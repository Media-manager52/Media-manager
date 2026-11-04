import { defineConfig, devices } from "@playwright/test"

// README screenshots (tests/screenshots) from invented demo data against the
// production bundle. Run with `npm run build && npm run screenshots`.
export default defineConfig({
  testDir: "./tests/screenshots",
  outputDir: "test-results/screenshots",
  workers: 1,
  retries: 0,
  reporter: "line",
  use: {
    ...devices["Desktop Chrome"],
    baseURL: "http://127.0.0.1:4175",
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
    timezoneId: "Europe/Copenhagen",
    locale: "da-DK",
  },
  webServer: {
    command: "PORT=4175 HOST=127.0.0.1 node server.mjs",
    url: "http://127.0.0.1:4175/",
    reuseExistingServer: false,
    timeout: 60_000,
  },
})
