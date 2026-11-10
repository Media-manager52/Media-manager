import type { APICalendarEntry, APIRecentAddition } from "@/api/client"

/** "God morgen" before 10, "God dag" until 17, then "God aften"; the night (before 5) is also evening. */
export function greeting(now: Date): string {
  const hour = now.getHours()
  if (hour >= 5 && hour < 10) return "God morgen"
  if (hour >= 10 && hour < 17) return "God dag"
  return "God aften"
}

export type Tile = {
  key: string
  href: string
  label: "Næste afsnit" | "Kom i biblioteket"
  title: string
  detail: string
  image?: string
  kind: "movie" | "series"
}

/**
 * The two wide picture tiles at the top of the overview: the next episodes
 * that air from today on, filled up with the newest arrivals when fewer than
 * two episodes are coming. One tile per title, so a show airing twice this
 * week does not take both places.
 */
export function wideTiles(calendar: APICalendarEntry[], recent: APIRecentAddition[], today: Date, describe: (entry: APICalendarEntry) => string, describeRecent: (item: APIRecentAddition) => string, titleHref: (mediaID: string) => string): Tile[] {
  const start = new Date(today)
  start.setHours(0, 0, 0, 0)
  const tiles: Tile[] = []
  const seen = new Set<string>()
  const upcoming = calendar
    .filter((entry) => new Date(entry.air_date).getTime() >= start.getTime())
    .sort((a, b) => new Date(a.air_date).getTime() - new Date(b.air_date).getTime())
  for (const entry of upcoming) {
    if (tiles.length === 2) break
    if (seen.has(entry.media_id)) continue
    seen.add(entry.media_id)
    tiles.push({ key: `next-${entry.media_id}`, href: titleHref(entry.media_id), label: "Næste afsnit", title: entry.media_title, detail: describe(entry), image: entry.backdrop_url ?? entry.poster_url, kind: entry.media_kind })
  }
  for (const item of recent.slice(1)) {
    if (tiles.length === 2) break
    if (seen.has(item.media_id)) continue
    seen.add(item.media_id)
    tiles.push({ key: `new-${item.media_id}`, href: titleHref(item.media_id), label: "Kom i biblioteket", title: item.title, detail: describeRecent(item), image: item.backdrop_url ?? item.poster_url, kind: item.media_kind })
  }
  return tiles
}
