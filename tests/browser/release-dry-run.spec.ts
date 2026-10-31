import AxeBuilder from "@axe-core/playwright"
import { expect, test, type Locator, type Page } from "@playwright/test"

import type { APIDecisionReason, APIDryRunCandidate, APIDryRunResult, APIReleaseEvidence } from "../../lib/media-manager-api"
import { installOwnerAPI, ownerFixtureIDs } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"
const observedAt = "2026-09-02T00:00:00Z"

type Recorder = { unexpected: string[]; dryRunRequests: Array<{ method: string; workflowID: string }> }

/**
 * Serves the dry-run endpoint on top of the shared local owner API. Playwright
 * consults the most recently registered route first, so everything else keeps
 * falling through to installOwnerAPI, which refuses mutations and unknown calls.
 */
async function installDryRun(page: Page, respond: (workflowID: string, attempt: number) => Promise<{ status: number; body: unknown }> | { status: number; body: unknown }): Promise<Recorder> {
  const recorder = await installOwnerAPI(page, baseURL) as Recorder
  recorder.dryRunRequests = []
  const attempts = new Map<string, number>()
  await page.route("**/api/v1/workflows/*/candidates/dry-run", async (route) => {
    const workflowID = new URL(route.request().url()).pathname.split("/")[4]
    const attempt = (attempts.get(workflowID) ?? 0) + 1
    attempts.set(workflowID, attempt)
    recorder.dryRunRequests.push({ method: route.request().method(), workflowID })
    const { status, body } = await respond(workflowID, attempt)
    await route.fulfill({ status, json: body })
  })
  return recorder
}

function reason(input: Partial<APIDecisionReason> & Pick<APIDecisionReason, "rule" | "decision" | "reason">): APIDecisionReason {
  return { score: 0, ...input }
}

function candidate(input: {
  title: string
  verdict: "eligible" | "held" | "rejected"
  score: number
  sequence: number
  attempted?: boolean
  excluded?: boolean
  selected?: boolean
  reasons?: APIDecisionReason[]
  evidence?: APIReleaseEvidence[]
}): APIDryRunCandidate {
  const id = `d${String(input.sequence).padStart(7, "0")}-0000-4000-8000-000000000000`
  const reasons = input.reasons ?? []
  return {
    candidate_id: id,
    title: input.title,
    indexer_id: ownerFixtureIDs.indexer,
    indexer_kind: "newznab",
    evaluation: { observation_id: `0b${String(input.sequence).padStart(6, "0")}-0000-4000-8000-000000000000`, verdict: input.verdict, score: input.score, explanations: reasons },
    explanation: { version: 1, verdict: input.verdict, score: input.score, reasons, evidence_scope: "dry_run_replay", evidence: input.evidence ?? [] },
    attempted: input.attempted ?? false,
    excluded: input.excluded ?? false,
    selected: input.selected ?? false,
    first_seen_sequence: input.sequence,
  }
}

const limitations = [
  "Uses only stored observations; no indexer search, NZB download, AltMount submission or Plex publication is performed.",
  "No open or discovery round snapshot applies; the profile's current policy is used for evaluation.",
  "Novelty cutoffs and the final persisted selection are not re-applied; this is a policy replay, not a new acquisition decision.",
]

function result(candidates: APIDryRunCandidate[], overrides: Partial<APIDryRunResult> = {}): APIDryRunResult {
  const ranked = candidates.map((item) => item.candidate_id)
  return {
    workflow_id: ownerFixtureIDs.wfA,
    policy_version: 3,
    profile_policy_version: 4,
    policy_source: "open_round",
    candidates,
    ranked_candidate_ids: ranked,
    limitations,
    generated_at: new Date().toISOString(),
    truncated: false,
    ...overrides,
  }
}

const topAttempted = candidate({
  title: "Dry.Run.Movie.2024.2160p.WEB-DL-TOP",
  verdict: "eligible",
  score: 150,
  sequence: 1,
  attempted: true,
  reasons: [reason({ rule: "Prefer 2160p", fact: "resolution", decision: "pass", values: ["2160p"], score: 150, reason: "observed evidence satisfies the rule", rule_kind: "preferred", category: "resolution_codec", evidence_state: "observed" })],
})
const best = candidate({
  title: "Dry.Run.Movie.2024.1080p.WEB-DL-BEST",
  verdict: "eligible",
  score: 90,
  sequence: 2,
  reasons: [
    reason({ rule: "Required resolution", fact: "resolution", decision: "pass", values: ["1080p"], reason: "observed evidence satisfies the rule", rule_kind: "required", category: "resolution_codec", evidence_state: "observed" }),
    reason({ rule: "Prefer 1080p", fact: "resolution", decision: "pass", values: ["1080p"], score: 90, reason: "observed evidence satisfies the rule", rule_kind: "preferred", category: "resolution_codec", evidence_state: "observed" }),
  ],
  evidence: [{ fact: "resolution", category: "resolution_codec", state: "observed", values: ["1080p"], observed_at: observedAt }],
})
const held = candidate({
  title: "Dry.Run.Movie.2024.HDTV-HELD",
  verdict: "held",
  score: 10,
  sequence: 3,
  reasons: [reason({ rule: "Required resolution", fact: "resolution", decision: "unknown", reason: "the provider supplied no conclusive evidence", rule_kind: "required", category: "resolution_codec", evidence_state: "unknown" })],
  evidence: [{ fact: "resolution", category: "resolution_codec", state: "unknown", values: [], observed_at: observedAt }],
})
const excluded = candidate({
  title: "Dry.Run.Movie.2024.2160p.WEB-DL-EXCLUDED",
  verdict: "rejected",
  score: 0,
  sequence: 4,
  excluded: true,
  reasons: [reason({ rule: "workflow subtitle verification", fact: "native_release", decision: "fail", values: ["guid-4"], reason: "required language was not verified", category: "exclusion" })],
})

async function openReleases(page: Page) {
  await page.goto("/intelligence/releases")
  await page.waitForLoadState("networkidle")
  await expect(page.getByText(/^Loading/)).toHaveCount(0, { timeout: 10_000 })
}

function dryRunButton(page: Page, titleFragment: string): Locator {
  return page.getByRole("row", { name: new RegExp(titleFragment) }).getByRole("button", { name: /^(Dry run|Replaying…)$/ })
}

function panelOf(page: Page): Locator {
  return page.locator("section.mm-panel").filter({ has: page.getByRole("heading", { name: "Read-only dry run" }) })
}

test("dry run replays stored candidates read-only and explains every ranked row", async ({ page }) => {
  const recorder = await installDryRun(page, () => ({ status: 200, body: { data: result([topAttempted, best, held, excluded], { best_eligible_candidate_id: best.candidate_id }) } }))
  await openReleases(page)
  await expect(panelOf(page)).toHaveCount(0)

  await dryRunButton(page, "First\\.Browser\\.Movie").click()
  const panel = panelOf(page)
  await expect(panel).toBeVisible()

  // Informational summary: skipping the attempted top-ranked release is visible.
  await expect(panel.getByText("Policy replay of stored observations. No indexer search, NZB download, AltMount submission, Plex publication or durable exclusion change.")).toBeVisible()
  await expect(panel.getByText("Best eligible", { exact: true })).toBeVisible()
  await expect(panel.getByText("Available", { exact: true }).first()).toBeVisible()
  await expect(panel.getByText("Dry.Run.Movie.2024.1080p.WEB-DL-BEST · rank 2 of 4")).toBeVisible()
  await expect(panel.getByText("Open Round policy 3 · profile 4")).toBeVisible()
  await expect(panel.getByText("Stored observations only")).toBeVisible()

  // The limitations are shown verbatim and the replay is not presented as a decision.
  const notice = panel.getByText("What this dry run does not do").locator("..")
  for (const sentence of limitations) await expect(notice).toContainText(sentence)

  // Ranked order, verdict + score chips and the per-candidate gates.
  const rows = panel.locator("tbody tr")
  await expect(rows).toHaveCount(4)
  await expect(rows.nth(0)).toContainText("Dry.Run.Movie.2024.2160p.WEB-DL-TOP")
  await expect(rows.nth(0)).toContainText("Eligible · 150")
  await expect(rows.nth(0)).toContainText("Attempted")
  await expect(rows.nth(1)).toContainText("Dry.Run.Movie.2024.1080p.WEB-DL-BEST")
  await expect(rows.nth(1)).toContainText("Eligible · 90")
  await expect(rows.nth(1)).toContainText("Available")
  await expect(rows.nth(2)).toContainText("Held · 10")
  await expect(rows.nth(3)).toContainText("Rejected · 0")
  await expect(rows.nth(3)).toContainText("Excluded")

  // Explanations: replay provenance, hard vs soft rules, scores and evidence.
  await rows.nth(1).getByText("Why this decision").click()
  await expect(rows.nth(1)).toContainText("Recomputed from the stored observation by this read-only replay")
  await expect(rows.nth(1)).toContainText("Hard requirement")
  await expect(rows.nth(1)).toContainText("Preference")
  await expect(rows.nth(1)).toContainText("+90")
  await expect(rows.nth(1)).toContainText("Stored evidence")
  await expect(rows.nth(1)).toContainText("Observed")
  await expect(rows.nth(1)).toContainText("1080p")
  await rows.nth(2).getByText("Why this decision").click()
  await expect(rows.nth(2)).toContainText("the provider supplied no conclusive evidence")
  await expect(rows.nth(2)).toContainText("Unknown")
  await rows.nth(3).getByText("Why this decision").click()
  await expect(rows.nth(3)).toContainText("required language was not verified")
  await expect(rows.nth(3)).toContainText("Exclusion")

  // Informational only: the single control in the panel is Close, and exactly
  // one read-only GET was issued.
  await expect(panel.locator("button")).toHaveCount(1)
  await expect(panel.locator("button")).toHaveText("Close")
  expect(recorder.dryRunRequests).toEqual([{ method: "GET", workflowID: ownerFixtureIDs.wfA }])
  expect(recorder.unexpected).toEqual([])

  const accessibility = await new AxeBuilder({ page }).include("section.mm-panel").withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze()
  expect(accessibility.violations.map((violation) => `${violation.id}: ${violation.nodes.length}`)).toEqual([])

  await panel.getByRole("button", { name: "Close" }).click()
  await expect(panelOf(page)).toHaveCount(0)
})

test("dry run shows a busy state, then a plain error that clears on a successful retry", async ({ page }) => {
  let release: () => void = () => undefined
  const gate = new Promise<void>((resolve) => { release = resolve })
  const recorder = await installDryRun(page, async (_workflowID, attempt) => {
    if (attempt === 1) {
      await gate
      return { status: 500, body: { error: { code: "dry_run_failed", message: "the read-only dry run could not be completed" } } }
    }
    return { status: 200, body: { data: result([best], { best_eligible_candidate_id: best.candidate_id }) } }
  })
  await openReleases(page)

  const button = dryRunButton(page, "First\\.Browser\\.Movie")
  await button.click()
  await expect(button).toHaveText("Replaying…")
  await expect(button).toBeDisabled()
  release()

  const unavailable = page.getByText("Dry run unavailable").locator("..")
  await expect(unavailable).toBeVisible()
  await expect(unavailable).toContainText("the read-only dry run could not be completed")
  await expect(panelOf(page)).toHaveCount(0)
  await expect(button).toHaveText("Dry run")
  await expect(button).toBeEnabled()

  await button.click()
  await expect(panelOf(page)).toBeVisible()
  await expect(page.getByText("Dry run unavailable")).toHaveCount(0)
  expect(recorder.dryRunRequests.map((request) => request.method)).toEqual(["GET", "GET"])
  expect(recorder.unexpected).toEqual([])
})

test("dry run reports a missing workflow without inventing a result", async ({ page }) => {
  const recorder = await installDryRun(page, () => ({ status: 404, body: { error: { code: "not_found", message: "workflow was not found" } } }))
  await openReleases(page)
  await dryRunButton(page, "First\\.Browser\\.Movie").click()
  await expect(page.getByText("Dry run unavailable").locator("..")).toContainText("workflow was not found")
  await expect(panelOf(page)).toHaveCount(0)
  expect(recorder.unexpected).toEqual([])
})

test("dry run with no stored candidates says so and proposes nothing", async ({ page }) => {
  await installDryRun(page, () => ({ status: 200, body: { data: result([]) } }))
  await openReleases(page)
  await dryRunButton(page, "First\\.Browser\\.Movie").click()
  const panel = panelOf(page)
  await expect(panel).toBeVisible()
  await expect(panel.getByText("None", { exact: true })).toBeVisible()
  await expect(panel.getByText("No stored candidate is eligible under this policy")).toBeVisible()
  await expect(panel.getByText("No stored candidates are available for this workflow.")).toBeVisible()
  await expect(panel.locator("tbody tr")).toHaveCount(0)
  await expect(panel.getByText("What this dry run does not do")).toBeVisible()
})

test("dry run caps the visible ranking, flags truncation and proposes nothing when nothing is eligible", async ({ page }) => {
  const many = Array.from({ length: 60 }, (_, index) => candidate({
    title: `Dry.Run.Movie.2024.HDTV-HELD-${String(index + 1).padStart(2, "0")}`,
    verdict: "held",
    score: 60 - index,
    sequence: index + 1,
    reasons: [reason({ rule: "Required resolution", fact: "resolution", decision: "unknown", reason: "the provider supplied no conclusive evidence", rule_kind: "required", category: "resolution_codec", evidence_state: "unknown" })],
  }))
  await installDryRun(page, () => ({ status: 200, body: { data: result(many, { truncated: true }) } }))
  await openReleases(page)
  await dryRunButton(page, "First\\.Browser\\.Movie").click()
  const panel = panelOf(page)
  await expect(panel).toBeVisible()
  await expect(panel.getByText("Replay truncated")).toBeVisible()
  await expect(panel.getByText("Only the most recently observed candidates were replayed, so the ranking may omit others.")).toBeVisible()
  await expect(panel.locator("tbody tr")).toHaveCount(50)
  await expect(panel.getByText("Showing the first 50 of 60 ranked candidates.")).toBeVisible()
  await expect(panel.getByText("None", { exact: true })).toBeVisible()
  await expect(panel.getByText("60", { exact: true })).toBeVisible()
})

test("a second dry run replaces the first result and the panel does not overflow on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await installDryRun(page, (workflowID) => workflowID === ownerFixtureIDs.wfA
    ? { status: 200, body: { data: result([best], { best_eligible_candidate_id: best.candidate_id }) } }
    : { status: 200, body: { data: result([held], { workflow_id: workflowID }) } })
  await openReleases(page)

  await dryRunButton(page, "First\\.Browser\\.Movie").click()
  await expect(panelOf(page)).toContainText("Dry.Run.Movie.2024.1080p.WEB-DL-BEST")

  await dryRunButton(page, "Attention\\.Browser\\.Movie").click()
  await expect(panelOf(page)).toContainText("Dry.Run.Movie.2024.HDTV-HELD")
  await expect(panelOf(page)).not.toContainText("Dry.Run.Movie.2024.1080p.WEB-DL-BEST")
  await expect(panelOf(page)).toHaveCount(1)

  const overflow = await page.evaluate(() => ({ viewport: window.innerWidth, document: document.documentElement.scrollWidth }))
  expect(overflow.document, "page must not scroll horizontally").toBeLessThanOrEqual(overflow.viewport)
})
