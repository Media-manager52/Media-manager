import { expect, test, type Page } from "@playwright/test"

const baseURL = "http://127.0.0.1:4174"

// Rows shaped like production attention work: long provider failure details,
// a long release title, and several recovery lines.
const workflows = [
  {
    id: "a1111111-1111-4111-8111-111111111111",
    media_id: "b1111111-1111-4111-8111-111111111111",
    media_kind: "series",
    title: "Forbrydelser der rystede Danmark",
    year: 2019,
    variant: "season:03:episode:04",
    state: "needs_attention",
    revision: 6,
    attention_code: "release_retrieval_failed",
    attention_detail: "selected provider rejected NZB access (newznab_http_403); avoid this provider for the remainder of this round; no eligible, untried candidate remains on a commissioned provider without an access rejection in this round.",
    owner_action_required: true,
    acquisition_attempts: 0,
    round_ordinal: 1,
    round_state: "open",
    round_slots_used: 0,
    round_attempt_limit: 2,
    retained_output_count: 0,
    retained_output_bytes: 0,
    excluded_release_count: 1,
    search_allowed: true,
    retry_discovery_state: "waiting",
    next_rescan_at: "2026-10-01T01:06:00Z",
    last_discovery_at: "2026-09-30T01:06:00Z",
    last_discovery_complete: false,
    search_blocked_reason: "The indexer that supplied the selected release rejected NZB access for this round.",
    adopted_replacement_confirmation_required: false,
    created_at: "2026-09-28T00:00:00Z",
    updated_at: "2026-09-30T01:18:00Z",
  },
  {
    id: "a2222222-2222-4222-8222-222222222222",
    media_id: "b2222222-2222-4222-8222-222222222222",
    media_kind: "series",
    title: "CSI: Crime Scene Investigation",
    year: 2000,
    variant: "season:12",
    state: "needs_attention",
    revision: 9,
    attention_code: "altmount_job_attention",
    attention_detail: "fast-fail segment check inconclusive: fast-fail validation inconclusive: 1 segment(s) remained unverified after 3 attempts; the provider may be missing articles for this post.",
    owner_action_required: true,
    acquisition_attempts: 2,
    round_ordinal: 2,
    round_state: "exhausted",
    round_slots_used: 1,
    round_attempt_limit: 2,
    retained_output_count: 0,
    retained_output_bytes: 0,
    excluded_release_count: 2,
    retry_discovery_state: "ready",
    ready_release_title: "CSI.Crime.Scene.Investigation.S12.NORDiC.1080p.WEB-DL.DD5.1.H.264-TWASERIES-EXTRA-LONG-GROUP-NAME",
    ready_indexer_name: "NZBFinder {DK}",
    ready_first_observed_at: "2026-09-29T20:00:00Z",
    open_next_round_allowed: true,
    adopted_replacement_confirmation_required: false,
    created_at: "2026-09-20T00:00:00Z",
    updated_at: "2026-09-29T21:00:00Z",
  },
  {
    id: "a3333333-3333-4333-8333-333333333333",
    media_id: "b3333333-3333-4333-8333-333333333333",
    media_kind: "movie",
    title: "The Prosecutor, the Defender, the Father and his Son",
    year: 2016,
    variant: "default",
    state: "waiting",
    revision: 3,
    acquisition_attempts: 0,
    round_ordinal: 1,
    round_state: "open",
    round_slots_used: 0,
    round_attempt_limit: 2,
    retained_output_count: 0,
    retained_output_bytes: 0,
    excluded_release_count: 0,
    search_allowed: true,
    adopted_replacement_confirmation_required: false,
    created_at: "2026-09-28T00:00:00Z",
    updated_at: "2026-09-30T08:00:00Z",
  },
]

async function openQueue(page: Page, width: number) {
  const unexpected: string[] = []
  await page.setViewportSize({ width, height: 900 })
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
    if (request.method() === "GET" && (url.pathname === "/api/v1/media" || url.pathname === "/api/v1/wanted")) {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/info") {
      await route.fulfill({ status: 200, json: { data: { version: "browser-test" } } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/health") {
      await route.fulfill({ status: 200, json: { data: { database_state: "ready", schema_version: 42, expected_schema: 42 } } })
      return
    }
    unexpected.push(`${request.method()} ${url.pathname}${url.search}`)
    await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
  })
  await page.goto("/activity/queue")
  await expect(page.getByText("Forbrydelser der rystede Danmark")).toBeVisible()
  return unexpected
}

// Long attention and recovery text must wrap inside its own cell instead of
// being drawn across the neighbouring columns.
async function cellsSpillingText(page: Page) {
  return page.evaluate(() => [...document.querySelectorAll("table tbody td")]
    .filter((cell) => cell.scrollWidth > cell.clientWidth + 1)
    .map((cell) => `${cell.scrollWidth}>${cell.clientWidth}: ${(cell.textContent ?? "").slice(0, 60)}`))
}

for (const width of [1280, 1440]) {
  test(`workflow table text stays inside its cells at ${width}px`, async ({ page }) => {
    const unexpected = await openQueue(page, width)
    expect(await cellsSpillingText(page)).toEqual([])
    expect(unexpected).toEqual([])
  })
}

test("workflow table shows every column at 1440px without sideways scrolling", async ({ page }) => {
  const unexpected = await openQueue(page, 1440)
  const fit = await page.evaluate(() => {
    const container = document.querySelector("table")?.parentElement
    return container ? { scroll: container.scrollWidth, visible: container.clientWidth } : null
  })
  expect(fit).not.toBeNull()
  expect(fit!.scroll, `table is ${fit!.scroll}px inside a ${fit!.visible}px panel`).toBeLessThanOrEqual(fit!.visible + 1)
  await expect(page.getByRole("columnheader", { name: "Actions" })).toBeInViewport()
  expect(unexpected).toEqual([])
})
