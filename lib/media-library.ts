import type {
  APIIdentity,
  APIManagedFile,
  APIMedia,
  APIWantedItem,
  APIWorkflow,
} from "@/lib/media-manager-api"

export type MediaLibrarySummary = "all" | "available" | "missing" | "monitoring" | "upgradable"

export type MediaLibraryFilters = {
  query: string
  summary: MediaLibrarySummary
  quality: string
  resolution: string
  audio: string
  language: string
  availability: MediaLibrarySummary
  root: string
  plexConfirmedOnly: boolean
  managedOnly: boolean
}

export type MediaLibraryRecord = {
  id: string
  kind: APIMedia["kind"]
  title: string
  year?: number
  identities: APIIdentity[]
  metadata?: APIMedia["metadata"]
  files: APIManagedFile[]
  wanted: APIWantedItem[]
  workflows: APIWorkflow[]
  profiles: string[]
  roots: string[]
  resolutions: string[]
  audioCodecs: string[]
  languages: string[]
  dynamicRanges: string[]
  available: boolean
  missing: boolean
  monitoring: boolean
  upgradable: boolean
  searchText: string
}

export const initialMediaFilters: MediaLibraryFilters = {
  query: "",
  summary: "all",
  quality: "",
  resolution: "",
  audio: "",
  language: "",
  availability: "all",
  root: "",
  plexConfirmedOnly: false,
  managedOnly: false,
}

function factValues(file: APIManagedFile, key: string) {
  const nested = file.quality_evidence.facts
  const facts = nested && typeof nested === "object" && !Array.isArray(nested)
    ? nested as Record<string, unknown>
    : file.quality_evidence
  const value = facts[key]
  if (Array.isArray(value)) return value.filter((item): item is string => typeof item === "string" && item.trim() !== "")
  if (typeof value === "string" && value.trim()) return [value]
  if (typeof value === "number") return [String(value)]
  return []
}

export function uniqueValues(values: string[]) {
  return Array.from(new Set(values.map((value) => value.trim()).filter(Boolean))).sort((left, right) => left.localeCompare(right))
}

function groupByMediaID<T extends { media_id: string }>(items: T[]) {
  const grouped = new Map<string, T[]>()
  for (const item of items) {
    const existing = grouped.get(item.media_id)
    if (existing) existing.push(item)
    else grouped.set(item.media_id, [item])
  }
  return grouped
}

function mediaSearchText(item: APIMedia) {
  return [
    item.title,
    item.year ? String(item.year) : "",
    ...(item.identities ?? []).flatMap((identity) => [
      identity.namespace,
      identity.value,
      `${identity.namespace} ${identity.value}`,
    ]),
  ].join(" ").toLocaleLowerCase()
}

export function buildMediaLibraryRecords(
  kind: APIMedia["kind"],
  media: APIMedia[],
  files: APIManagedFile[],
  wanted: APIWantedItem[],
  workflows: APIWorkflow[],
) {
  const filesByMediaID = groupByMediaID(files)
  const wantedByMediaID = groupByMediaID(wanted)
  const workflowsByMediaID = groupByMediaID(workflows)

  return media
    .filter((item) => item.kind === kind)
    .map<MediaLibraryRecord>((item) => {
      const itemFiles = filesByMediaID.get(item.id) ?? []
      const itemWanted = wantedByMediaID.get(item.id) ?? []
      const itemWorkflows = workflowsByMediaID.get(item.id) ?? []
      return {
        id: item.id,
        kind: item.kind,
        title: item.title,
        year: item.year,
        identities: item.identities ?? [],
        metadata: item.metadata,
        files: itemFiles,
        wanted: itemWanted,
        workflows: itemWorkflows,
        profiles: uniqueValues([
          ...itemFiles.map((file) => file.quality_profile),
          ...itemWanted.map((entry) => entry.target_profile),
        ]),
        roots: uniqueValues(itemFiles.map((file) => file.root_name)),
        resolutions: uniqueValues(itemFiles.flatMap((file) => factValues(file, "resolution"))),
        audioCodecs: uniqueValues(itemFiles.flatMap((file) => factValues(file, "audio_codecs"))),
        languages: uniqueValues(itemFiles.flatMap((file) => [
          ...factValues(file, "audio_languages"),
          ...factValues(file, "subtitle_languages"),
        ])),
        dynamicRanges: uniqueValues(itemFiles.flatMap((file) => [
          ...factValues(file, "dynamic_range"),
          ...factValues(file, "hdr_format"),
        ])),
        available: itemFiles.some((file) => file.state === "current" && file.plex_confirmed),
        missing: itemWanted.some((entry) => entry.gap === "missing"),
        monitoring: itemWorkflows.some((workflow) => workflow.state === "monitored"),
        upgradable: itemWanted.some((entry) => entry.gap === "upgrade"),
        searchText: mediaSearchText(item),
      }
    })
    .sort((left, right) => left.title.localeCompare(right.title) || (left.year ?? 0) - (right.year ?? 0))
}

function matchesMediaSummary(item: MediaLibraryRecord, summary: MediaLibrarySummary) {
  if (summary === "available") return item.available
  if (summary === "missing") return item.missing
  if (summary === "monitoring") return item.monitoring
  if (summary === "upgradable") return item.upgradable
  return true
}

export function filterMediaLibraryRecords(items: MediaLibraryRecord[], filters: MediaLibraryFilters) {
  const query = filters.query.trim().toLocaleLowerCase()
  return items.filter((item) => (!query || item.searchText.includes(query))
    && matchesMediaSummary(item, filters.summary)
    && matchesMediaSummary(item, filters.availability)
    && (!filters.quality || item.profiles.includes(filters.quality))
    && (!filters.resolution || item.resolutions.includes(filters.resolution))
    && (!filters.audio || item.audioCodecs.includes(filters.audio))
    && (!filters.language || item.languages.includes(filters.language))
    && (!filters.root || item.roots.includes(filters.root))
    && (!filters.plexConfirmedOnly || item.files.some((file) => file.plex_confirmed))
    && (!filters.managedOnly || item.files.length > 0))
}
