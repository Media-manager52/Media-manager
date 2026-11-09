import { describe, expect, it } from "vitest"

import type { APIMedia } from "@/api/client"
import { guessMedia, proposedName, seasonVariant, validSeasonVariant } from "@/lib/adoption"

const media = (id: string, title: string, year?: number) => ({ id, kind: "movie", title, year, identities: [] }) as APIMedia
const all = [media("a", "Flammen & Citronen", 2008), media("b", "Dune", 1984), media("c", "Dune", 2021), media("d", "Heat", 1995)]

describe("guessMedia", () => {
  it("matches title and year from the folder", () => {
    expect(guessMedia("Dune (2021)/Dune.2021.mkv", all)?.id).toBe("c")
  })
  it("matches a dotted file name", () => {
    expect(guessMedia("Heat.1995.1080p.mkv", all)?.id).toBe("d")
  })
  it("does not guess between equal titles without a year", () => {
    expect(guessMedia("Dune/Dune.mkv", all)).toBeUndefined()
  })
  it("does not offer a wrong year or a near title", () => {
    expect(guessMedia("Dune (2000)/Dune.mkv", all)).toBeUndefined()
    expect(guessMedia("Heat 2 (1995)/x.mkv", all)).toBeUndefined()
  })
})

describe("seasonVariant", () => {
  it("reads the season folder and episode name", () => {
    expect(seasonVariant("Show (2020)/Season 3/Show.S03E01.mkv")).toBe("season:03")
    expect(seasonVariant("Show/Sæson 12/e1.mkv")).toBe("season:12")
    expect(seasonVariant("Show.s01e02.mkv")).toBe("season:01")
  })
  it("leaves it empty when unclear or contradictory", () => {
    expect(seasonVariant("Show/e1.mkv")).toBe("")
    expect(seasonVariant("Show/Season 1/Show.S02E01.mkv")).toBe("")
  })
  it("validates what the server accepts", () => {
    expect(validSeasonVariant("season:03")).toBe(true)
    expect(validSeasonVariant("season:3")).toBe(false)
  })
})

describe("proposedName", () => {
  it("keeps a file at its own place in the library", () => {
    expect(proposedName("/libraries/movies", "/libraries/movies", "A (2020)/a.mkv")).toBe("A (2020)/a.mkv")
    expect(proposedName("/libraries/movies/Gamle/2000", "/libraries/movies/", "A (2020)/a.mkv")).toBe("Gamle/2000/A (2020)/a.mkv")
  })
  it("does not treat a similar folder name as inside the library", () => {
    expect(proposedName("/libraries/movies2", "/libraries/movies", "a.mkv")).toBe("a.mkv")
  })
})
