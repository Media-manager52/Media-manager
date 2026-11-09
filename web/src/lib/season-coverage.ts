import type { APILibraryRoot, APIManagedFile, APIMediaSeason } from "@/api/client"
import { parseSeriesVariant } from "@/lib/series-variants"

/** Display names can collide or change. Only persisted edition/root identity counts. */
export function managedFileEdition(file: APIManagedFile, roots: APILibraryRoot[]): string | undefined {
  if (file.edition === "standard" || file.edition === "4k") return file.edition
  return roots.find((root) => root.id === file.root_id)?.edition
}

/** Exact confirmed episode coverage, never a count of files or guessed expectations. */
export function seasonInPlex(season: APIMediaSeason | undefined, files: APIManagedFile[], edition: string, roots: APILibraryRoot[]): boolean {
  if (!season || season.expected_episodes <= 0) return false
  const expected = new Set((season.episodes ?? []).map((episode) => episode.number))
  if (expected.size !== season.expected_episodes || [...expected].some((number) => !Number.isInteger(number) || number <= 0)) return false
  const present = new Set<number>()
  for (const file of files) {
    if (file.state !== "current" || !file.plex_confirmed || managedFileEdition(file, roots) !== edition) continue
    const scope = parseSeriesVariant(file.variant)
    if (scope.kind === "other" || scope.season !== season.number) continue
    if (scope.kind === "episode") {
      if (expected.has(scope.episode)) present.add(scope.episode)
      continue
    }
    // RenderEpisodeSet publishes S01E01 or S01E01-E03. Read the filename,
    // never a title/folder token, and refuse ambiguous or malformed names.
    const filename = file.destination_relative.split("/").at(-1) ?? ""
    const tokens = [...filename.matchAll(/(?:^|[ ._-])S(\d{1,3})E(\d{1,4})(?:-E(\d{1,4}))?(?=[ ._]|$)/gi)]
    if (tokens.length !== 1 || Number(tokens[0][1]) !== season.number) continue
    const first = Number(tokens[0][2])
    const last = Number(tokens[0][3] ?? first)
    if (first <= 0 || last < first || last - first > 99) continue
    for (let number = first; number <= last; number++) if (expected.has(number)) present.add(number)
  }
  return [...expected].every((number) => present.has(number))
}
