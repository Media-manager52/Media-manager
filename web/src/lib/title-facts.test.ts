import { describe, expect, it } from "vitest"

import type { APIMedia, APIMetadataCredit } from "@/api/client"
import { castList, creditNames, runtime, titleFacts } from "@/lib/title-facts"

const metadata = { genres: [], countries: [], networks: [], aliases: [], sources: [], ratings: [], events: [], observed_at: "2026-11-10T00:00:00Z" }
const credit = (name: string, role: string, character?: string): APIMetadataCredit => ({ name, role, character, source: "tmdb" })

describe("titleFacts", () => {
  it("shows year, length, age rating and the TMDB score for a film", () => {
    const media: APIMedia = { id: "m", kind: "movie", title: "Film", year: 2025, identities: [], metadata: { ...metadata, runtime_minutes: 101, certification: "15", ratings: [{ source: "imdb", value: 6.1, votes: 1 }, { source: "tmdb", value: 7.25, votes: 9 }] } }
    expect(titleFacts(media)).toEqual(["2025", "1 t 41 min", "15", "★ 7,3"])
  })
  it("shows the number of seasons for a series and leaves out what is unknown", () => {
    const media: APIMedia = { id: "s", kind: "series", title: "Serie", identities: [], metadata: { ...metadata, season_count: 1, runtime_minutes: 45, ratings: [{ source: "tmdb", value: 0, votes: 0 }] } }
    expect(titleFacts(media)).toEqual(["1 sæson"])
  })
})

describe("runtime", () => {
  it("writes hours and minutes", () => {
    expect(runtime(45)).toBe("45 min")
    expect(runtime(120)).toBe("2 t")
    expect(runtime(135)).toBe("2 t 15 min")
  })
})

describe("credits", () => {
  const credits = [credit("A", "Cast", "Hero"), credit("B", "Director"), credit("A", "Actor", "Hero again"), credit("C", "Actor"), credit("B", "Director"), credit("D", "Creator")]
  it("lists each actor once in billing order", () => {
    expect(castList(credits).map((entry) => entry.name)).toEqual(["A", "C"])
  })
  it("lists each director or creator once", () => {
    expect(creditNames(credits, ["Director"], 3)).toEqual(["B"])
    expect(creditNames(credits, ["Creator", "Showrunner"], 3)).toEqual(["D"])
  })
})
