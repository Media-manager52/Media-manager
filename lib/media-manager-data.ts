export type StatusTone = "good" | "info" | "warn" | "bad" | "neutral"

export type MediaRecord = {
  id: string
  kind: "Movie" | "Series"
  title: string
  year: number
  poster: string
  state: "Available" | "Monitoring" | "Wanted" | "Importing" | "Cataloged"
  profile: string
  root: string
  quality: string
  externalIds: string[]
}

export type QueueRecord = {
  id: string
  title: string
  kind: "Movie" | "Season pack" | "Episode"
  client: string
  stage: string
  progress: number
  state: "Streaming ready" | "Processing" | "Queued" | "Needs attention"
  eta: string
}

export type ReleaseCandidate = {
  id: string
  title: string
  indexer: string
  age: string
  size: string
  score: number
  decision: "Eligible" | "Held" | "Rejected"
  facts: string[]
  reason: string
}

export const mediaRecords: MediaRecord[] = [
  {
    id: "mov_dune_part_two",
    kind: "Movie",
    title: "Dune: Part Two",
    year: 2024,
    poster: "/media/dune.svg",
    state: "Available",
    profile: "UHD Reference",
    root: "Movies · Primary",
    quality: "2160p · Dolby Vision · Atmos",
    externalIds: ["TMDB 693134", "IMDb tt15239678"],
  },
  {
    id: "ser_foundation",
    kind: "Series",
    title: "Foundation",
    year: 2021,
    poster: "/media/foundation.svg",
    state: "Monitoring",
    profile: "TV UHD",
    root: "TV · Primary",
    quality: "S01–S02 complete · S03 monitored",
    externalIds: ["TVDB 366972", "TMDB 93740"],
  },
  {
    id: "mov_blade_runner_2049",
    kind: "Movie",
    title: "Blade Runner 2049",
    year: 2017,
    poster: "/media/blade.svg",
    state: "Wanted",
    profile: "UHD Reference",
    root: "Movies · Primary",
    quality: "1080p · upgrade wanted",
    externalIds: ["TMDB 335984", "IMDb tt1856101"],
  },
  {
    id: "mov_arrival",
    kind: "Movie",
    title: "Arrival",
    year: 2016,
    poster: "/media/arrival.svg",
    state: "Available",
    profile: "UHD Balanced",
    root: "Movies · Archive",
    quality: "2160p · HDR10 · TrueHD",
    externalIds: ["TMDB 329865", "IMDb tt2543164"],
  },
  {
    id: "ser_fallout",
    kind: "Series",
    title: "Fallout",
    year: 2024,
    poster: "/media/fallout.svg",
    state: "Available",
    profile: "TV UHD",
    root: "TV · Primary",
    quality: "Season 01 complete",
    externalIds: ["TVDB 416744", "TMDB 106379"],
  },
  {
    id: "ser_andor",
    kind: "Series",
    title: "Andor",
    year: 2022,
    poster: "/media/andor.svg",
    state: "Importing",
    profile: "TV UHD",
    root: "TV · Primary",
    quality: "Season 02 pack importing",
    externalIds: ["TVDB 393189", "TMDB 83867"],
  },
]

export const queueRecords: QueueRecord[] = [
  {
    id: "job_andor_s02",
    title: "Andor (2022) · Season 02 complete pack",
    kind: "Season pack",
    client: "AltMount",
    stage: "Verifying 12 of 12 episode mappings",
    progress: 84,
    state: "Streaming ready",
    eta: "2m",
  },
  {
    id: "job_blade_upgrade",
    title: "Blade Runner 2049 (2017)",
    kind: "Movie",
    client: "AltMount",
    stage: "Waiting for queue worker",
    progress: 0,
    state: "Queued",
    eta: "—",
  },
  {
    id: "job_import_scan",
    title: "Existing Movies · adoption batch",
    kind: "Movie",
    client: "Local import",
    stage: "Review required for 3 unknown companions",
    progress: 67,
    state: "Needs attention",
    eta: "Paused",
  },
]

export const releaseCandidates: ReleaseCandidate[] = [
  {
    id: "rel_uhd_reference",
    title: "Blade.Runner.2049.2017.2160p.UHD.BluRay.REMUX.DV.HDR.TrueHD.Atmos-GROUP",
    indexer: "Treasure Maps",
    age: "18m",
    size: "72.4 GB",
    score: 96,
    decision: "Eligible",
    facts: ["2160p", "UHD BluRay", "REMUX", "Dolby Vision", "TrueHD Atmos", "Danish subtitles"],
    reason: "All hard requirements pass; highest preferred-format score and verified Danish full subtitles.",
  },
  {
    id: "rel_web_compact",
    title: "Blade.Runner.2049.2017.2160p.WEB-DL.DDP5.1.HDR.H265-GROUP2",
    indexer: "Nordic Index",
    age: "2h",
    size: "24.1 GB",
    score: 74,
    decision: "Eligible",
    facts: ["2160p", "WEB-DL", "HDR10", "HEVC", "EAC3 5.1"],
    reason: "Eligible, but loses to the REMUX on source, audio, and preferred-format score.",
  },
  {
    id: "rel_unknown_audio",
    title: "Blade.Runner.2049.2017.MULTi.2160p.WEB.H265-GROUP3",
    indexer: "Archive NZB",
    age: "1d",
    size: "19.8 GB",
    score: 42,
    decision: "Held",
    facts: ["2160p", "WEB", "Audio unknown", "Subtitle status unknown"],
    reason: "Required language and subtitle facts are unknown. Unknown evidence never becomes a pass.",
  },
  {
    id: "rel_wrong_resolution",
    title: "Blade.Runner.2049.2017.1080p.BluRay.DTS-GROUP4",
    indexer: "Treasure Maps",
    age: "3d",
    size: "18.7 GB",
    score: 0,
    decision: "Rejected",
    facts: ["1080p", "BluRay", "DTS"],
    reason: "Rejected by the UHD minimum-resolution requirement before preference scoring.",
  },
]

export const dashboardMetrics = [
  { label: "Library", value: "642", note: "381 movies · 261 series", tone: "info" as const },
  { label: "Wanted", value: "49", note: "14 missing · 35 upgrades", tone: "warn" as const },
  { label: "Active work", value: "3", note: "2 queue jobs · 1 import review", tone: "good" as const },
  { label: "Evidence coverage", value: "92.4%", note: "Recent eligible candidates", tone: "info" as const },
]

export const routeTitles: Record<string, { area: string; title: string; subtitle: string }> = {
  "/": { area: "Home", title: "Overview", subtitle: "The operational state of your library, automation, and release intelligence." },
  "/media": { area: "Library", title: "Media", subtitle: "Browse, filter, monitor, and manage every movie and series." },
  "/files": { area: "Library", title: "File Manager", subtitle: "Inspect real files, virtual files, symlinks, provenance, and safe operations." },
  "/collections": { area: "Library", title: "Collections", subtitle: "Franchises, smart rules, and custom media groupings." },
  "/calendar": { area: "Library", title: "Calendar", subtitle: "Future releases and episodes that Media Manager is monitoring." },
  "/wanted": { area: "Library", title: "Wanted & Upgrades", subtitle: "Missing media and independent quality, language, audio, and subtitle goals." },
  "/import": { area: "Library", title: "Import", subtitle: "Build a reviewed, reversible plan before adopting or publishing files." },
  "/activity/queue": { area: "Activity", title: "Queue", subtitle: "Live work across AltMount, import, verification, and Plex publication." },
  "/activity/recycle": { area: "Activity", title: "Recycle", subtitle: "Restore managed links during the 30-day retention window; AltMount source files remain untouched." },
  "/activity/history": { area: "Activity", title: "History", subtitle: "Durable grabs, imports, upgrades, failures, and file operations." },
  "/activity/blocklist": { area: "Activity", title: "Blocklist", subtitle: "Bad releases are isolated without damaging unrelated provider health." },
  "/discover": { area: "Intelligence", title: "Discover", subtitle: "Search metadata, verify identity, and choose monitoring intent before adding." },
  "/intelligence/releases": { area: "Intelligence", title: "Release Intelligence", subtitle: "Compare normalized candidates, evidence, policy results, and ranking." },
  "/intelligence/groups": { area: "Intelligence", title: "Release Groups", subtitle: "Observed behavior and evidence—never permanent or absolute trust." },
  "/intelligence/evidence": { area: "Intelligence", title: "Evidence Explorer", subtitle: "Inspect claims, sources, confidence, contradictions, and provenance." },
  "/sources/indexers": { area: "Sources", title: "Indexers", subtitle: "Health, learned coverage, quotas, pacing, and evidence value." },
  "/sources/indexer-lab": { area: "Sources", title: "Indexer Lab", subtitle: "Commission and map one indexer safely before runtime use." },
  "/sources/download-clients": { area: "Sources", title: "Download Clients", subtitle: "Capability-based clients with AltMount as the first-class Usenet path." },
  "/sources/import-lists": { area: "Sources", title: "Import Lists", subtitle: "Bring external watchlists into monitoring without treating them as evidence." },
  "/settings/profiles": { area: "Settings", title: "Profiles & Policies", subtitle: "Hard requirements, preferences, scoring, cutoffs, and upgrade rules." },
  "/settings/metadata": { area: "Settings", title: "Metadata & Artwork", subtitle: "Catalog providers, artwork choices, locks, and sidecar output." },
  "/settings/media-management": { area: "Settings", title: "Media Management", subtitle: "File lifecycle, upgrades, companions, recycling, and permissions." },
  "/settings/storage": { area: "Settings", title: "Storage & Naming", subtitle: "Multiple roots, capacity, path mappings, symlinks, and Plex-safe names." },
  "/settings/connections": { area: "Settings", title: "Connections", subtitle: "Plex, Seerr, and webhooks—separate from indexers and clients." },
  "/settings/notifications": { area: "Settings", title: "Notifications", subtitle: "Route selected operational and media events to destinations." },
  "/system/tasks": { area: "System", title: "Tasks", subtitle: "Scheduled work, latest execution, duration, state, and manual runs." },
  "/system/backups": { area: "System", title: "Backups", subtitle: "Backup policy, retained copies, restore plans, and restore verification." },
  "/system/security": { area: "System", title: "Security", subtitle: "Owner access, secrets, hostile-input boundaries, and outbound controls." },
  "/system/api": { area: "System", title: "API", subtitle: "Scoped machine keys, auditability, limits, and API exploration." },
  "/system/health": { area: "System", title: "Health", subtitle: "Granular component health without false whole-system outages." },
  "/system/logs": { area: "System", title: "Logs & Audit", subtitle: "Application diagnostics and the durable write audit." },
}

export function normalizeRoute(path: string) {
  if (path.startsWith("/media/movies/")) return "/media/movies/$mediaId"
  if (path.startsWith("/media/series/")) return "/media/series/$mediaId"
  if (path.startsWith("/files/")) return "/files/$fileId"
  if (path.startsWith("/sources/indexers/")) return "/sources/indexers/$indexerId"
  return path.length > 1 ? path.replace(/\/$/, "") : path
}

export function isKnownRoute(path: string) {
  const normalized = path.length > 1 ? path.replace(/\/$/, "") : path
  if (routeTitles[normalized]) return true
  const movieMatch = normalized.match(/^\/media\/movies\/([^/]+)$/)
  if (movieMatch) return mediaRecords.some((item) => item.kind === "Movie" && item.id === movieMatch[1]) || process.env.NEXT_PUBLIC_MEDIA_MANAGER_MODE === "live" && /^[0-9a-f-]{36}$/i.test(movieMatch[1])
  const seriesMatch = normalized.match(/^\/media\/series\/([^/]+)$/)
  if (seriesMatch) return mediaRecords.some((item) => item.kind === "Series" && item.id === seriesMatch[1]) || process.env.NEXT_PUBLIC_MEDIA_MANAGER_MODE === "live" && /^[0-9a-f-]{36}$/i.test(seriesMatch[1])
  if (/^\/files\/(file_blade_2049|file_dune_part_two|file_foundation_s03e01|file_unknown_companion)$/.test(normalized)) return true
  if (process.env.NEXT_PUBLIC_MEDIA_MANAGER_MODE === "live" && /^\/files\/[0-9a-f-]{36}$/i.test(normalized)) return true
  if (normalized === "/sources/indexers/treasure-maps") return true
  if (process.env.NEXT_PUBLIC_MEDIA_MANAGER_MODE === "live" && /^\/sources\/indexers\/[0-9a-f-]{36}$/i.test(normalized)) return true
  return false
}
