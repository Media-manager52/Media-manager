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
