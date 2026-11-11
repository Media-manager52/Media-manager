import type { APICalendarEntry } from "@/api/client"
import type { Tone } from "@/lib/tone"

export type CalendarBadge = { tone: Tone; text: string }

export type CalendarGroup = {
  key: string
  first: APICalendarEntry
  entries: APICalendarEntry[]
  /** "S01E03 · Title" for one episode, "S01E01–E08 · 8 afsnit" for several. */
  label: string
  badge: CalendarBadge
}

const episodeCode = (season: number, episode: number) => `S${String(season).padStart(2, "0")}E${String(episode).padStart(2, "0")}`
const toneRank: Record<Tone, number> = { bad: 0, warn: 1, info: 2, neutral: 3, good: 4 }

/**
 * Groups one day's episodes by show and season, so a season released in one
 * go is one picture instead of a row of identical ones. The badge keeps every
 * state: one state reads as before, mixed states are counted ("6 I Plex · 2
 * Venter") with the most urgent tone.
 */
export function groupCalendarDay(entries: APICalendarEntry[], badgeOf: (entry: APICalendarEntry) => CalendarBadge): CalendarGroup[] {
  const groups = new Map<string, APICalendarEntry[]>()
  for (const entry of entries) {
    const key = `${entry.media_id}:${entry.season}`
    groups.set(key, [...(groups.get(key) ?? []), entry])
  }
  return [...groups.entries()].map(([key, list]) => {
    const sorted = [...list].sort((a, b) => a.episode - b.episode)
    const first = sorted[0]
    const last = sorted[sorted.length - 1]
    const label = sorted.length === 1
      ? `${episodeCode(first.season, first.episode)}${first.episode_title ? ` · ${first.episode_title}` : ""}`
      : `${episodeCode(first.season, first.episode)}–E${String(last.episode).padStart(2, "0")} · ${sorted.length} afsnit`
    return { key, first, entries: sorted, label, badge: combineBadges(sorted.map(badgeOf)) }
  })
}

function combineBadges(badges: CalendarBadge[]): CalendarBadge {
  const counts = new Map<string, { badge: CalendarBadge; count: number }>()
  for (const badge of badges) {
    const entry = counts.get(badge.text)
    if (entry) entry.count++
    else counts.set(badge.text, { badge, count: 1 })
  }
  const distinct = [...counts.values()].sort((a, b) => toneRank[a.badge.tone] - toneRank[b.badge.tone] || b.count - a.count)
  if (distinct.length === 1) return distinct[0].badge
  return { tone: distinct[0].badge.tone, text: distinct.map((entry) => `${entry.count} ${entry.badge.text}`).join(" · ") }
}
