import { describe, expect, it } from "vitest"

import type { APILibraryRoot, APIManagedFile, APIMediaSeason } from "@/api/client"
import { managedFileEdition, seasonInPlex } from "./season-coverage"

const season: APIMediaSeason = { number: 1, expected_episodes: 2, episodes: [{ number: 1 }, { number: 2 }], monitored: true, prefer_season_pack: true, search_immediately: true, state: "waiting", updated_at: "2026-01-01" }
const file = (token: string, patch: Partial<APIManagedFile> = {}): APIManagedFile => ({
  id: token, media_id: "show", media_kind: "series", title: "Show", root_id: "standard-root", root_name: "Renamed", edition: "standard", state: "current", plex_confirmed: true,
  destination_relative: `Show/Season 01/Show (2020) - ${token}.mkv`, source_relative: token, plex_visible_target: token, generation: 1, variant: "season:01", origin: "altmount", quality_profile: "Danish", quality_evidence_state: "media_info", quality_evidence: {}, ...patch,
})

describe("confirmed season coverage", () => {
  it("uses unique expected episodes only in the selected edition", () => {
    const files = [file("S01E01"), file("S01E02")]
    expect(seasonInPlex(season, files, "standard", [])).toBe(true)
    expect(seasonInPlex(season, files, "4k", [])).toBe(false)
    expect(seasonInPlex(season, [files[0], files[0]], "standard", [])).toBe(false)
    expect(seasonInPlex(season, [files[0], file("S01E02", { edition: "4k" })], "standard", [])).toBe(false)
  })

  it("requires current exact Plex confirmation and known expectations", () => {
    for (const patch of [{ state: "displaced" }, { plex_confirmed: false }, { edition: undefined, root_id: undefined }]) {
      expect(seasonInPlex(season, [file("S01E01-E02", patch)], "standard", [])).toBe(false)
    }
    expect(seasonInPlex({ ...season, episodes: undefined }, [file("S01E01-E02")], "standard", [])).toBe(false)
    expect(seasonInPlex({ ...season, episodes: [{ number: 1 }, { number: 1 }] }, [file("S01E01-E02")], "standard", [])).toBe(false)
  })

  it("reads exact canonical ranges and refuses ambiguous or malformed names", () => {
    expect(seasonInPlex(season, [file("S01E01-E02")], "standard", [])).toBe(true)
    for (const token of ["S02E01-E02", "S01E02-E01", "S01E00-E02", "S01E01-E1000", "S01E01-E02-E04", "S01E01E02", "S01E01 - S01E02"]) {
      expect(seasonInPlex(season, [file(token)], "standard", []), token).toBe(false)
    }
    const sparse = { ...season, episodes: [{ number: 1 }, { number: 3 }] }
    expect(seasonInPlex(sparse, [file("S01E01-E02")], "standard", [])).toBe(false)
    expect(seasonInPlex(sparse, [file("S01E01-E03")], "standard", [])).toBe(true)
  })

  it("deduplicates episode workflow scopes and does not use another season", () => {
    const first = file("Unusual adopted name", { variant: "season:01:episode:01", origin: "adoption" })
    const second = file("Another name", { variant: "season:1:episode:2" })
    expect(seasonInPlex(season, [first, second], "standard", [])).toBe(true)
    expect(seasonInPlex(season, [first, first], "standard", [])).toBe(false)
    expect(seasonInPlex(season, [first, file("S01E03", { variant: "season:01:episode:03" })], "standard", [])).toBe(false)
    expect(seasonInPlex(season, [first, { ...second, variant: "season:02:episode:02" }], "standard", [])).toBe(false)
  })

  it("uses stable root identity when an older file response omits edition", () => {
    const roots = [{ id: "standard-root", name: "Changed display name", edition: "4k" }] as APILibraryRoot[]
    expect(managedFileEdition(file("S01E01", { edition: undefined }), roots)).toBe("4k")
    expect(managedFileEdition(file("S01E01", { edition: undefined, root_id: "unknown" }), roots)).toBeUndefined()
    expect(managedFileEdition(file("S01E01", { edition: undefined }), [])).toBeUndefined()
  })
})
