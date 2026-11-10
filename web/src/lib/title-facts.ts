import type { APIMedia, APIMetadataCredit } from "@/api/client"

/**
 * The short facts shown on the title picture: year, length or seasons, age
 * rating and the TMDB score. Missing facts are left out, never guessed.
 */
export function titleFacts(media: APIMedia): string[] {
  const meta = media.metadata
  const rating = meta?.ratings?.find((entry) => entry.source === "tmdb") ?? meta?.ratings?.[0]
  const seasons = meta?.season_count
  return [
    media.year ? String(media.year) : "",
    media.kind === "movie" && meta?.runtime_minutes ? runtime(meta.runtime_minutes) : "",
    media.kind === "series" && seasons ? `${seasons} ${seasons === 1 ? "sæson" : "sæsoner"}` : "",
    meta?.certification ?? "",
    rating && rating.value > 0 ? `★ ${rating.value.toLocaleString("da-DK", { minimumFractionDigits: 1, maximumFractionDigits: 1 })}` : "",
  ].filter(Boolean)
}

/** "1 t 41 min" from minutes; under an hour stays "45 min". */
export function runtime(minutes: number): string {
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  if (hours === 0) return `${rest} min`
  return rest ? `${hours} t ${rest} min` : `${hours} t`
}

/** The people listed under one of the roles, each name once, in the provider's order. */
export function creditNames(credits: APIMetadataCredit[], roles: string[], limit: number): string[] {
  const names: string[] = []
  for (const credit of credits) {
    if (!roles.includes(credit.role) || names.includes(credit.name)) continue
    names.push(credit.name)
    if (names.length === limit) break
  }
  return names
}

/** The actors in the provider's billing order, each person once. */
export function castList(credits: APIMetadataCredit[]): APIMetadataCredit[] {
  const seen = new Set<string>()
  return credits.filter((credit) => {
    if ((credit.role !== "Cast" && credit.role !== "Actor") || seen.has(credit.name)) return false
    seen.add(credit.name)
    return true
  })
}
