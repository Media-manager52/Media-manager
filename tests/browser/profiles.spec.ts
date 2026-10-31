import { readFileSync } from "node:fs"
import { expect, test, type Page } from "@playwright/test"
import { qualityProfilePresets } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"

type ProfileFixture = {
  id: string
  media_kind: "movie" | "series"
  name: string
  policy_version: number
  policy: Record<string, unknown>
  upgrades_enabled: boolean
  is_default: boolean
  enabled: boolean
}

test("owner can persist and reload canonical original-language routing", async ({ page }) => {
  const unexpected: string[] = []
  let profiles: ProfileFixture[] = [{
    id: "11111111-1111-4111-8111-111111111111",
    media_kind: "movie",
    name: "Movie 1080p Danish Original",
    policy_version: 1,
    policy: {
      rules: [
        { name: "Required resolution", fact: "resolution", allowed_values: ["1080p"], kind: "required", weight: 0, unknown: "hold" },
        { name: "Preferred source", fact: "source", allowed_values: ["webdl"], kind: "preferred", weight: 100, unknown: "allow" },
      ],
      original_language_routing: ["da"],
      required_subtitle_languages: [],
      preferred_subtitle_languages: [],
    },
    upgrades_enabled: true,
    is_default: false,
    enabled: true,
  }]
  let saved: Record<string, unknown> | undefined

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
    if (request.method() === "GET" && url.pathname === "/api/v1/settings/quality-profiles") {
      await route.fulfill({ status: 200, json: { data: profiles } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/acquisition-verifications" && url.search === "?limit=100") {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    // The profiles screen lists the soft quality presets alongside the profiles.
    if (request.method() === "GET" && url.pathname === "/api/v1/settings/quality-profile-presets") {
      await route.fulfill({ status: 200, json: { data: qualityProfilePresets } })
      return
    }
    // The shell loads owner attention on every page.
    if (request.method() === "GET" && (url.pathname === "/api/v1/wanted" || url.pathname === "/api/v1/workflows")) {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/info") {
      await route.fulfill({ status: 200, json: { data: { version: "browser-test" } } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/health") {
      await route.fulfill({ status: 200, json: { data: { database_state: "ready", schema_version: 30, expected_schema: 30 } } })
      return
    }
    if (request.method() === "PATCH" && url.pathname === `/api/v1/settings/quality-profiles/${profiles[0].id}`) {
      saved = request.postDataJSON() as Record<string, unknown>
      const policy = saved.policy as Record<string, unknown>
      profiles = [{ ...profiles[0], ...saved, policy: { ...policy, original_language_routing: ["da"] }, policy_version: 2 }]
      await route.fulfill({ status: 200, json: { data: profiles[0] } })
      return
    }
    unexpected.push(`${request.method()} ${url.pathname}${url.search}`)
    await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
  })

  await page.goto("/settings/profiles")
  await page.getByRole("button", { name: "Edit" }).click()
  await expect(page.getByLabel("Original-language routing")).toHaveValue("da")
  await expect(page.getByLabel("Required subtitle languages")).toHaveValue("")

  await page.getByLabel("Original-language routing").fill("Danish")
  await page.getByRole("button", { name: "Update profile" }).click()

  expect(saved).toBeDefined()
  expect(saved?.policy_version).toBe(1)
  expect(saved?.is_default).toBe(false)
  expect((saved?.policy as Record<string, unknown>).original_language_routing).toEqual(["Danish"])
  await page.getByRole("button", { name: "Edit" }).click()
  await expect(page.getByLabel("Original-language routing")).toHaveValue("da")
  expect(unexpected).toEqual([])
})

test("owner can replace a hard resolution gate with persisted best-available quality", async ({ page }) => {
  const unexpected: string[] = []
  let profiles: ProfileFixture[] = [{
    id: "66666666-6666-4666-8666-666666666666",
    media_kind: "series",
    name: "TV Danish Subs",
    policy_version: 1,
    policy: {
      rules: [
        { name: "Required resolution", fact: "resolution", allowed_values: ["2160p"], kind: "required", weight: 0, unknown: "hold" },
        { name: "Preferred source", fact: "source", allowed_values: ["remux"], kind: "preferred", weight: 100, unknown: "allow" },
      ],
      original_language_routing: [],
      required_subtitle_languages: ["da"],
      preferred_subtitle_languages: [],
    },
    upgrades_enabled: true,
    is_default: true,
    enabled: true,
  }]
  let saved: Record<string, unknown> | undefined

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
    if (request.method() === "GET" && url.pathname === "/api/v1/settings/quality-profiles") {
      await route.fulfill({ status: 200, json: { data: profiles } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/acquisition-verifications" && url.search === "?limit=100") {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    // The profiles screen lists the soft quality presets alongside the profiles.
    if (request.method() === "GET" && url.pathname === "/api/v1/settings/quality-profile-presets") {
      await route.fulfill({ status: 200, json: { data: qualityProfilePresets } })
      return
    }
    // The shell loads owner attention on every page.
    if (request.method() === "GET" && (url.pathname === "/api/v1/wanted" || url.pathname === "/api/v1/workflows")) {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/info") {
      await route.fulfill({ status: 200, json: { data: { version: "browser-test" } } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/health") {
      await route.fulfill({ status: 200, json: { data: { database_state: "ready", schema_version: 30, expected_schema: 30 } } })
      return
    }
    if (request.method() === "PATCH" && url.pathname === `/api/v1/settings/quality-profiles/${profiles[0].id}`) {
      saved = request.postDataJSON() as Record<string, unknown>
      profiles = [{ ...profiles[0], ...saved, policy_version: 2 } as ProfileFixture]
      await route.fulfill({ status: 200, json: { data: profiles[0] } })
      return
    }
    unexpected.push(`${request.method()} ${url.pathname}${url.search}`)
    await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
  })

  await page.goto("/settings/profiles")
  await page.getByRole("button", { name: "Edit" }).click()
  // The hard gate is shown as such; removing it is a separate, explicit choice.
  await expect(page.getByLabel("Required resolution")).toHaveText("Require 2160p")
  await page.getByLabel("Required resolution").click()
  await page.getByRole("option", { name: "No requirement", exact: true }).click()
  await page.getByLabel("Resolution preference").click()
  await page.getByRole("option", { name: "Best available", exact: true }).click()
  await page.getByLabel("Audio preference").click()
  await page.getByRole("option", { name: "Best available", exact: true }).click()
  await page.getByRole("button", { name: "Update profile" }).click()

  expect(saved).toBeDefined()
  expect(saved?.policy_version).toBe(1)
  const policy = saved?.policy as Record<string, unknown>
  const rules = policy.rules as Array<Record<string, unknown>>
  expect(rules.some((rule) => rule.fact === "resolution" && rule.kind === "required")).toBe(false)
  expect(rules.filter((rule) => rule.fact === "resolution" && rule.kind === "preferred").map((rule) => [rule.allowed_values, rule.weight])).toEqual([
    [["2160p"], 50000], [["1080p"], 40000], [["720p"], 30000], [["576p"], 20000], [["480p"], 10000],
  ])
  expect(rules.filter((rule) => rule.fact === "audio_codec" && rule.kind === "preferred")).toHaveLength(14)
  expect(policy.required_subtitle_languages).toEqual(["da"])
  await page.getByRole("button", { name: "Edit" }).click()
  await expect(page.getByLabel("Required resolution")).toHaveText("No requirement")
  await expect(page.getByLabel("Resolution preference")).toHaveText("Best available")
  await expect(page.getByLabel("Audio preference")).toHaveText("Best available")
  expect(unexpected).toEqual([])
})

test("owner can queue one exact retained-output reverification", async ({ page }) => {
  const unexpected: string[] = []
  const workflowID = "22222222-2222-4222-8222-222222222222"
  const jobID = "33333333-3333-4333-8333-333333333333"
  let canReverify = true
  let submitted: Record<string, unknown> | undefined

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
    if (request.method() === "GET" && url.pathname === "/api/v1/settings/quality-profiles") {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/acquisition-verifications" && url.search === "?limit=100") {
      await route.fulfill({ status: 200, json: { data: [{
        id: "44444444-4444-4444-8444-444444444444",
        workflow_id: workflowID,
        altmount_job_id: jobID,
        candidate_id: "55555555-5555-4555-8555-555555555555",
        media_title: "Retained Season",
        release_title: "Retained.Season.S02.2160p.WEB-DL",
        attempt_generation: 2,
        status: "unverified",
        required_languages: ["da"],
        preferred_languages: [],
        observed_languages: [],
        evidence: {},
        failure_reasons: ["bounded media-info inspection failed"],
        retry_state: "exhausted",
        can_reverify: canReverify,
        created_at: "2026-09-01T20:00:00Z",
      }] } })
      return
    }
    if (request.method() === "POST" && url.pathname === `/api/v1/workflows/${workflowID}/reverify-retained-output`) {
      submitted = request.postDataJSON() as Record<string, unknown>
      canReverify = false
      await route.fulfill({ status: 202, json: { data: { status: "reverification_queued", workflow_id: workflowID, job_id: jobID } } })
      return
    }
    // The profiles screen lists the soft quality presets alongside the profiles.
    if (request.method() === "GET" && url.pathname === "/api/v1/settings/quality-profile-presets") {
      await route.fulfill({ status: 200, json: { data: qualityProfilePresets } })
      return
    }
    // The shell loads owner attention on every page.
    if (request.method() === "GET" && (url.pathname === "/api/v1/wanted" || url.pathname === "/api/v1/workflows")) {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/info") {
      await route.fulfill({ status: 200, json: { data: { version: "browser-test" } } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/health") {
      await route.fulfill({ status: 200, json: { data: { database_state: "ready", schema_version: 30, expected_schema: 30 } } })
      return
    }
    unexpected.push(`${request.method()} ${url.pathname}${url.search}`)
    await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
  })

  page.on("dialog", (dialog) => void dialog.accept())
  await page.goto("/settings/profiles")
  await page.getByRole("button", { name: "Reverify output" }).click()

  await expect.poll(() => submitted).toEqual({ job_id: jobID })
  await expect(page.getByRole("button", { name: "Reverify output" })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test("owner applies a soft quality preset against the reloaded policy version", async ({ page }) => {
  const unexpected: string[] = []
  const profileID = "11111111-1111-4111-8111-111111111111"
  let profile: ProfileFixture = {
    id: profileID,
    media_kind: "movie",
    name: "Movie 1080p Danish",
    policy_version: 3,
    policy: {
      rules: [{ name: "Required resolution", fact: "resolution", allowed_values: ["1080p"], kind: "required", weight: 0, unknown: "hold" }],
      required_subtitle_languages: ["da"],
      preferred_subtitle_languages: [],
    },
    upgrades_enabled: true,
    is_default: true,
    enabled: true,
  }
  const submitted: Array<Record<string, unknown>> = []
  const confirmations: string[] = []
  page.on("dialog", async (dialog) => {
    confirmations.push(dialog.message())
    await dialog.accept()
  })

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
    if (request.method() === "GET" && url.pathname === "/api/v1/settings/quality-profiles") {
      await route.fulfill({ status: 200, json: { data: [profile] } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/settings/quality-profile-presets") {
      await route.fulfill({ status: 200, json: { data: qualityProfilePresets } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/acquisition-verifications" && url.search === "?limit=100") {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    // The shell loads owner attention on every page.
    if (request.method() === "GET" && (url.pathname === "/api/v1/wanted" || url.pathname === "/api/v1/workflows")) {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/info") {
      await route.fulfill({ status: 200, json: { data: { version: "browser-test" } } })
      return
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/system/health") {
      await route.fulfill({ status: 200, json: { data: { database_state: "ready", schema_version: 30, expected_schema: 30 } } })
      return
    }
    if (request.method() === "POST" && url.pathname === `/api/v1/settings/quality-profiles/${profileID}/preset`) {
      submitted.push(request.postDataJSON() as Record<string, unknown>)
      // The server replaces only soft rules and bumps the policy version; the
      // required resolution rule and the Danish requirement are untouched.
      profile = { ...profile, policy_version: profile.policy_version + 1 }
      await route.fulfill({ status: 200, json: { data: profile } })
      return
    }
    unexpected.push(`${request.method()} ${url.pathname}${url.search}`)
    await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
  })

  await page.goto("/settings/profiles")
  const choose = async (presetName: string) => {
    await page.getByLabel("Soft preset for Movie 1080p Danish").click()
    await page.getByRole("option", { name: presetName }).click()
    await page.getByRole("button", { name: "Apply", exact: true }).click()
  }

  await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled()
  await choose("Storage efficient")
  await expect.poll(() => submitted.length).toBe(1)
  expect(submitted[0]).toEqual({ preset: "storage_efficient", policy_version: 3 })
  expect(confirmations[0]).toContain("Storage efficient")
  expect(confirmations[0]).toContain("Required rules, language requirements and unrelated preferences stay unchanged")
  expect(confirmations[0]).toContain("Existing acquisition rounds keep their current policy")

  // The screen must reload the bumped version instead of reusing the stale one.
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled()
  await choose("Compatibility first")
  await expect.poll(() => submitted.length).toBe(2)
  expect(submitted[1]).toEqual({ preset: "compatibility_first", policy_version: 4 })
  expect(unexpected).toEqual([])
})

type Rule = Record<string, unknown>
const presetFixture = JSON.parse(readFileSync(new URL("./fixtures/quality-preset-applications.json", import.meta.url), "utf8")) as { base_rules: Rule[]; applied_rules: Record<string, Rule[]> }
const profileID = "77777777-7777-4777-8777-777777777777"
const presetLabels: Record<string, { name: string; resolutionPreference: string; audioPreference: string; resolutionCell: RegExp }> = {
  balanced: { name: "Balanced", resolutionPreference: "Best available", audioPreference: "Best available", resolutionCell: /Require 1080p, prefer best available/ },
  maximum_quality: { name: "Maximum quality", resolutionPreference: "Keep custom / none", audioPreference: "Keep custom / none", resolutionCell: /Require 1080p/ },
  storage_efficient: { name: "Storage efficient", resolutionPreference: "Keep custom / none", audioPreference: "Keep custom / none", resolutionCell: /Require 1080p/ },
  compatibility_first: { name: "Compatibility first", resolutionPreference: "Keep custom / none", audioPreference: "Keep custom / none", resolutionCell: /Require 1080p/ },
}

/**
 * A stateful profile API that applies the real Go ApplyPreset output (recorded
 * in the shared fixture) the way the server does: it checks the policy_version
 * of the request, replaces the rules, and bumps the version.
 */
async function installProfileAPI(page: Page, rules: Rule[], options: { presetStatus?: number } = {}) {
  const unexpected: string[] = []
  const patches: Array<Record<string, unknown>> = []
  // Every PATCH the page sent, including those the server refused.
  const attempts: Array<Record<string, unknown>> = []
  const presetRequests: Array<Record<string, unknown>> = []
  let profile: ProfileFixture = {
    id: profileID,
    media_kind: "movie",
    name: "Movie 1080p Danish",
    policy_version: 3,
    policy: { rules, required_subtitle_languages: ["da"], preferred_subtitle_languages: ["da", "en"], original_language_routing: [] },
    upgrades_enabled: true,
    is_default: true,
    enabled: true,
  }
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
    const get = request.method() === "GET"
    if (get && url.pathname === "/api/v1/settings/quality-profiles") return await route.fulfill({ status: 200, json: { data: [profile] } })
    if (get && url.pathname === "/api/v1/settings/quality-profile-presets") return await route.fulfill({ status: 200, json: { data: qualityProfilePresets } })
    if (get && url.pathname === "/api/v1/acquisition-verifications") return await route.fulfill({ status: 200, json: { data: [] } })
    if (get && (url.pathname === "/api/v1/wanted" || url.pathname === "/api/v1/workflows")) return await route.fulfill({ status: 200, json: { data: [] } })
    if (get && url.pathname === "/api/v1/system/info") return await route.fulfill({ status: 200, json: { data: { version: "browser-test" } } })
    if (get && url.pathname === "/api/v1/system/health") return await route.fulfill({ status: 200, json: { data: { database_state: "ready", schema_version: 30, expected_schema: 30 } } })
    if (request.method() === "POST" && url.pathname === `/api/v1/settings/quality-profiles/${profileID}/preset`) {
      const body = request.postDataJSON() as { preset: string; policy_version: number }
      presetRequests.push(body)
      if (options.presetStatus === 409 || body.policy_version !== profile.policy_version) {
        return await route.fulfill({ status: 409, json: { error: { code: "quality_profile_changed", message: "the quality profile changed; reload it and retry" } } })
      }
      profile = { ...profile, policy_version: profile.policy_version + 1, policy: { ...profile.policy, rules: presetFixture.applied_rules[body.preset] } }
      return await route.fulfill({ status: 200, json: { data: profile } })
    }
    if (request.method() === "PATCH" && url.pathname === `/api/v1/settings/quality-profiles/${profileID}`) {
      // The server contract: policy_version is the version the editor read. It is
      // required, a stale one is a 409, and a refused edit writes nothing.
      const body = request.postDataJSON() as Record<string, unknown>
      attempts.push(body)
      if (typeof body.policy_version !== "number" || body.policy_version < 1) {
        return await route.fulfill({ status: 400, json: { error: { code: "invalid_request", message: "current policy_version is required" } } })
      }
      if (body.policy_version !== profile.policy_version) {
        return await route.fulfill({ status: 409, json: { error: { code: "quality_profile_changed", message: "the quality profile changed; reload it and retry" } } })
      }
      patches.push(body)
      profile = { ...profile, ...body, policy_version: profile.policy_version + 1 } as ProfileFixture
      return await route.fulfill({ status: 200, json: { data: profile } })
    }
    unexpected.push(`${request.method()} ${url.pathname}${url.search}`)
    await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
  })
  return { unexpected, patches, attempts, presetRequests, current: () => profile, setProfile: (next: ProfileFixture) => { profile = next } }
}

async function applyPresetFromTable(page: Page, preset: string) {
  await page.getByLabel("Soft preset for Movie 1080p Danish").click()
  await page.getByRole("option", { name: presetLabels[preset].name, exact: true }).click()
  await page.getByRole("button", { name: "Apply", exact: true }).click()
}

for (const preset of Object.keys(presetLabels)) {
  test(`${preset}: editing an unrelated field after applying the preset keeps the required resolution and audio rules`, async ({ page }) => {
    const api = await installProfileAPI(page, presetFixture.base_rules)
    page.on("dialog", (dialog) => void dialog.accept())
    await page.goto("/settings/profiles")

    await applyPresetFromTable(page, preset)
    await expect.poll(() => api.presetRequests.length).toBe(1)
    expect(api.presetRequests[0]).toEqual({ preset, policy_version: 3 })
    await expect(page.getByRole("row", { name: /Movie 1080p Danish/ }).getByRole("cell").nth(2)).toHaveText(presetLabels[preset].resolutionCell)

    await page.getByRole("button", { name: "Edit" }).click()
    // The editor shows the effective profile: the hard gates and the preset's preferences.
    await expect(page.getByLabel("Required resolution")).toHaveText("Require 1080p")
    await expect(page.getByLabel("Required audio codec")).toHaveText("Keep current (eac3 / ac3)")
    await expect(page.getByLabel("Resolution preference")).toHaveText(presetLabels[preset].resolutionPreference)
    await expect(page.getByLabel("Audio preference")).toHaveText(presetLabels[preset].audioPreference)

    await page.getByLabel("Name", { exact: true }).fill("Movie 1080p Danish (renamed)")
    await page.getByRole("button", { name: "Update profile" }).click()

    await expect.poll(() => api.patches.length).toBe(1)
    const saved = api.patches[0]
    const policy = saved.policy as Record<string, unknown>
    // Every rule the preset left behind survives, unchanged and in order.
    expect(policy.rules).toEqual(presetFixture.applied_rules[preset])
    expect(saved.policy_version).toBe(4)
    expect(saved.name).toBe("Movie 1080p Danish (renamed)")
    expect(policy.required_subtitle_languages).toEqual(["da"])
    expect(policy.preferred_subtitle_languages).toEqual(["da", "en"])

    // And it round-trips: the reopened editor still shows the same effective profile.
    await page.getByRole("button", { name: "Edit" }).click()
    await expect(page.getByLabel("Required resolution")).toHaveText("Require 1080p")
    await expect(page.getByLabel("Required audio codec")).toHaveText("Keep current (eac3 / ac3)")
    await expect(page.getByLabel("Resolution preference")).toHaveText(presetLabels[preset].resolutionPreference)
    expect(api.unexpected).toEqual([])
  })
}

test("the owner can still remove the required resolution and audio codec explicitly", async ({ page }) => {
  const api = await installProfileAPI(page, presetFixture.applied_rules.balanced)
  await page.goto("/settings/profiles")
  await page.getByRole("button", { name: "Edit" }).click()

  await page.getByLabel("Required resolution").click()
  await page.getByRole("option", { name: "No requirement", exact: true }).click()
  await page.getByLabel("Required audio codec").click()
  await page.getByRole("option", { name: "Remove requirement", exact: true }).click()
  await page.getByRole("button", { name: "Update profile" }).click()

  await expect.poll(() => api.patches.length).toBe(1)
  const rules = (api.patches[0].policy as Record<string, unknown>).rules as Rule[]
  // Only the two gates are gone; the preset ladders and the Danish rule remain.
  expect(rules).toEqual(presetFixture.applied_rules.balanced.filter((rule) => !((rule.fact === "resolution" || rule.fact === "audio_codec") && rule.kind === "required")))
  expect(rules.some((rule) => rule.fact === "subtitle_or_audio_languages" && rule.kind === "required")).toBe(true)
  await page.getByRole("button", { name: "Edit" }).click()
  await expect(page.getByLabel("Required resolution")).toHaveText("No requirement")
  await expect(page.getByLabel("Resolution preference")).toHaveText("Best available")
  await expect(page.getByLabel("Required audio codec")).toHaveCount(0)
  expect(api.unexpected).toEqual([])
})

test("changing the required resolution keeps the preset ranking", async ({ page }) => {
  const api = await installProfileAPI(page, presetFixture.applied_rules.maximum_quality)
  await page.goto("/settings/profiles")
  await page.getByRole("button", { name: "Edit" }).click()
  await page.getByLabel("Required resolution").click()
  await page.getByRole("option", { name: "Require 2160p", exact: true }).click()
  await page.getByRole("button", { name: "Update profile" }).click()

  await expect.poll(() => api.patches.length).toBe(1)
  const rules = (api.patches[0].policy as Record<string, unknown>).rules as Rule[]
  expect(rules.filter((rule) => rule.fact === "resolution" && rule.kind === "required").map((rule) => rule.allowed_values)).toEqual([["2160p"]])
  expect(rules.filter((rule) => rule.fact === "resolution" && rule.kind === "preferred")).toEqual(presetFixture.applied_rules.maximum_quality.filter((rule) => rule.fact === "resolution" && rule.kind === "preferred"))
  expect(rules.filter((rule) => rule.fact === "audio_codec" && rule.kind === "required")).toEqual(presetFixture.base_rules.filter((rule) => rule.fact === "audio_codec" && rule.kind === "required"))
  expect(api.unexpected).toEqual([])
})

test("a custom required resolution is kept as it is, not narrowed to its first value", async ({ page }) => {
  const custom: Rule[] = [
    { name: "Minimum 1080p", fact: "resolution", allowed_values: ["1080p", "2160p"], kind: "required", weight: 0, unknown: "reject" },
    { name: "Preferred source", fact: "source", allowed_values: ["webdl", "bluray"], kind: "preferred", weight: 100, unknown: "allow" },
  ]
  const api = await installProfileAPI(page, custom)
  await page.goto("/settings/profiles")
  await page.getByRole("button", { name: "Edit" }).click()
  await expect(page.getByLabel("Required resolution")).toHaveText("Keep current (1080p / 2160p)")
  await expect(page.getByLabel("Preferred source")).toHaveText("Keep custom / none")
  await page.getByLabel("Name", { exact: true }).fill("Renamed only")
  await page.getByRole("button", { name: "Update profile" }).click()

  await expect.poll(() => api.patches.length).toBe(1)
  expect((api.patches[0].policy as Record<string, unknown>).rules).toEqual(custom)
  expect(api.unexpected).toEqual([])
})

test("an edit opened before a preset was applied is refused instead of overwriting the preset", async ({ page }) => {
  const api = await installProfileAPI(page, presetFixture.base_rules)
  page.on("dialog", (dialog) => void dialog.accept())
  await page.goto("/settings/profiles")
  await page.getByRole("button", { name: "Edit" }).click()
  await page.getByLabel("Name", { exact: true }).fill("Edited against version 3")

  await applyPresetFromTable(page, "balanced")
  await expect.poll(() => api.presetRequests.length).toBe(1)

  // The open form was built from version 3; the profile is now version 4.
  await expect(page.getByText("Profile changed", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Update profile" })).toBeDisabled()
  expect(api.patches).toEqual([])

  // Reopening loads the current policy, including the preset, and saving works.
  await page.getByRole("button", { name: "Cancel" }).click()
  await page.getByRole("button", { name: "Edit" }).click()
  await expect(page.getByLabel("Resolution preference")).toHaveText("Best available")
  await page.getByRole("button", { name: "Update profile" }).click()
  await expect.poll(() => api.patches.length).toBe(1)
  expect(api.patches[0].policy_version).toBe(4)
  expect((api.patches[0].policy as Record<string, unknown>).rules).toEqual(presetFixture.applied_rules.balanced)
  expect(api.unexpected).toEqual([])
})

test("a preset applied against a stale policy version is refused and changes nothing", async ({ page }) => {
  const api = await installProfileAPI(page, presetFixture.base_rules, { presetStatus: 409 })
  page.on("dialog", (dialog) => void dialog.accept())
  await page.goto("/settings/profiles")
  await applyPresetFromTable(page, "storage_efficient")

  await expect(page.getByText("the quality profile changed; reload it and retry")).toBeVisible()
  expect(api.presetRequests).toEqual([{ preset: "storage_efficient", policy_version: 3 }])
  expect(api.current().policy_version).toBe(3)
  expect((api.current().policy as Record<string, unknown>).rules).toEqual(presetFixture.base_rules)
  expect(api.patches).toEqual([])
  expect(api.unexpected).toEqual([])
})

test("a save the server refuses as stale shows the conflict, refreshes the form and keeps the newer required rules", async ({ page }) => {
  const api = await installProfileAPI(page, presetFixture.base_rules)
  await page.goto("/settings/profiles")
  await page.getByRole("button", { name: "Edit" }).click()
  await page.getByLabel("Name", { exact: true }).fill("Edited against version 3")

  // Another session applies a preset after this page loaded; this page cannot know.
  api.setProfile({ ...api.current(), policy_version: 4, policy: { ...api.current().policy, rules: presetFixture.applied_rules.balanced } })
  await page.getByRole("button", { name: "Update profile" }).click()

  // The page sent the version it opened the form on, and the server refused it.
  await expect.poll(() => api.attempts.length).toBe(1)
  expect(api.attempts[0].policy_version).toBe(3)
  await expect(page.getByText("the quality profile changed; reload it and retry")).toBeVisible()
  // The page re-read the profile: the open form is marked stale and cannot be saved again.
  await expect(page.getByText("Profile changed", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Update profile" })).toBeDisabled()
  await expect(page.getByRole("row", { name: /Movie 1080p Danish/ }).getByRole("cell").nth(2)).toHaveText(/Require 1080p, prefer best available/)
  // Nothing was written: the newer policy, with its required rules, is intact.
  expect(api.patches).toEqual([])
  expect(api.current().policy_version).toBe(4)
  expect((api.current().policy as Record<string, unknown>).rules).toEqual(presetFixture.applied_rules.balanced)

  // Reopening loads the current policy and the repeated edit is accepted.
  await page.getByRole("button", { name: "Cancel" }).click()
  await page.getByRole("button", { name: "Edit" }).click()
  await page.getByLabel("Name", { exact: true }).fill("Edited against version 4")
  await page.getByRole("button", { name: "Update profile" }).click()
  await expect.poll(() => api.patches.length).toBe(1)
  expect(api.patches[0].policy_version).toBe(4)
  expect((api.patches[0].policy as Record<string, unknown>).rules).toEqual(presetFixture.applied_rules.balanced)
  expect(api.unexpected).toEqual([])
})
