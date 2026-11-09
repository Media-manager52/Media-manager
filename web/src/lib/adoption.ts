import type { APIMedia } from "@/api/client"

export const adoptionPhrase = "EXECUTE REVIEWED ADOPTION"
/** The server executes at most this many approved items in one batch. */
export const adoptionBatchLimit = 25

function normalized(value: string): string {
  return value.toLowerCase().normalize("NFKD").replace(/[̀-ͯ]/g, "").replace(/[^a-z0-9æøå]+/g, " ").trim()
}

/**
 * A suggestion only: the owner confirms every item. Reads "Title (Year)" from
 * the first folder (or the file name) and requires an exact title match, so a
 * near match is never offered as if it were right.
 */
export function guessMedia(relative: string, media: APIMedia[]): APIMedia | undefined {
  const first = relative.split("/")[0].replace(/\.[a-z0-9]{2,4}$/i, "")
  const match = first.match(/^(.*?)[\s._-]*[([{]?((?:19|20)\d{2})[)\]}]?/)
  const title = normalized(match?.[1] || first)
  const year = match?.[2] ? Number(match[2]) : undefined
  if (!title) return undefined
  const sameTitle = media.filter((item) => normalized(item.title) === title)
  if (year) return sameTitle.find((item) => item.year === year)
  return sameTitle.length === 1 ? sameTitle[0] : undefined
}

/** The season a file belongs to, read from "Season 01" or S01E02; empty if unclear. */
export function seasonVariant(relative: string): string {
  const seasons = new Set<number>()
  for (const match of relative.matchAll(/(?:^|[/ ._-])(?:season|sæson)[ ._-]?(\d{1,2})(?=[/ ._-]|$)/gi)) seasons.add(Number(match[1]))
  for (const match of relative.matchAll(/(?:^|[/ ._-])s(\d{1,2})e\d{1,4}/gi)) seasons.add(Number(match[1]))
  if (seasons.size !== 1) return ""
  return `season:${String([...seasons][0]).padStart(2, "0")}`
}

export function validSeasonVariant(value: string): boolean {
  return /^season:\d{2}$/.test(value)
}

/**
 * The scan lists files relative to the scanned folder, but the new name is
 * relative to the library. Without the prefix, a file in a subfolder would be
 * proposed for a move up to the top of the library.
 */
export function proposedName(sourceRoot: string, libraryRoot: string, relative: string): string {
  const root = libraryRoot.replace(/\/+$/, "")
  const source = sourceRoot.replace(/\/+$/, "")
  const prefix = source.startsWith(`${root}/`) ? source.slice(root.length + 1) : ""
  return prefix ? `${prefix}/${relative}` : relative
}
