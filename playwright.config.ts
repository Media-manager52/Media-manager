import { defineConfig, devices } from "@playwright/test"

export default defineConfig({
  testDir: "./tests/browser",
  outputDir: ".sites-runtime/playwright-results",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: "line",
  use: {
    baseURL: "http://127.0.0.1:4174",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    video: "off",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: {
    // The built bundle targets Workers and imports cloudflare:workers; the
    // production container registers the same inert loader (see Dockerfile).
    command: "NODE_OPTIONS=--import=./scripts/register-cloudflare-loader.mjs bash scripts/sites-env.sh -- ./node_modules/.bin/vinext start --hostname 127.0.0.1 --port 4174",
    url: "http://127.0.0.1:4174/media",
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
