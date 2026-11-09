import { describe, expect, it } from "vitest"

import type { APIMedia } from "@/api/client"
import { searchTitles, sortMedia } from "@/lib/library-filter"

const media: APIMedia[] = [
  { id: "1", kind: "movie", title: "Ærø", year: 2001, identities: [] },
  { id: "2", kind: "series", title: "Borgen", year: 2010, identities: [], metadata: { aliases: [{ name: "Slottet", source: "tmdb" }] } as APIMedia["metadata"] },
  { id: "3", kind: "movie", title: "Amélie", year: 2001, identities: [] },
  { id: "4", kind: "movie", title: "Den store Borgfest", year: 2015, identities: [] },
]

describe("sorting by when a title was added", () => {
  it("puts the newest file first and titles without a file last, by title", () => {
    const addedAt = new Map([["1", "2026-11-01T10:00:00Z"], ["2", "2026-11-08T10:00:00Z"]])
    expect(sortMedia(media, "senest-tilfoejet", addedAt).map((item) => item.title)).toEqual(["Borgen", "Ærø", "Amélie", "Den store Borgfest"])
  })

  it("falls back to title order when nothing is known", () => {
    expect(sortMedia(media, "senest-tilfoejet").map((item) => item.id)).toEqual(["3", "2", "4", "1"])
  })

  it("does not change the list it was given", () => {
    const before = media.map((item) => item.id)
    sortMedia(media, "senest-tilfoejet", new Map([["4", "2026-01-01T00:00:00Z"]]))
    expect(media.map((item) => item.id)).toEqual(before)
  })
})

describe("the search field in the menu", () => {
  it("needs two characters", () => {
    expect(searchTitles(media, "b", 6)).toEqual([])
    expect(searchTitles(media, " b ", 6)).toEqual([])
  })

  it("puts titles that start with the words before titles that contain them", () => {
    const more: APIMedia[] = [...media, { id: "5", kind: "movie", title: "Aftenborg", year: 1999, identities: [] }]
    expect(searchTitles(more, "borg", 6).map((item) => item.title)).toEqual(["Borgen", "Aftenborg", "Den store Borgfest"])
  })

  it("finds aliases and ignores accents", () => {
    expect(searchTitles(media, "slot", 6).map((item) => item.id)).toEqual(["2"])
    expect(searchTitles(media, "amel", 6).map((item) => item.id)).toEqual(["3"])
  })

  it("returns at most the limit", () => {
    expect(searchTitles(media, "20", 1)).toHaveLength(0)
    expect(searchTitles(media, "or", 1).map((item) => item.id)).toEqual(["2"])
  })
})
