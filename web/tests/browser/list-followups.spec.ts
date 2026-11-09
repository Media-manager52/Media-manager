import { expect, test } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

test("a saved connection test stays visible and says when", async ({ page }) => {
  await mockApi(page)
  const component = (id: string, kind: string, extra: object) => ({ id, kind, name: kind, enabled: true, is_default: true, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", configuration_state: "configured", consecutive_failure: 0, ...extra })
  await page.route("**/api/v1/system/health", (route) => route.fulfill({ json: { data: { database_state: "ready", database_latency_ms: 1, schema_version: 57, expected_schema: 57, outbox: { pending: 0, due: 0, retrying: 0 }, components: [component("c-plex", "plex", { last_check_ok: true, last_check_at: "2026-11-09T10:00:00Z" }), component("c-altmount", "altmount", { last_check_ok: false, last_check_at: "2026-11-09T11:00:00Z" })], checked_at: "2026-11-09T12:00:00Z" } } }))
  await page.route("**/api/v1/setup/status", (route) => route.fulfill({ json: { data: { complete: true, secret_storage_enabled: true, servarr_enabled: false, seerr_intake_enabled: false, steps: ["plex", "altmount"].map((id) => ({ id, done: true, optional: false, detail: "Saved" })) } } }))
  await page.goto("/kom-i-gang")
  await expect(page.getByText(/Testet senest .*: virkede\./)).toHaveCount(1)
  await expect(page.getByText(/Testet senest .*: virkede ikke\./)).toHaveCount(1)
  await expect(page.getByText("Ikke testet endnu")).toHaveCount(0)
})

test("the library says when status was checked and can check again", async ({ page }) => {
  await mockApi(page)
  let requests = 0
  await page.route("**/api/v1/media/status*", (route) => { requests++; return route.fulfill({ json: { data: [] } }) })
  await page.goto("/bibliotek")
  await expect(page.getByText(/^Status kontrolleret /)).toBeVisible()
  const before = requests
  await page.getByRole("button", { name: "Kontrollér igen" }).click()
  await expect.poll(() => requests).toBeGreaterThan(before)
})

test("the backup page names the latest restore test and not more", async ({ page }) => {
  await mockApi(page)
  await page.route("**/api/v1/backups", (route) => route.fulfill({ json: { data: { configured: true, format: "mmbackup", restore_verification: "isolated", automatic_deletion: false, production_restore: false, offsite: { configured: false, kind: "" }, records: [
    { id: "a", state: "verified", format: "mmbackup", size_bytes: 10, created_at: "2026-11-01T00:00:00Z", completed_at: "2026-11-01T01:00:00Z" },
    { id: "b", state: "failed", format: "mmbackup", size_bytes: 0, created_at: "2026-11-08T00:00:00Z", completed_at: "2026-11-08T01:00:00Z" },
  ] } } }))
  await page.goto("/system/backup")
  const line = page.getByText(/^Seneste prøvegendannelse af databasen: /)
  await expect(line).toBeVisible()
  // The failed backup a week later must not count as a restore test.
  await expect(line).not.toContainText("8.")
  await expect(page.getByText(/er ikke dokumenteret her/)).toBeVisible()
})

test("an indexer's language claim is labelled as unchecked", async ({ page }) => {
  await mockApi(page)
  const workflow = { id: ids.attention, media_id: ids.movie, media_kind: "movie", title: "Testfilmen", year: 2024, variant: "default", state: "waiting", revision: 1, created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-01T00:00:00Z" }
  const candidate = { id: "c1", title: "Testfilmen.2024.1080p", indexer_id: "i", indexer_name: "I", indexer_kind: "newznab", decision: { verdict: "eligible", decision_explanation: { version: 1, verdict: "eligible", score: 1, reasons: [], evidence_scope: "current_linked_observation", evidence: [{ fact: "subtitle_languages", category: "language", state: "observed", values: ["da"] }] } } }
  await page.route(`**/api/v1/workflows/${ids.attention}`, (route) => route.fulfill({ json: { data: workflow } }))
  await page.route(`**/api/v1/workflows/${ids.attention}/candidates*`, (route) => route.fulfill({ json: { data: [candidate] } }))
  await page.route("**/api/v1/acquisition-verifications*", (route) => route.fulfill({ json: { data: [] } }))
  await page.goto(`/arbejde/${ids.attention}`)
  await expect(page.getByText("Dansk nævnt af indexeren, men endnu ikke kontrolleret.")).toBeVisible()
})
