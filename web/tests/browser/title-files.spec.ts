import { expect, test } from "@playwright/test"

import { ids, mockApi } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

const path = (episode: string) => `Testserien (2024) {tvdb-1}/Season 01/Testserien (2024) - ${episode} [1080p MM-01a1bc17b7d972a9ad314f307a7adfb0 aac webdl].mkv`
const base = { media_id: ids.movie, media_kind: "series", title: "Testserien", root_name: "Serier", origin: "altmount", state: "current", plex_confirmed: true, source_size_bytes: 3_000_000_000 }
const subtitles = ["da", "eng", "swe", "nob"]
const files = [
  { ...base, id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1", destination_relative: path("S01E01 - Gæsten"), source_relative: "media-manager/a/e1.mkv", quality_evidence: { facts: { resolution: "1080p", video_codec: "h264", audio_languages: ["eng"], subtitle_languages: subtitles } } },
  { ...base, id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2", destination_relative: path("S01E02 - Rejsen"), source_relative: "media-manager/a/e2.mkv", quality_evidence: {} },
]

test("library files show a short name and keep the path one tap away", async ({ page }) => {
  await mockApi(page)
  await page.route(`**/api/v1/media/${ids.movie}/detail`, (route) => route.fulfill({ json: { data: {
    media: { id: ids.movie, kind: "series", title: "Testserien", year: 2024, identities: [], metadata: {} }, workflows: [], seasons: [], files,
  } } }))
  await page.goto(`/bibliotek/${ids.movie}`)
  const first = page.getByRole("button", { name: /S01E01 · Gæsten/ })
  await expect(first).toContainText("Tekster: Dansk, Engelsk + 2 andre")
  await expect(page.getByRole("button", { name: /S01E02 · Rejsen/ })).toBeVisible()
  await expect(page.getByText(files[0].destination_relative)).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Erstat dårlig fil", exact: true })).toHaveCount(0)
  await first.click()
  await expect(page.getByText(files[0].destination_relative)).toBeVisible()
  await expect(page.getByText("Dansk, Engelsk, Svensk, Norsk")).toBeVisible()
  await expect(page.getByRole("button", { name: "Erstat dårlig fil", exact: true })).toHaveCount(1)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})
