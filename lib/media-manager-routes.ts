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
  return /^\/media\/(movies|series)\/[0-9a-f-]{36}$/i.test(normalized)
    || /^\/files\/[0-9a-f-]{36}$/i.test(normalized)
    || /^\/sources\/indexers\/[0-9a-f-]{36}$/i.test(normalized)
}
