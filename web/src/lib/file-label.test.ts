import { describe, expect, it } from "vitest"

import { fileLabel, shortLanguages } from "./file-label"

describe("fileLabel", () => {
  it("names an episode by its code and title", () => {
    expect(fileLabel("Show (2026) {tvdb-1}/Season 01/Show (2026) - S01E03 - The Guest [1080p MM-01a1 aac webdl].mkv")).toBe("S01E03 · The Guest")
  })
  it("keeps the code alone when the episode has no title", () => {
    expect(fileLabel("Show/Season 02/Show - S02E10 [720p].mkv")).toBe("S02E10")
  })
  it("keeps a multi-episode range", () => {
    expect(fileLabel("Show/Season 01/Show - S01E01-E02 - Pilot [1080p].mkv")).toBe("S01E01-E02 · Pilot")
    expect(fileLabel("Show/Season 01/Show - S01E20E21 - Finale [1080p].mkv")).toBe("S01E20-E21 · Finale")
  })
  it("names a film by its title and year without tags", () => {
    expect(fileLabel("Film (2025) {tmdb-12}/Film (2025) {tmdb-12} - [2160p MM-01a1 hdr hevc].mkv")).toBe("Film (2025)")
  })
  it("falls back to the path when nothing readable is left", () => {
    expect(fileLabel("[x].mkv")).toBe("[x].mkv")
  })
})

describe("shortLanguages", () => {
  it("lists a few names in full", () => {
    expect(shortLanguages(["Engelsk", "Dansk"])).toBe("Dansk, Engelsk")
  })
  it("puts Danish first and counts the rest", () => {
    expect(shortLanguages(["Arabisk", "Engelsk", "Dansk", "Svensk"])).toBe("Dansk, Arabisk + 2 andre")
    expect(shortLanguages(["Engelsk", "Svensk", "Norsk"])).toBe("Engelsk, Svensk + 1 andet")
  })
  it("is empty for no names", () => {
    expect(shortLanguages([])).toBe("")
  })
})
