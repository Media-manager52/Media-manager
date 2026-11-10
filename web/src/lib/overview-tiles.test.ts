import { describe, expect, it } from "vitest"

import type { APICalendarEntry, APIRecentAddition } from "@/api/client"
import { greeting, wideTiles } from "@/lib/overview-tiles"

const entry = (media: string, air: string, extra: Partial<APICalendarEntry> = {}): APICalendarEntry => ({
  media_id: media, media_kind: "series", media_title: `Show ${media}`, workflow_id: `wf-${media}-${air}`, variant: "season:01", season: 1, episode: 1, air_date: air, monitored: true, workflow_state: "waiting", ...extra,
})
const recent = (media: string, extra: Partial<APIRecentAddition> = {}): APIRecentAddition => ({
  media_id: media, media_kind: "movie", title: `Film ${media}`, variant: "default", added_at: "2026-11-10T20:00:00Z", ...extra,
})
const href = (id: string) => `/titel/${id}`

describe("greeting", () => {
  it("follows the time of day", () => {
    expect(greeting(new Date(2026, 10, 10, 7))).toBe("God morgen")
    expect(greeting(new Date(2026, 10, 10, 12))).toBe("God dag")
    expect(greeting(new Date(2026, 10, 10, 21))).toBe("God aften")
    expect(greeting(new Date(2026, 10, 10, 2))).toBe("God aften")
  })
})

describe("wideTiles", () => {
  const today = new Date(2026, 10, 10, 12)
  it("shows the next two titles airing, one tile per title, with the wide artwork", () => {
    const tiles = wideTiles(
      [entry("b", "2026-11-13"), entry("a", "2026-11-11", { backdrop_url: "wide-a", poster_url: "poster-a" }), entry("a", "2026-11-12"), entry("old", "2026-11-01")],
      [recent("r0"), recent("r1")], today, () => "S01E01", () => "Film", href,
    )
    expect(tiles.map((tile) => tile.key)).toEqual(["next-a", "next-b"])
    expect(tiles[0]).toMatchObject({ label: "Næste afsnit", image: "wide-a", href: "/titel/a" })
  })
  it("fills up with recent arrivals, skipping the newest one (it has its own tile)", () => {
    const tiles = wideTiles([], [recent("r0"), recent("r1", { poster_url: "p1" }), recent("r2")], today, () => "", () => "Film", href)
    expect(tiles.map((tile) => tile.key)).toEqual(["new-r1", "new-r2"])
    expect(tiles[0]).toMatchObject({ label: "Kom i biblioteket", image: "p1" })
  })
  it("is empty when nothing airs and nothing has arrived", () => {
    expect(wideTiles([], [], today, () => "", () => "", href)).toEqual([])
  })
})
