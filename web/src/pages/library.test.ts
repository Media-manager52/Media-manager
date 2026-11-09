import { describe, expect, it } from "vitest"

import type { APIMedia } from "@/api/client"
import { filterMedia, inFolder, libraryFolders, matchesStatus, sortMedia } from "@/pages/library"

const media: APIMedia[] = [
  { id: "1", kind: "movie", title: "Ærø", year: 2001, identities: [] },
  { id: "2", kind: "series", title: "Borgen", year: 2010, identities: [], metadata: { aliases: [{ name: "Borgen – Power & Glory", source: "tmdb" }] } as APIMedia["metadata"] },
  { id: "3", kind: "movie", title: "Amélie", year: 2001, identities: [], metadata: { original_title: "Le Fabuleux Destin d'Amélie Poulain" } as APIMedia["metadata"] },
]

describe("library filtering", () => {
  it("filters by kind", () => {
    expect(filterMedia(media, "serier", "").map((item) => item.id)).toEqual(["2"])
    expect(filterMedia(media, "film", "").map((item) => item.id)).toEqual(["1", "3"])
  })

  it("matches titles, original titles and aliases without accents", () => {
    expect(filterMedia(media, "alle", "amelie").map((item) => item.id)).toEqual(["3"])
    expect(filterMedia(media, "alle", "fabuleux").map((item) => item.id)).toEqual(["3"])
    expect(filterMedia(media, "alle", "power").map((item) => item.id)).toEqual(["2"])
    expect(filterMedia(media, "alle", "2010").map((item) => item.id)).toEqual(["2"])
  })

  it("sorts Danish letters after Z", () => {
    expect(sortMedia(media, "titel").map((item) => item.title)).toEqual(["Amélie", "Borgen", "Ærø"])
    expect(sortMedia(media, "aar-nyeste")[0].title).toBe("Borgen")
  })
})

describe("library status filter", () => {
  const wanted = { media_id: "1", in_plex: false, missing: 1, attention: false }
  const partial = { media_id: "2", in_plex: true, missing: 3, attention: true }
  const done = { media_id: "3", in_plex: true, missing: 0, attention: false }
  const cancelled = { media_id: "4", in_plex: false, missing: 0, attention: false }
  it("sorts titles into Mangler noget, Kræver handling and Ikke hentet", () => {
    expect([wanted, partial, done, cancelled].filter((entry) => matchesStatus(entry, "mangler")).map((entry) => entry.media_id)).toEqual(["1", "2"])
    expect([wanted, partial, done, cancelled].filter((entry) => matchesStatus(entry, "handling")).map((entry) => entry.media_id)).toEqual(["2"])
    expect([wanted, partial, done, cancelled].filter((entry) => matchesStatus(entry, "ikke-hentet")).map((entry) => entry.media_id)).toEqual(["4"])
  })
})

describe("library folders", () => {
  const roots = [
    { id: "film", name: "Film", media_kind: "movie" as const },
    { id: "boern", name: "Børnefilm", media_kind: "movie" as const },
    { id: "serier", name: "Serier", media_kind: "series" as const },
    { id: "tom", name: "Tom mappe", media_kind: "movie" as const },
  ]
  const statuses = [
    { media_id: "1", in_plex: true, missing: 0, attention: false, root_ids: ["film", "boern"] },
    { media_id: "2", in_plex: true, missing: 0, attention: false, root_ids: ["serier"] },
    { media_id: "3", in_plex: true, missing: 0, attention: false, root_ids: ["film"] },
    { media_id: "4", in_plex: false, missing: 1, attention: false },
  ]

  it("lists the folders that hold a title, in setup order, with counts", () => {
    expect(libraryFolders(roots, "alle", statuses)).toEqual([{ id: "film", name: "Film", count: 2 }, { id: "boern", name: "Børnefilm", count: 1 }, { id: "serier", name: "Serier", count: 1 }])
  })

  it("follows the chosen type", () => {
    expect(libraryFolders(roots, "serier", statuses).map((entry) => entry.id)).toEqual(["serier"])
    expect(libraryFolders(roots, "film", statuses).map((entry) => entry.id)).toEqual(["film", "boern"])
  })

  it("matches only a title with a file in that folder", () => {
    expect(inFolder(statuses[0], "boern")).toBe(true)
    expect(inFolder(statuses[2], "boern")).toBe(false)
    expect(inFolder(statuses[3], "film")).toBe(false)
    expect(inFolder(undefined, "film")).toBe(false)
  })
})
