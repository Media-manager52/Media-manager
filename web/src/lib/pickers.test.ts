import { describe, expect, it } from "vitest"

import type { APILibraryRoot, APIQualityProfile } from "@/api/client"
import { profileOptions, profileSummary, rootOptions } from "@/lib/pickers"

const root = (name: string, extra: Partial<APILibraryRoot> = {}): APILibraryRoot => ({
  id: name, media_kind: "movie", name, destination_root: `/srv/${name}`, plex_visible_root: `/media/${name}`, source_visible_root: "/mnt/altmount", is_default: false, enabled: true, edition: "standard", ...extra,
})
const profile = (name: string, policy: Record<string, unknown>, extra: Partial<APIQualityProfile> = {}): APIQualityProfile => ({
  id: name, media_kind: "movie", name, policy_version: 1, policy, upgrades_enabled: true, is_default: false, enabled: true, ...extra,
})

describe("library choices", () => {
  it("puts the automatic choice first, then the default, and 4K in its own group", () => {
    const options = rootOptions([root("Horror"), root("Film 4K", { edition: "4k" }), root("Movies Primary", { is_default: true }), root("Serier", { media_kind: "series" }), root("Off", { enabled: false })], "movie", { label: "Automatisk", description: "Standardmappen." })
    expect(options.map((option) => option.label)).toEqual(["Automatisk", "Movies Primary", "Horror", "Film 4K"])
    expect(options[0]).toMatchObject({ value: "", description: "Standardmappen. Lige nu: Movies Primary.", summary: "Lige nu: Movies Primary" })
    expect(options[1]).toMatchObject({ badges: ["Standard"], group: "Biblioteker", description: "/media/Movies Primary" })
    expect(options[3]).toMatchObject({ badges: ["4K"], group: "4K-udgaver" })
    expect(options[3].description).toContain("Ekstra 4K-udgave af film")
  })

  it("has no automatic choice when none is asked for", () => {
    expect(rootOptions([root("Horror")], "movie").map((option) => option.value)).toEqual(["Horror"])
  })
})

describe("quality profile choices", () => {
  it("says in Danish what each profile demands", () => {
    expect(profileSummary(profile("Subs", { required_subtitle_languages: ["da"] }))).toBe("Kræver danske undertekster eller tale · bedste kvalitet")
    expect(profileSummary(profile("Kids", { required_audio_languages: ["da"], required_subtitle_languages: [] }))).toBe("Kræver dansk tale · bedste kvalitet")
    expect(profileSummary(profile("Original", { required_subtitle_languages: [], original_language_routing: ["da"] }))).toBe("Intet sprogkrav · bedste kvalitet · vælges selv til titler på dansk")
    expect(profileSummary(profile("4K", { rules: [{ fact: "resolution", kind: "required", allowed_values: ["2160p"] }] }))).toBe("Intet sprogkrav · kræver 2160p")
  })

  it("names the profile the automatic choice means today", () => {
    const options = profileOptions([profile("B", {}), profile("A", {}, { is_default: true })], "movie", { label: "Automatisk", description: "Standardprofilen." })
    expect(options.map((option) => option.label)).toEqual(["Automatisk", "A", "B"])
    expect(options[0].description).toBe("Standardprofilen. Lige nu: A.")
    expect(options[1].badges).toEqual(["Standard"])
  })
})
