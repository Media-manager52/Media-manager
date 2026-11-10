import { describe, expect, it } from "vitest"

import type { APILibraryRoot, APIQualityProfile } from "@/api/client"

import { gapChoice, matchesTitle, seasonList, seriesStatusLabel } from "./library-gaps"

const root = (id: string, extra: Partial<APILibraryRoot> = {}): APILibraryRoot => ({ id, media_kind: "series", name: id, destination_root: "/d", plex_visible_root: "/p", source_visible_root: "/s", is_default: false, enabled: true, ...extra })
const profile = (id: string, extra: Partial<APIQualityProfile> = {}): APIQualityProfile => ({ id, media_kind: "series", name: id, policy_version: 1, policy: {}, upgrades_enabled: true, is_default: false, enabled: true, ...extra })

describe("seasonList", () => {
  it("names one, two, runs and mixes of seasons", () => {
    expect(seasonList([4])).toBe("Sæson 4")
    expect(seasonList([5, 4])).toBe("Sæson 4 og 5")
    expect(seasonList([1, 2, 3, 4, 5, 6])).toBe("Sæson 1–6")
    expect(seasonList([1, 3, 5, 6, 7])).toBe("Sæson 1, 3 og 5–7")
    expect(seasonList([])).toBe("")
  })
})

describe("gapChoice", () => {
  it("keeps the title's own folder and profile while they are usable", () => {
    const roots = [root("own"), root("default", { is_default: true })]
    const profiles = [profile("own-profile"), profile("default-profile", { is_default: true })]
    expect(gapChoice({ media_id: "m", title: "T", root_id: "own", quality_profile_id: "own-profile" }, "series", roots, profiles)).toEqual({ rootId: "own", profileId: "own-profile" })
  })
  it("falls back to the defaults for a disabled, 4K or missing folder and profile", () => {
    const roots = [root("off", { enabled: false }), root("uhd", { edition: "4k" }), root("default", { is_default: true }), root("film", { media_kind: "movie", is_default: true })]
    const profiles = [profile("off", { enabled: false }), profile("default-profile", { is_default: true })]
    expect(gapChoice({ media_id: "m", title: "T", root_id: "off", quality_profile_id: "off" }, "series", roots, profiles)).toEqual({ rootId: "default", profileId: "default-profile" })
    expect(gapChoice({ media_id: "m", title: "T", root_id: "uhd" }, "series", roots, profiles).rootId).toBe("default")
    expect(gapChoice({ media_id: "m", title: "T" }, "movie", roots, profiles)).toEqual({ rootId: "film", profileId: "" })
  })
})

describe("labels and filter", () => {
  it("translates provider statuses and matches titles without accents", () => {
    expect(seriesStatusLabel("Returning Series")).toBe("Kører stadig")
    expect(seriesStatusLabel("Ended")).toBe("Afsluttet")
    expect(seriesStatusLabel("Something")).toBeUndefined()
    expect(matchesTitle("Pokémon", "pokemon")).toBe(true)
    expect(matchesTitle("Matador", "  ")).toBe(true)
    expect(matchesTitle("Matador", "borgen")).toBe(false)
  })
})
