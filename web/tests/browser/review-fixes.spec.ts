import { expect, test } from "@playwright/test"
import { mockApi } from "./mock-api"

test("Henter nu pages beyond 200 workflows using one combined state filter", async ({ page }) => {
  await mockApi(page)
  const workflows = Array.from({ length: 201 }, (_, i) => ({
    id: `aaaaaaaa-aaaa-4aaa-8aaa-${String(i).padStart(12, "0")}`, media_id: `bbbbbbbb-bbbb-4bbb-8bbb-${String(i).padStart(12, "0")}`,
    media_kind: "movie", title: `Paging item ${i}`, year: 2026, variant: "default", state: "searching", revision: 1,
    created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-01T00:00:00Z", available_actions: [],
  }))
  const offsets: number[] = []
  await page.route("**/api/v1/workflows?**", async (route) => {
    const url = new URL(route.request().url())
    expect(url.searchParams.get("state")).toBe("acquiring")
    const offset = Number(url.searchParams.get("offset"))
    const limit = Number(url.searchParams.get("limit"))
    offsets.push(offset)
    await route.fulfill({ json: { data: workflows.slice(offset, offset + limit) } })
  })
  await page.goto("/arbejde")
  await expect(page.getByRole("link", { name: /Paging item/ })).toHaveCount(100)
  await page.getByRole("button", { name: "Næste", exact: true }).click()
  await expect(page.getByRole("link", { name: /Paging item 100 / })).toBeVisible()
  await page.getByRole("button", { name: "Næste", exact: true }).click()
  await expect(page.getByRole("link", { name: /Paging item 200 / })).toBeVisible()
  await expect(page.getByRole("button", { name: "Næste", exact: true })).toBeDisabled()
  await page.getByRole("button", { name: "Forrige", exact: true }).click()
  await expect(page.getByRole("link", { name: /Paging item 100 / })).toBeVisible()
  expect(offsets).toEqual(expect.arrayContaining([0, 100, 200]))
})

test("Kilder shows the enforced provider limit and explains blocked downloads", async ({ page }) => {
  await mockApi(page)
  await page.route("**/api/v1/indexers", async (route) => route.fulfill({ json: { data: [{
    id: "55555555-5555-4555-8555-555555555555", name: "Quota test", kind: "newznab", enabled: true, commissioned: true,
    budget: { query_limit: 1000, grab_limit: 100, background_query_limit: 600, minimum_interval_ms: 3000, window_seconds: 86400, source: "owner_stated" },
    usage: { queries_used: 5, queries_remaining: 95, background_queries_remaining: 95, grabs_used: 0, grabs_remaining: 0 }, pacing: { consecutive_failures: 0 },
    effective_budget: { queries: { limit: 100, used: 5, remaining: 95, blocked: false }, grabs: { limit: 30, used: 30, remaining: 0, blocked: true }, background_queries: { limit: 100, used: 5, remaining: 95, blocked: false } },
    provider_counters: { grabs: { maximum: 30, used: 30, observed_at: "2026-11-01T12:00:00Z", maximum_observed_at: "2026-11-01T12:00:00Z" } },
  }] } }))
  await page.goto("/kilder")
  await expect(page.getByText("Kvote opbrugt", { exact: true })).toBeVisible()
  await expect(page.getByText("30 / 30", { exact: true })).toBeVisible()
  await expect(page.getByText(/Hentninger venter på ledig kvote/)).toBeVisible()
  await expect(page.getByText(/Hentninger hos kilden: 30 \/ 30/)).toBeVisible()
  await expect(page.getByText(/Egne reservationer: 5 søgninger · 0 hentninger/)).toBeVisible()
})

test("queue timings distinguish measured work from the older unfinished queue", async ({ page }) => {
  await mockApi(page)
  await page.route("**/api/v1/system/queue", async (route) => route.fulfill({ json: { data: {
    checked_at: "2026-11-01T13:00:00Z", timing_started_at: "2026-11-01T12:00:00Z", legacy_pending: 7,
    due: 10, scheduled: 0, leased: 0, retrying: 0, completed_last_10m: 2, completed_last_hour: 2,
    by_type: [{ event_type: "workflow.search_requested", due: 10, scheduled: 0, leased: 0, retrying: 0 }],
    timings_last_24h: [{ event_type: "workflow.search_requested", events: 2, retried: 0, attempts: 2, planned_p50_ms: 0, planned_p90_ms: 0, ready_wait_p50_ms: 1000, ready_wait_p90_ms: 60000, active_p50_ms: 100, active_p90_ms: 200, retry_p50_ms: 0, retry_p90_ms: 0 }],
  } } }))
  await page.goto("/system/koe")
  await expect(page.getByRole("heading", { name: "Hvor går tiden hen?" })).toBeVisible()
  await expect(page.getByText(/7 af dem er oprettet før målingens start/)).toBeVisible()
  await expect(page.getByRole("cell", { name: "100 ms / 200 ms", exact: true })).toBeVisible()
})
