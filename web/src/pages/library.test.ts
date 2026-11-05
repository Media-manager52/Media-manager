import { describe, expect, it } from "vitest"

import type { APIMedia } from "@/api/client"
import { filterMedia, matchesStatus, sortMedia } from "@/pages/library"

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
