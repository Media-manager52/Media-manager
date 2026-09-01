import { expect, test } from "@playwright/test"

const baseURL = "http://127.0.0.1:4174"

test("workflow queue shows truthful bounded recovery and blocks a third acquisition", async ({ page }) => {
  const unexpected: string[] = []
  const retryID = "11111111-1111-4111-8111-111111111111"
  const exhaustedID = "22222222-2222-4222-8222-222222222222"
  let retried = false
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
      state: "needs_attention",
      revision: 12,
      attention_code: "subtitle_requirements_unverified",
      attention_detail: "Required Danish subtitles were not verified.",
      acquisition_attempts: 2,
      retained_output_count: 2,
      excluded_release_count: 2,
      search_allowed: false,
      search_blocked_reason: "Two acquisition attempts were used; failed output remains retained for owner review.",
      adopted_replacement_confirmation_required: false,
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T02:00:00Z",
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
    if (request.method() === "GET" && url.pathname === "/api/v1/system/info") {
      await route.fulfill({ status: 200, json: { data: { version: "browser-test" } } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/health") {
      await route.fulfill({ status: 200, json: { data: { database_state: "ready", schema_version: 31, expected_schema: 31 } } })
      return
    }
    unexpected.push(`${request.method()} ${url.pathname}${url.search}`)
    await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
  })

  await page.goto("/activity/queue")
  const retryRow = page.getByRole("row").filter({ hasText: "Retry Season" })
  await expect(retryRow).toContainText("Attempts 1 / 2")
  await expect(retryRow).toContainText("1 retained output")
  await retryRow.getByRole("button", { name: "Retry" }).click()
  await expect.poll(() => retried).toBe(true)

  const exhaustedRow = page.getByRole("row").filter({ hasText: "Exhausted Season" })
  await expect(exhaustedRow).toContainText("Attempts 2 / 2")
  await expect(exhaustedRow).toContainText("2 retained outputs")
  await expect(exhaustedRow).toContainText("Two acquisition attempts were used")
  await expect(exhaustedRow.getByRole("button", { name: "Retry" })).toBeDisabled()
  expect(unexpected).toEqual([])
})
