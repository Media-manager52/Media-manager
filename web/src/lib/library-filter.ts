import type { APIMedia } from "@/api/client"

export type LibraryKind = "alle" | "film" | "serier"
export type LibrarySort = "titel" | "senest-tilfoejet" | "aar-nyeste" | "aar-aeldste"
export const librarySorts: LibrarySort[] = ["titel", "senest-tilfoejet", "aar-nyeste", "aar-aeldste"]

const fold = (value: string) => value.toLocaleLowerCase("da").normalize("NFD").replace(/[̀-ͯ]/g, "")
const collator = new Intl.Collator("da", { sensitivity: "base", numeric: true })

function names(item: APIMedia): string[] {
  return [item.title, item.metadata?.original_title ?? "", ...(item.metadata?.aliases ?? []).map((alias) => alias.name)]
}

export function filterMedia(items: APIMedia[], kind: LibraryKind, search: string): APIMedia[] {
  const needle = fold(search.trim())
  return items.filter((item) => {
    if (kind === "film" && item.kind !== "movie") return false
    if (kind === "serier" && item.kind !== "series") return false
    if (!needle) return true
    return names(item).some((name) => fold(name).includes(needle)) || String(item.year ?? "") === needle
  })
}

/**
 * Sorts the library. "senest-tilfoejet" uses when each title last got a file
 * (`addedAt`, by title id): newest first, and titles without a file last.
 */
export function sortMedia(items: APIMedia[], sort: LibrarySort, addedAt: ReadonlyMap<string, string> = new Map()): APIMedia[] {
  const sorted = [...items]
  const byTitle = (a: APIMedia, b: APIMedia) => collator.compare(a.title, b.title)
  if (sort === "titel") sorted.sort(byTitle)
  if (sort === "aar-nyeste") sorted.sort((a, b) => (b.year ?? 0) - (a.year ?? 0) || byTitle(a, b))
  if (sort === "aar-aeldste") sorted.sort((a, b) => (a.year ?? 9999) - (b.year ?? 9999) || byTitle(a, b))
  if (sort === "senest-tilfoejet") sorted.sort((a, b) => (addedAt.get(b.id) ?? "").localeCompare(addedAt.get(a.id) ?? "") || byTitle(a, b))
  return sorted
}

/**
 * The best matches for the search field in the menu: titles that start with
 * the words first, then titles that contain them, each group by title.
 */
export function searchTitles(items: APIMedia[], search: string, limit: number): APIMedia[] {
  const needle = fold(search.trim())
  if (needle.length < 2) return []
  const starts = (item: APIMedia) => (names(item).some((name) => fold(name).startsWith(needle)) ? 0 : 1)
  return filterMedia(items, "alle", search).sort((a, b) => starts(a) - starts(b) || collator.compare(a.title, b.title)).slice(0, limit)
}
