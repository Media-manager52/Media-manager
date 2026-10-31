import { expect, test, type Page } from "@playwright/test"

import type { APIReleaseCandidate } from "../../lib/media-manager-api"
import { installOwnerAPI, ownerFixtureIDs } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"
const observed = "2026-09-02T00:00:00Z"

async function openEvidence(page: Page) {
  await page.goto("/intelligence/evidence")
  await page.waitForLoadState("networkidle")
  await expect(page.getByText(/^Loading/)).toHaveCount(0, { timeout: 10_000 })
}

function stateOf(row: ReturnType<Page["getByRole"]>) {
  return row.getByRole("cell").nth(3)
}

// The Evidence explorer prefers the server's structured evidence. Only when a
// candidate carries none does it fall back to the raw stored facts, which the Go
// store writes as an array of {name, values, state} and which therefore keep
// their own recorded state.
test("evidence explorer shows the recorded state of stored fact arrays", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  await openEvidence(page)

  const decided = page.getByRole("row", { name: /First\.Browser\.Movie/ })
  await expect(decided).toHaveCount(2)
  await expect(stateOf(decided.nth(0))).toHaveText("Observed")
  await expect(stateOf(decided.nth(1))).toHaveText("Observed")

  // The fixture's second candidate has no server explanation, so its rows come
  // from the stored fact array: group and resolution, both recorded as observed.
  const stored = page.getByRole("row", { name: /Attention\.Browser\.Movie/ })
  await expect(stored).toHaveCount(2)
  const resolution = stored.filter({ has: page.getByRole("cell", { name: "resolution", exact: true }) })
  await expect(resolution).toHaveCount(1)
  await expect(resolution.getByRole("cell").nth(2)).toHaveText("720p")
  for (const index of [0, 1]) await expect(stateOf(stored.nth(index))).toHaveText("Observed")
  expect(recorder.unexpected).toEqual([])
})

// A plain {fact: value} object carries no per-fact state. It is only ever seen
// from hand-written data, but if it appears the explorer must not present the
// missing state as the provider's "unknown".
test("evidence explorer labels object-shaped facts as not recorded", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  const legacy = {
    id: "d0000002-0000-4000-8000-000000000002",
    workflow_id: ownerFixtureIDs.wfA,
    media_id: ownerFixtureIDs.movieA,
    media_kind: "movie",
    media_title: "First Browser Movie",
    media_year: 2024,
    variant: "movie",
    title: "Legacy.Browser.Movie.2024.720p-OTHER",
    indexer_id: ownerFixtureIDs.indexer,
    indexer_name: "NZB Example",
    reasons: [],
    normalized_facts: { resolution: "720p", group: "OTHER" },
    problems: [],
    selected: false,
    observed_at: observed,
    last_observed_at: observed,
  } as unknown as APIReleaseCandidate
  await page.route("**/api/v1/intelligence/releases*", (route) => route.fulfill({ status: 200, json: { data: [legacy] } }))
  await openEvidence(page)

  const rows = page.getByRole("row", { name: /Legacy\.Browser\.Movie/ })
  await expect(rows).toHaveCount(2)
  const resolution = rows.filter({ has: page.getByRole("cell", { name: "resolution", exact: true }) })
  await expect(resolution.getByRole("cell").nth(2)).toHaveText("720p")
  for (const index of [0, 1]) {
    await expect(stateOf(rows.nth(index))).toHaveText("Not Recorded")
    await expect(stateOf(rows.nth(index))).not.toHaveText("Unknown")
  }
  expect(recorder.unexpected).toEqual([])
})

test("evidence explorer reads stored fact arrays without inventing rows", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  // The shape the Go store actually writes: json.Marshal of the fact slice. This
  // candidate has no decision, so no server explanation is attached.
  const undecided = {
    id: "d0000001-0000-4000-8000-000000000001",
    workflow_id: ownerFixtureIDs.wfA,
    media_id: ownerFixtureIDs.movieA,
    media_kind: "movie",
    media_title: "First Browser Movie",
    media_year: 2024,
    variant: "movie",
    title: "Undecided.Browser.Movie.2024.1080p-GROUP",
    indexer_id: ownerFixtureIDs.indexer,
    indexer_name: "NZB Example",
    reasons: [],
    normalized_facts: [
      { name: "resolution", values: ["1080p"], state: "observed", evidence_at: [0], observed_at: observed },
      { name: "subtitle_languages", values: [], state: "unknown", evidence_at: [], observed_at: observed },
      { values: ["no-name-so-no-row"], state: "observed" },
    ],
    problems: [],
    selected: false,
    observed_at: observed,
    last_observed_at: observed,
  } as unknown as APIReleaseCandidate
  await page.route("**/api/v1/intelligence/releases*", (route) => route.fulfill({ status: 200, json: { data: [undecided] } }))
  await openEvidence(page)

  const rows = page.getByRole("row", { name: /Undecided\.Browser\.Movie/ })
  await expect(rows).toHaveCount(2)
  await expect(rows.nth(0)).toContainText("resolution")
  await expect(rows.nth(0)).toContainText("1080p")
  await expect(stateOf(rows.nth(0))).toHaveText("Observed")
  await expect(rows.nth(1)).toContainText("subtitle_languages")
  await expect(rows.nth(1)).toContainText("Unknown")
  await expect(stateOf(rows.nth(1))).toHaveText("Unknown")
  await expect(page.getByRole("cell", { name: "0", exact: true })).toHaveCount(0)
  await expect(page.getByText("no-name-so-no-row")).toHaveCount(0)
  expect(recorder.unexpected).toEqual([])
})
