import { expect, test, type Page } from "@playwright/test"

import type { APIReleaseCandidate } from "../../lib/media-manager-api"
import { installOwnerAPI, ownerFixtureIDs } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"
const observed = "2026-09-02T00:00:00Z"

type StoredFact = { name: string; values: string[] | null; state: "observed" | "inferred" | "unknown" | "conflicting"; evidence_at?: number[]; observed_at?: string }

// The shape the Go store writes: json.Marshal of the []releases.Fact slice,
// sorted by fact name, with the fact name under `name` and not as an object key.
function candidate(index: number, verdict: string, facts: unknown): APIReleaseCandidate {
  return {
    id: `e0000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
    workflow_id: ownerFixtureIDs.wfA,
    media_id: ownerFixtureIDs.movieA,
    media_kind: "movie",
    media_title: "First Browser Movie",
    media_year: 2024,
    variant: "movie",
    title: `Grouped.Browser.Movie.${index}.1080p`,
    indexer_id: ownerFixtureIDs.indexer,
    indexer_name: "NZB Example",
    reasons: [],
    verdict,
    normalized_facts: facts,
    problems: [],
    selected: false,
    observed_at: observed,
    last_observed_at: observed,
  } as unknown as APIReleaseCandidate
}

const fact = (name: string, values: string[] | null, state: StoredFact["state"] = "observed"): StoredFact => ({ name, values, state, evidence_at: [], observed_at: observed })

async function openGroups(page: Page, candidates?: APIReleaseCandidate[]) {
  const recorder = await installOwnerAPI(page, baseURL)
  if (candidates) await page.route("**/api/v1/intelligence/releases*", (route) => route.fulfill({ status: 200, json: { data: candidates } }))
  await page.goto("/intelligence/groups")
  await page.waitForLoadState("networkidle")
  await expect(page.getByText(/^Loading/)).toHaveCount(0, { timeout: 10_000 })
  return recorder
}

function groupRow(page: Page, name: string) {
  return page.getByRole("row").filter({ has: page.getByRole("cell", { name, exact: true }) })
}

async function counts(page: Page, name: string) {
  const row = groupRow(page, name)
  await expect(row).toHaveCount(1)
  return await row.getByRole("cell").allTextContents()
}

test("release groups read the observed group from the owner fixtures' stored fact arrays", async ({ page }) => {
  const recorder = await openGroups(page)

  // Both fixture candidates name a group in their fact array. Reading the array
  // as an object would report every candidate as a single "Unknown" group.
  expect(await counts(page, "GROUP")).toEqual(["GROUP", "1", "1", "0", "0"])
  expect(await counts(page, "OTHER")).toEqual(["OTHER", "1", "0", "0", "1"])
  await expect(groupRow(page, "Unknown")).toHaveCount(0)
  expect(recorder.unexpected).toEqual([])
})

test("release groups count verdicts per observed group and keep missing evidence out of every group", async ({ page }) => {
  const recorder = await openGroups(page, [
    candidate(1, "eligible", [fact("group", ["ALPHA"]), fact("resolution", ["1080p"])]),
    candidate(2, "rejected", [fact("group", ["ALPHA"])]),
    candidate(3, "held", [fact("group", ["ALPHA"], "inferred")]),
    candidate(4, "eligible", [fact("group", ["BRAVO"]), fact("source", ["webdl"])]),
    // release_group outranks group when both are present.
    candidate(5, "eligible", [fact("group", ["usenet.group"]), fact("release_group", ["CHARLIE"])]),
    // Unknown, empty, null, absent and non-array evidence is never a group.
    candidate(6, "held", [fact("group", [], "unknown")]),
    candidate(7, "rejected", [fact("group", null, "unknown")]),
    candidate(8, "held", [fact("resolution", ["720p"])]),
    candidate(9, "held", []),
    candidate(10, "held", {}),
    // A conflicting fact, or several distinct values, is not attributed to one of them.
    candidate(11, "held", [fact("group", ["ALPHA", "BRAVO"], "conflicting")]),
    candidate(12, "eligible", [fact("group", ["DELTA", "ECHO"])]),
  ])

  expect(await counts(page, "ALPHA")).toEqual(["ALPHA", "3", "1", "1", "1"])
  expect(await counts(page, "BRAVO")).toEqual(["BRAVO", "1", "1", "0", "0"])
  expect(await counts(page, "CHARLIE")).toEqual(["CHARLIE", "1", "1", "0", "0"])
  expect(await counts(page, "Conflicting evidence")).toEqual(["Conflicting evidence", "2", "1", "1", "0"])
  expect(await counts(page, "Unknown")).toEqual(["Unknown", "5", "0", "4", "1"])
  await expect(groupRow(page, "DELTA")).toHaveCount(0)
  await expect(groupRow(page, "usenet.group")).toHaveCount(0)

  // Real groups come first; the evidence buckets never lead the table.
  const names = await page.getByRole("row").evaluateAll((rows) => rows.slice(1).map((row) => row.querySelector("td")?.textContent))
  expect(names).toEqual(["ALPHA", "BRAVO", "CHARLIE", "Conflicting evidence", "Unknown"])
  expect(recorder.unexpected).toEqual([])
})

test("release groups show an empty state when there are no observations", async ({ page }) => {
  const recorder = await openGroups(page, [])
  await expect(page.getByRole("row")).toHaveCount(1)
  expect(recorder.unexpected).toEqual([])
})
