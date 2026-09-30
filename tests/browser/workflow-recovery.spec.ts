import { expect, test } from "@playwright/test"

const baseURL = "http://127.0.0.1:4174"

test("workflow queue shows durable rounds and requires owner approval for a new round", async ({ page }) => {
  const unexpected: string[] = []
  const retryID = "11111111-1111-4111-8111-111111111111"
  const exhaustedID = "22222222-2222-4222-8222-222222222222"
  const readyID = "55555555-5555-4555-8555-555555555555"
  const infrastructureID = "77777777-7777-4777-8777-777777777777"
  let retried = false
  let nextRoundStarted = false
  let infrastructureReconciled = false
  const workflows = [
    {
      id: retryID,
      media_id: "33333333-3333-4333-8333-333333333333",
      media_kind: "series",
      title: "Retry Season",
      year: 2024,
      variant: "season:01",
      state: "needs_attention",
      revision: 8,
      attention_code: "output_verification_failed",
      attention_detail: "Completed output is missing one expected episode.",
      acquisition_attempts: 1,
      round_ordinal: 1,
      round_state: "open",
      round_slots_used: 1,
      round_attempt_limit: 2,
      retry_discovery_state: "paused",
      retained_output_count: 1,
      excluded_release_count: 1,
      search_allowed: true,
      adopted_replacement_confirmation_required: false,
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T01:00:00Z",
    },
    {
      id: exhaustedID,
      media_id: "44444444-4444-4444-8444-444444444444",
      media_kind: "series",
      title: "Exhausted Season",
      year: 2023,
      variant: "season:02",
      state: "waiting",
      revision: 12,
      attention_code: "subtitle_requirements_unverified",
      attention_detail: "Required Danish subtitles were not verified.",
      acquisition_attempts: 2,
      round_ordinal: 1,
      round_state: "exhausted",
      round_slots_used: 2,
      round_attempt_limit: 2,
      retry_discovery_state: "waiting",
      next_rescan_at: "2026-09-03T02:00:00Z",
      last_discovery_at: "2026-09-02T02:00:00Z",
      last_discovery_complete: false,
      retained_output_count: 2,
      retained_output_bytes: 4294967296,
      excluded_release_count: 2,
      search_allowed: false,
      search_blocked_reason: "The round is exhausted; scheduled discovery is waiting for a newly observed eligible release.",
      adopted_replacement_confirmation_required: false,
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T02:00:00Z",
    },
    {
      id: readyID,
      media_id: "66666666-6666-4666-8666-666666666666",
      media_kind: "movie",
      title: "Ready Movie",
      year: 2025,
      variant: "default",
      state: "waiting",
      revision: 14,
      attention_code: "round_exhausted_waiting_new_identity",
      attention_detail: "Waiting for a newly observed eligible release.",
      acquisition_attempts: 2,
      round_ordinal: 1,
      round_state: "exhausted",
      round_slots_used: 2,
      round_attempt_limit: 2,
      retry_discovery_state: "ready",
      last_discovery_at: "2026-09-02T03:00:00Z",
      last_discovery_complete: true,
      new_release_ready: true,
      ready_candidate_id: "99999999-9999-4999-8999-999999999999",
      ready_release_title: "Ready.Movie.2025.1080p.WEB-DL",
      ready_indexer_name: "Fixture Indexer",
      ready_first_observed_at: "2026-09-02T02:30:00Z",
      open_next_round_allowed: true,
      retained_output_count: 2,
      retained_output_bytes: 3221225472,
      excluded_release_count: 2,
      search_allowed: false,
      search_blocked_reason: "A new eligible release is ready; the owner must start the next acquisition round.",
      adopted_replacement_confirmation_required: false,
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-02T03:00:00Z",
    },
    {
      id: infrastructureID,
      media_id: "88888888-8888-4888-8888-888888888888",
      media_kind: "movie",
      title: "Retained Infrastructure Job",
      year: 2026,
      variant: "default",
      state: "needs_attention",
      revision: 6,
      attention_code: "output_inspection_failed",
      attention_detail: "The bounded verifier was unavailable.",
      acquisition_attempts: 1,
      round_ordinal: 1,
      round_state: "open",
      round_slots_used: 1,
      round_attempt_limit: 2,
      retained_output_count: 1,
      retained_output_bytes: 1073741824,
      excluded_release_count: 0,
      search_allowed: false,
      search_blocked_reason: "Recover the current AltMount job instead of searching for another release.",
      adopted_replacement_confirmation_required: false,
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-02T04:00:00Z",
    },
  ]

  await page.route("**/*", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.origin !== baseURL) {
      unexpected.push(`${request.method()} ${url.origin}${url.pathname}`)
      await route.abort("blockedbyclient")
      return
    }
    if (!url.pathname.startsWith("/api/v1/")) {
      await route.continue()
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/workflows") {
      await route.fulfill({ status: 200, json: { data: workflows } })
      return
    }
    if (request.method() === "POST" && url.pathname === `/api/v1/workflows/${retryID}/search`) {
      retried = true
      await route.fulfill({ status: 202, json: { data: { status: "search_queued", id: retryID } } })
      return
    }
    if (request.method() === "POST" && url.pathname === `/api/v1/workflows/${readyID}/acquisition-rounds`) {
      nextRoundStarted = true
      await route.fulfill({ status: 202, json: { data: { status: "round_opened", id: readyID } } })
      return
    }
    if (request.method() === "POST" && url.pathname === `/api/v1/workflows/${infrastructureID}/reconcile-altmount`) {
      infrastructureReconciled = true
      await route.fulfill({ status: 202, json: { data: { status: "reconciliation_queued", id: infrastructureID } } })
      return
    }
    // The control-room board loads catalog artwork once; none exists here.
    if (request.method() === "GET" && url.pathname === "/api/v1/media") {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    // The shell loads owner attention on every page.
    if (request.method() === "GET" && url.pathname === "/api/v1/wanted") {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/info") {
      await route.fulfill({ status: 200, json: { data: { version: "browser-test" } } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/health") {
      await route.fulfill({ status: 200, json: { data: { database_state: "ready", schema_version: 32, expected_schema: 32 } } })
      return
    }
    unexpected.push(`${request.method()} ${url.pathname}${url.search}`)
    await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
  })

  await page.goto("/activity/queue")
  const retryRow = page.getByRole("row").filter({ hasText: "Retry Season" })
  await expect(retryRow).toContainText("Round 1 · 1/2 slots · 1 lifetime")
  await expect(retryRow).toContainText("1 retained")
  await retryRow.getByRole("button", { name: "Retry" }).click()
  await expect.poll(() => retried).toBe(true)

  const exhaustedRow = page.getByRole("row").filter({ hasText: "Exhausted Season" })
  await expect(exhaustedRow).toContainText("Round 1 · 2/2 slots · 2 lifetime")
  await expect(exhaustedRow).toContainText("2 retained")
  await expect(exhaustedRow).toContainText("Last discovery partial")
  await expect(exhaustedRow.getByRole("button", { name: "Start next round" })).toBeDisabled()

  const readyRow = page.getByRole("row").filter({ hasText: "Ready Movie" })
  await expect(readyRow).toContainText("New eligible exact identity found")
  await expect(readyRow).toContainText("Ready.Movie.2025.1080p.WEB-DL · Fixture Indexer")
  await readyRow.getByRole("button", { name: "Start next round" }).click()
  await expect.poll(() => nextRoundStarted).toBe(true)

  const infrastructureRow = page.getByRole("row").filter({ hasText: "Retained Infrastructure Job" })
  await expect(infrastructureRow).toContainText("Recover the current AltMount job")
  await infrastructureRow.getByRole("button", { name: "Reconcile same job" }).click()
  await expect.poll(() => infrastructureReconciled).toBe(true)
  expect(unexpected).toEqual([])
})
