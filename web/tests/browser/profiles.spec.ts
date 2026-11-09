import { expect, test, type Page } from "@playwright/test"

import { mockApi } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

const ladderRule = { name: "Best", fact: "resolution", kind: "preferred", allowed_values: ["2160p"], weight: 50000, unknown: "allow" }
const customRule = { name: "Only HEVC", fact: "video_codec", kind: "required", allowed_values: ["hevc"], unknown: "reject" }
const routing = { da: { required_subtitle_languages: [] } }
const profile = {
  id: "p1", media_kind: "movie", name: "Dansk", policy_version: 4, upgrades_enabled: true, is_default: false, enabled: true,
  policy: { rules: [ladderRule, customRule], required_subtitle_languages: ["da"], required_audio_languages: [], preferred_subtitle_languages: [], original_language_routing: routing },
}
type Sent = { path: string; body: Record<string, any> | null } // eslint-disable-line @typescript-eslint/no-explicit-any

async function openProfiles(page: Page, overrides: Partial<typeof profile> = {}, respond?: (path: string) => { status: number; json: unknown }) {
  await mockApi(page)
  const sent: Sent[] = []
  await page.route("**/api/v1/settings/quality-profiles", (route) => {
    if (route.request().method() !== "GET") {
      sent.push({ path: "create", body: route.request().postDataJSON() })
      return route.fulfill({ status: 201, json: { data: profile } })
    }
    return route.fulfill({ json: { data: [{ ...profile, ...overrides }] } })
  })
  await page.route("**/api/v1/settings/quality-profiles/p1**", (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1/settings/quality-profiles/p1", "") || "patch"
    sent.push({ path, body: route.request().postDataJSON() })
    const answer = respond?.(path) ?? { status: 200, json: { data: { ...profile, policy_version: 5, applied: 3, skipped: 2 } } }
    return route.fulfill(answer)
  })
  await page.route("**/api/v1/settings/quality-profile-presets", (route) => route.fulfill({ json: { data: [{ preset: "storage_efficient", name: "Small", description: "", managed_facts: [], rules: [] }] } }))
  await page.goto("/indstillinger/profiler")
  return sent
}

test("Rediger saves the choices and keeps every rule the form cannot show", async ({ page }) => {
  const sent = await openProfiles(page)
  await page.getByRole("button", { name: "Rediger Dansk" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Navn").fill("Dansk ny")
  await dialog.getByLabel("Opløsning").selectOption("1080p")
  await dialog.getByRole("group", { name: "Kræv tale på" }).getByLabel("Dansk").check()
  await dialog.getByRole("button", { name: "Gem ændringer" }).click()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByText(/Det gælder nye søgninger/)).toBeVisible()
  expect(sent).toHaveLength(1)
  expect(sent[0]).toMatchObject({ path: "patch", body: { name: "Dansk ny", media_kind: "movie", policy_version: 4, is_default: false, enabled: true, policy: { original_language_routing: routing, required_subtitle_languages: ["da"], required_audio_languages: ["da"] } } })
  const rules = sent[0].body!.policy.rules
  expect(rules).toEqual(expect.arrayContaining([ladderRule, customRule]))
  expect(rules.find((rule: { kind: string; fact: string }) => rule.kind === "required" && rule.fact === "resolution")).toMatchObject({ allowed_values: ["1080p"] })
})

test("saving without changes writes the stored rules back untouched", async ({ page }) => {
  const sent = await openProfiles(page)
  await page.getByRole("button", { name: "Rediger Dansk" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Gem ændringer" }).click()
  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0].body!.policy).toEqual(profile.policy)
})

test("removing a language requirement is called out before saving", async ({ page }) => {
  await openProfiles(page)
  await page.getByRole("button", { name: "Rediger Dansk" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("alert")).toHaveCount(0)
  await dialog.getByRole("group", { name: "Kræv tekst eller tale på" }).getByLabel("Dansk").uncheck()
  await expect(dialog.getByRole("alert")).toContainText("Du fjerner et sprogkrav: Dansk")
})

test("a preset is applied on top of the saved version", async ({ page }) => {
  const sent = await openProfiles(page)
  await page.getByRole("button", { name: "Rediger Dansk" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Foretrækker").selectOption("storage_efficient")
  await dialog.getByRole("button", { name: "Gem ændringer" }).click()
  await expect.poll(() => sent.length).toBe(2)
  expect(sent[1]).toEqual({ path: "/preset", body: { preset: "storage_efficient", policy_version: 5 } })
})

test("a changed profile is refused in plain Danish and the dialog stays open", async ({ page }) => {
  await openProfiles(page, {}, () => ({ status: 409, json: { error: { code: "quality_profile_changed", message: "changed" } } }))
  await page.getByRole("button", { name: "Rediger Dansk" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Gem ændringer" }).click()
  await expect(page.getByText(/Profilen er blevet ændret imens/)).toBeVisible()
  await expect(dialog).toBeVisible()
})

test("the default profile cannot be switched off or lose its default mark", async ({ page }) => {
  await openProfiles(page, { is_default: true })
  await page.getByRole("button", { name: "Rediger Dansk" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByLabel("Profilen er slået til")).toBeDisabled()
  await expect(dialog.getByLabel(/Brug som standard/)).toBeDisabled()
})

test("Kopier starts a new extra profile with the same requirements", async ({ page }) => {
  const sent = await openProfiles(page, { is_default: true })
  await page.getByRole("button", { name: "Kopier Dansk" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByLabel("Navn")).toHaveValue("Kopi af Dansk")
  await dialog.getByRole("button", { name: "Opret profil" }).click()
  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0]).toMatchObject({ path: "create", body: { name: "Kopi af Dansk", is_default: false, policy: { rules: profile.policy.rules, required_subtitle_languages: ["da"], original_language_routing: routing } } })
})

test("Brug på ventende only runs when confirmed and reports what happened", async ({ page }) => {
  const sent = await openProfiles(page)
  await page.getByRole("button", { name: "Brug Dansk på ventende opgaver" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText("Filer, du allerede har, bliver ikke rørt.", { exact: false })).toBeVisible()
  expect(sent).toEqual([])
  await dialog.getByRole("button", { name: "Brug på ventende" }).click()
  await expect(dialog.getByRole("status")).toContainText("3 opgaver bruger nu de nye regler. 2 blev sprunget over")
  expect(sent).toEqual([{ path: "/apply-policy", body: null }])
})

test("Slet removes an unused profile after a confirmation", async ({ page }) => {
  const sent = await openProfiles(page)
  const methods: string[] = []
  page.on("request", (request) => { if (request.url().endsWith("/quality-profiles/p1")) methods.push(request.method()) })
  await page.getByRole("button", { name: "Slet Dansk" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Slet profilen Dansk?")
  expect(sent).toHaveLength(0)
  await dialog.getByRole("button", { name: "Slet profil" }).click()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByText("Profilen Dansk er slettet.")).toBeVisible()
  expect(sent).toEqual([{ path: "patch", body: null }])
  expect(methods).toEqual(["DELETE"])
})

test("Slet explains why a profile in use stays, and the default has no Slet", async ({ page }) => {
  await openProfiles(page, {}, () => ({ status: 409, json: { error: { code: "quality_profile_in_use", message: "in use" } } }))
  await page.getByRole("button", { name: "Slet Dansk" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Slet profil" }).click()
  await expect(dialog.getByRole("alert")).toContainText("Profilen kan ikke slettes")
  await dialog.getByRole("button", { name: "Fortryd" }).click()
  await openProfiles(page, { is_default: true })
  await expect(page.getByRole("button", { name: "Rediger Dansk" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Slet Dansk" })).toHaveCount(0)
})
