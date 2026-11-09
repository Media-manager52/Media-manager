import { describe, expect, it } from "vitest"

import type { APIDiscoveryDetail } from "@/api/client"
import { latestAiredSeason } from "@/lib/latest-season"

const today = "2026-11-09"
const season = (number: number, dates: Array<string | undefined>) => ({ season: { number, episodes: dates.map((air_date, index) => ({ number: index + 1, air_date })) } }) as Pick<APIDiscoveryDetail, "season">

describe("latestAiredSeason", () => {
  it("takes the newest season when it has started", async () => {
    await expect(latestAiredSeason(3, async (number) => season(number, ["2026-01-01", "2027-01-01"]), today)).resolves.toBe(3)
  })

  it("skips an announced season", async () => {
    const seasons: Record<number, Pick<APIDiscoveryDetail, "season">> = { 3: season(3, ["2027-02-01"]), 2: season(2, [undefined]), 1: season(1, ["2020-01-01"]) }
    await expect(latestAiredSeason(3, async (number) => seasons[number], today)).resolves.toBe(1)
  })

  it("counts an episode airing today", async () => {
    await expect(latestAiredSeason(1, async (number) => season(number, ["2026-11-09"]), today)).resolves.toBe(1)
  })

  it("falls back to the newest number when TMDB cannot answer", async () => {
    await expect(latestAiredSeason(4, async () => { throw new Error("offline") }, today)).resolves.toBe(4)
  })

  it("stops looking back after a few seasons", async () => {
    const asked: number[] = []
    await expect(latestAiredSeason(20, async (number) => { asked.push(number); return season(number, ["2030-01-01"]) }, today)).resolves.toBe(20)
    expect(asked).toEqual([20, 19, 18, 17, 16])
  })
})
