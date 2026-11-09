import type { APIDiscoveryDetail } from "@/api/client"

/** How far back "Seneste sæson" looks past seasons that have not started. */
const maxSeasonsBack = 5

/**
 * The newest season with an episode that has aired. TMDB counts an announced
 * season as soon as it is listed, and a new user who picks "Seneste sæson"
 * wants something that can be watched. Falls back to the newest season number
 * when nothing can be determined; adding it then still reports a clear error
 * if TMDB has no episode list.
 */
export async function latestAiredSeason(
  seasonCount: number,
  loadSeason: (number: number) => Promise<Pick<APIDiscoveryDetail, "season">>,
  today: string,
): Promise<number> {
  for (let number = seasonCount; number >= 1 && number > seasonCount - maxSeasonsBack; number--) {
    try {
      const { season } = await loadSeason(number)
      if (season?.episodes.some((episode) => episode.air_date && episode.air_date.slice(0, 10) <= today)) return number
    } catch {
      break
    }
  }
  return seasonCount
}
