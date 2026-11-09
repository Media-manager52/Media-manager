import { readFile } from "node:fs/promises"

import { expect, test, type Page } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

const workflow = { id: ids.attention, media_id: ids.movie, media_kind: "movie", title: "Testfilmen", year: 2024, variant: "default", state: "waiting", revision: 1, created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-09T00:00:00Z", adopted_replacement_confirmation_required: false, available_actions: [] }
const rejected = { id: "c1", title: "Testfilmen.2024.1080p", indexer_id: "i", indexer_name: "I", indexer_kind: "newznab", decision: { verdict: "rejected", decision_explanation: { reasons: [{ decision: "fail", rule_kind: "required", rule: "Danske undertekster", reason: "observed evidence does not satisfy the rule" }] } } }
const verification = { id: "v", workflow_id: ids.attention, altmount_job_id: "j", candidate_id: "c", media_title: "Testfilmen", release_title: "R", attempt_generation: 1, status: "verified", required_languages: ["da"], preferred_languages: [], observed_languages: ["da"], failure_reasons: [], retry_state: "not_needed", can_reverify: false, created_at: "2026-11-09T00:00:00Z", evidence: { videos: [{ languages: [{ kind: "embedded", canonical: "da", track_index: 4 }] }] } }

async function open(page: Page, verifications: unknown = [verification]) {
  await mockApi(page)
  await page.route(`**/api/v1/workflows/${ids.attention}`, (route) => route.fulfill({ json: { data: workflow } }))
  await page.route(`**/api/v1/workflows/${ids.attention}/candidates*`, (route) => route.fulfill({ json: { data: [rejected] } }))
  await page.route("**/api/v1/acquisition-verifications*", (route) => verifications === "fail" ? route.fulfill({ status: 500, json: { error: { code: "database_error", message: "x" } } }) : route.fulfill({ json: { data: verifications } }))
  await page.goto(`/arbejde/${ids.attention}`)
}

test("the work page answers the owner's three questions and shows the language proof", async ({ page }) => {
  await open(page)
  await expect(page.getByText("Hvorfor har jeg den ikke endnu?")).toBeVisible()
  await expect(page.getByText(/Hyppigste grund: Danske undertekster: opfylder ikke kravet/)).toBeVisible()
  await expect(page.getByText("Den søger ikke selv videre.")).toBeVisible()
  await expect(page.getByText("Nej. Media Manager klarer det selv.")).toBeVisible()
  await expect(page.getByText("Dansk tekst fundet i filens spor 4.")).toBeVisible()
})

test("a failed language lookup is reported, never shown as no result", async ({ page }) => {
  await open(page, "fail")
  await expect(page.getByText("Kunne ikke hentes lige nu.")).toBeVisible()
})

test("Vurder igen is read-only and says so", async ({ page }) => {
  await open(page)
  let dryRuns = 0
  await page.route(`**/api/v1/workflows/${ids.attention}/candidates/dry-run`, (route) => { dryRuns++; return route.fulfill({ json: { data: { workflow_id: ids.attention, policy_version: 1, profile_policy_version: 1, policy_source: "profile", generated_at: "2026-11-09T00:00:00Z", truncated: false, limitations: [], ranked_candidate_ids: ["c1"], best_eligible_candidate_id: "c1", candidates: [{ candidate_id: "c1", title: "Testfilmen.2024.1080p", indexer_id: "i", indexer_kind: "newznab", evaluation: { observation_id: "o", verdict: "eligible", score: 1, explanations: [] }, explanation: {}, attempted: false, excluded: false, selected: false, first_seen_sequence: 1 }] } } }) })
  expect(dryRuns).toBe(0)
  await page.getByRole("button", { name: "Vurder igen" }).click()
  await expect(page.getByRole("status").filter({ hasText: "Efter profilens nuværende regler" })).toContainText("1 af 1 kendte udgivelser")
  await expect(page.getByRole("status").filter({ hasText: "Efter profilens" })).toContainText("Intet er hentet eller ændret.")
  expect(dryRuns).toBe(1)
})

test("Hent supportfil downloads the redacted text from the server", async ({ page }) => {
  await mockApi(page)
  await page.route("**/api/v1/problems/support", (route) => route.fulfill({ contentType: "text/plain", body: "Media Manager support bundle\n" }))
  await page.goto("/system")
  const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: "Hent supportfil" }).click()])
  expect(download.suggestedFilename()).toBe("media-manager-support.txt")
  expect(await readFile(await download.path(), "utf-8")).toContain("support bundle")
})
