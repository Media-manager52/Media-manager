import { expect, test } from "@playwright/test"

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
  await page.getByLabel("Resolution policy").click()
  await page.getByRole("option", { name: "Best available", exact: true }).click()
  await page.getByLabel("Audio policy").click()
  await page.getByRole("option", { name: "Best available", exact: true }).click()
  await page.getByRole("button", { name: "Update profile" }).click()

  expect(saved).toBeDefined()
  const policy = saved?.policy as Record<string, unknown>
  const rules = policy.rules as Array<Record<string, unknown>>
  expect(rules.some((rule) => rule.fact === "resolution" && rule.kind === "required")).toBe(false)
  expect(rules.filter((rule) => rule.fact === "resolution" && rule.kind === "preferred").map((rule) => [rule.allowed_values, rule.weight])).toEqual([
    [["2160p"], 50000], [["1080p"], 40000], [["720p"], 30000], [["576p"], 20000], [["480p"], 10000],
  ])
  expect(rules.filter((rule) => rule.fact === "audio_codec" && rule.kind === "preferred")).toHaveLength(14)
  expect(policy.required_subtitle_languages).toEqual(["da"])
  await page.getByRole("button", { name: "Edit" }).click()
  await expect(page.getByLabel("Resolution policy")).toHaveText("Best available")
  await expect(page.getByLabel("Audio policy")).toHaveText("Best available")
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
