import type { APIGapTitle, APILibraryRoot, APIQualityProfile } from "@/api/client"

/** "Sæson 4", "Sæson 4 og 5", "Sæson 1–6", "Sæson 1, 3 og 5–7". */
export function seasonList(numbers: number[]): string {
  const sorted = [...new Set(numbers)].sort((a, b) => a - b)
  if (sorted.length === 0) return ""
  const parts: string[] = []
  let start = sorted[0]
  let previous = sorted[0]
  const close = () => { parts.push(previous - start >= 2 ? `${start}–${previous}` : previous === start ? `${start}` : `${start}, ${previous}`) }
  for (const number of sorted.slice(1)) {
    if (number === previous + 1) { previous = number; continue }
    close()
    start = number
    previous = number
  }
  close()
  const items = parts.join(", ").split(", ")
  const text = items.length === 1 ? items[0] : `${items.slice(0, -1).join(", ")} og ${items[items.length - 1]}`
  return `Sæson ${text}`
}

/**
 * The folder and profile new work on a title uses: the title's own when they
 * are still enabled (never a 4K folder), otherwise the defaults for its kind.
 */
export function gapChoice(gap: APIGapTitle, kind: "movie" | "series", roots: APILibraryRoot[], profiles: APIQualityProfile[]): { rootId: string; profileId: string } {
  const ownRoot = roots.find((root) => root.id === gap.root_id && root.enabled && root.edition !== "4k" && root.media_kind === kind)
  const ownProfile = profiles.find((profile) => profile.id === gap.quality_profile_id && profile.enabled && profile.media_kind === kind)
  return {
    rootId: ownRoot?.id ?? roots.find((root) => root.media_kind === kind && root.enabled && root.is_default)?.id ?? "",
    profileId: ownProfile?.id ?? profiles.find((profile) => profile.media_kind === kind && profile.enabled && profile.is_default)?.id ?? "",
  }
}

/** Danish for a series' provider status. */
export function seriesStatusLabel(status?: string): string | undefined {
  switch (status) {
    case "Returning Series": return "Kører stadig"
    case "Ended": return "Afsluttet"
    case "Canceled": return "Stoppet"
    case "In Production": return "Under produktion"
    case "Planned": return "Planlagt"
    default: return undefined
  }
}

/** Matches a title's name against a filter, ignoring case and accents. */
export function matchesTitle(title: string, filter: string): boolean {
  const fold = (value: string) => value.normalize("NFD").replace(/\p{Diacritic}/gu, "").toLowerCase()
  const wanted = fold(filter.trim())
  return wanted === "" || fold(title).includes(wanted)
}
