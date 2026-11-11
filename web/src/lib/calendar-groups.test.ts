import { describe, expect, it } from "vitest"

import type { APICalendarEntry } from "@/api/client"
import { groupCalendarDay } from "@/lib/calendar-groups"

const entry = (media: string, episode: number, state: string, extra: Partial<APICalendarEntry> = {}): APICalendarEntry => ({
  media_id: media, media_kind: "series", media_title: `Show ${media}`, workflow_id: `wf-${media}-${episode}`, variant: `season:1:episode:${episode}`, season: 1, episode, air_date: "2026-11-04", monitored: true, workflow_state: state, ...extra,
})
const badge = (item: APICalendarEntry) => item.workflow_state === "plex_confirmed" ? { tone: "good" as const, text: "I Plex" } : item.workflow_state === "needs_attention" ? { tone: "warn" as const, text: "Kræver handling" } : { tone: "neutral" as const, text: "Venter" }

describe("groupCalendarDay", () => {
  it("keeps a single episode with its own code and title", () => {
    const [group] = groupCalendarDay([entry("a", 3, "waiting", { episode_title: "Pilot" })], badge)
    expect(group.label).toBe("S01E03 · Pilot")
    expect(group.badge).toEqual({ tone: "neutral", text: "Venter" })
  })

  it("makes a season released in one go one tile with the episode range", () => {
    const groups = groupCalendarDay([entry("a", 2, "plex_confirmed"), entry("a", 1, "plex_confirmed"), entry("b", 5, "waiting"), entry("a", 8, "plex_confirmed")], badge)
    expect(groups.map((group) => group.key)).toEqual(["a:1", "b:1"])
    expect(groups[0].label).toBe("S01E01–E08 · 3 afsnit")
    expect(groups[0].badge).toEqual({ tone: "good", text: "I Plex" })
  })

  it("counts mixed states instead of hiding any, most urgent first", () => {
    const [group] = groupCalendarDay([entry("a", 1, "plex_confirmed"), entry("a", 2, "plex_confirmed"), entry("a", 3, "needs_attention"), entry("a", 4, "waiting")], badge)
    expect(group.badge).toEqual({ tone: "warn", text: "1 Kræver handling · 1 Venter · 2 I Plex" })
  })
})
