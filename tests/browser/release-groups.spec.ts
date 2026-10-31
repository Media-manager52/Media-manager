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

// Each table lives in its own panel, found by its heading.
function table(page: Page, heading: string) {
  return page.locator("section").filter({ has: page.getByRole("heading", { name: heading, exact: true }) })
}

function groupRow(page: Page, heading: string, name: string) {
  return table(page, heading).getByRole("row").filter({ has: page.getByRole("cell", { name, exact: true }) })
}

async function counts(page: Page, heading: string, name: string) {
  const row = groupRow(page, heading, name)
  await expect(row).toHaveCount(1)
  return await row.getByRole("cell").allTextContents()
}

async function names(page: Page, heading: string) {
  return await table(page, heading).getByRole("row").evaluateAll((rows) => rows.slice(1).map((row) => row.querySelector("td")?.textContent))
}

const releaseGroups = "Observed release groups"
const newsgroups = "Usenet newsgroups"

// Newznab's `group` attribute is "NNTP Group(s)": the Usenet newsgroup(s) a release
// was posted to. It is not the team that made the release, so it must never be
// listed as a release group, whatever its value looks like.
test("the provider's group fact appears as a Usenet newsgroup and never as a release group", async ({ page }) => {
  const recorder = await openGroups(page)

  // The owner fixtures carry only the Newznab `group` fact: no release group is recorded.
  expect(await names(page, releaseGroups)).toEqual(["Unknown"])
  expect(await counts(page, releaseGroups, "Unknown")).toEqual(["Unknown", "2", "1", "0", "1"])
  for (const newsgroup of ["alt.binaries.movies", "alt.binaries.hdtv"]) await expect(groupRow(page, releaseGroups, newsgroup)).toHaveCount(0)

  // The same facts are shown, correctly named, as newsgroups. One fixture release is
  // cross-posted to two newsgroups and is counted under each.
  expect(await names(page, newsgroups)).toEqual(["alt.binaries.movies", "alt.binaries.hdtv"])
  expect(await counts(page, newsgroups, "alt.binaries.movies")).toEqual(["alt.binaries.movies", "2", "1", "0", "1"])
  expect(await counts(page, newsgroups, "alt.binaries.hdtv")).toEqual(["alt.binaries.hdtv", "1", "1", "0", "0"])
  await expect(table(page, newsgroups)).toContainText("A newsgroup is not a release group")
  expect(recorder.unexpected).toEqual([])
})

test("release groups count verdicts per recorded release group and keep missing evidence out of every group", async ({ page }) => {
  const recorder = await openGroups(page, [
    candidate(1, "eligible", [fact("release_group", ["ALPHA"]), fact("resolution", ["1080p"])]),
    candidate(2, "rejected", [fact("release_group", ["ALPHA"])]),
    candidate(3, "held", [fact("release_group", ["ALPHA"], "inferred")]),
    candidate(4, "eligible", [fact("group", ["alt.binaries.hdtv"]), fact("release_group", ["BRAVO"])]),
    // A newsgroup alone is not a release group, however group-like its value is.
    candidate(5, "eligible", [fact("group", ["CHARLIE"])]),
    candidate(6, "held", [fact("group", ["alt.binaries.teevee"])]),
    // Unknown, empty, null, absent and non-array evidence is never a group.
    candidate(7, "held", [fact("release_group", [], "unknown")]),
    candidate(8, "rejected", [fact("release_group", null, "unknown")]),
    candidate(9, "held", [fact("resolution", ["720p"])]),
    candidate(10, "held", []),
    candidate(11, "held", {}),
    // A conflicting fact, or several distinct values, is not attributed to one of them.
    candidate(12, "held", [fact("release_group", ["ALPHA", "BRAVO"], "conflicting")]),
    candidate(13, "eligible", [fact("release_group", ["DELTA", "ECHO"])]),
  ])

  expect(await counts(page, releaseGroups, "ALPHA")).toEqual(["ALPHA", "3", "1", "1", "1"])
  expect(await counts(page, releaseGroups, "BRAVO")).toEqual(["BRAVO", "1", "1", "0", "0"])
  expect(await counts(page, releaseGroups, "Conflicting evidence")).toEqual(["Conflicting evidence", "2", "1", "1", "0"])
  expect(await counts(page, releaseGroups, "Unknown")).toEqual(["Unknown", "7", "1", "5", "1"])
  for (const notAGroup of ["CHARLIE", "DELTA", "alt.binaries.hdtv", "alt.binaries.teevee"]) await expect(groupRow(page, releaseGroups, notAGroup)).toHaveCount(0)
  // Real groups come first; the evidence buckets never lead the table.
  expect(await names(page, releaseGroups)).toEqual(["ALPHA", "BRAVO", "Conflicting evidence", "Unknown"])
  expect(recorder.unexpected).toEqual([])
})

test("newsgroups are split on commas, counted once per newsgroup and kept apart from release groups", async ({ page }) => {
  const recorder = await openGroups(page, [
    candidate(1, "eligible", [fact("group", ["a.b.group, a.b.teevee"]), fact("release_group", ["ALPHA"])]),
    candidate(2, "rejected", [fact("group", ["a.b.teevee"])]),
    candidate(3, "held", [fact("group", ["A.B.TEEVEE , a.b.other"], "inferred")]),
    candidate(4, "held", [fact("release_group", ["BRAVO"])]),
    candidate(5, "held", [fact("group", [], "unknown")]),
    candidate(6, "eligible", [fact("group", ["a.b.x", "a.b.y"], "conflicting")]),
  ])

  expect(await names(page, newsgroups)).toEqual(["a.b.teevee", "a.b.group", "a.b.other", "Conflicting evidence", "Unknown"])
  expect(await counts(page, newsgroups, "a.b.teevee")).toEqual(["a.b.teevee", "3", "1", "1", "1"])
  expect(await counts(page, newsgroups, "a.b.group")).toEqual(["a.b.group", "1", "1", "0", "0"])
  expect(await counts(page, newsgroups, "a.b.other")).toEqual(["a.b.other", "1", "0", "1", "0"])
  expect(await counts(page, newsgroups, "Conflicting evidence")).toEqual(["Conflicting evidence", "1", "1", "0", "0"])
  expect(await counts(page, newsgroups, "Unknown")).toEqual(["Unknown", "2", "0", "2", "0"])
  // A release group is never listed as a newsgroup, nor the other way round.
  for (const group of ["ALPHA", "BRAVO"]) await expect(groupRow(page, newsgroups, group)).toHaveCount(0)
  for (const group of ["a.b.teevee", "a.b.group", "a.b.other"]) await expect(groupRow(page, releaseGroups, group)).toHaveCount(0)
  expect(await names(page, releaseGroups)).toEqual(["ALPHA", "BRAVO", "Unknown"])
  expect(recorder.unexpected).toEqual([])
})

test("release groups and newsgroups show an empty state when there are no observations", async ({ page }) => {
  const recorder = await openGroups(page, [])
  await expect(table(page, releaseGroups).getByRole("row")).toHaveCount(1)
  await expect(table(page, newsgroups).getByRole("row")).toHaveCount(1)
  await expect(page.getByText("No release-group evidence has been observed.")).toBeVisible()
  await expect(page.getByText("No newsgroup evidence has been observed.")).toBeVisible()
  expect(recorder.unexpected).toEqual([])
})
