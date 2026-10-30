"use client"

import * as React from "react"
import { LIVE_REFRESH_MS, useAutoRefresh } from "@/hooks/use-auto-refresh"
import { usePagedList } from "@/hooks/use-paged-list"
import Link from "next/link"
import { toast } from "sonner"
import {
  Activity,
  AlertTriangle,
  ChevronLeft,
  ChevronRight,
  CheckCircle2,
  Database,
  DatabaseBackup,
  Download,
  ExternalLink,
  FileCheck2,
  Files,
  Film,
  Gauge,
  Grid2X2,
  Globe2,
  HardDrive,
  List,
  ListChecks,
  Plus,
  RefreshCw,
  Search,
  SearchCheck,
  ShieldCheck,
  SlidersHorizontal,
  Tv,
  X,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  createReleaseBlock,
  createNotificationDestination,
  createImportList,
  createBackup,
  deleteReleaseBlock,
  loadActivity,
  loadCalendar,
  loadCatalogMedia,
  loadManagedFile,
  loadManagedFilesPage,
  loadRequestsPage,
  loadWantedPage,
  loadNotificationDeliveries,
  loadNotificationDestinations,
  loadImportList,
  loadImportLists,
  loadBackups,
  loadReleaseCandidates,
  loadReleaseBlocks,
  loadRequest,
  loadSources,
  loadSystemHealth,
  loadSystemInfo,
  loadTasks,
  loadWorkflow,
  loadWorkflows,
  loadWorkflowsPage,
  refreshMediaMetadata,
  retryBackupOffsite,
  setNotificationDestinationEnabled,
  uploadImportListSnapshot,
  type APIActivity,
  type APICalendarEntry,
  type APIReleaseCandidate,
  type APISourceStatus,
  type APISystemHealth,
  type APISystemInfo,
  type APITasks,
  type APIImportList,
  type APIManagedFile,
  type APIRequest,
  type APIRequestDetail,
  type APIWantedItem,
  type APIWorkflow,
} from "@/lib/media-manager-api"
import { cn } from "@/lib/utils"
import {
  buildMediaLibraryRecords,
  filterMediaLibraryRecords,
  initialMediaFilters,
  uniqueValues,
  type MediaLibraryFilters,
  type MediaLibraryRecord,
  type MediaLibrarySummary,
} from "@/lib/media-library"
import { filterRequests, requestAttentionStates, requestFilterCounts, requestLifecycleLabel, requestLifecycleTone, requestProgressMessage, seerrStateLabel, type RequestFilter } from "@/lib/media-manager-requests"
import { parseSeriesVariant, rollupSeriesSeasons, type SeriesSeasonRollup } from "@/lib/series-variants"
import { AttentionCard, attentionEntries, ConsoleRow, DownloadProgress, mediaHref, MiniPoster, RequestDecisionButtons, requestAwaitingApproval, StageStepper, titleHue } from "./console"
import { retryPlan } from "@/lib/workflow-presentation"
import { isActivelyAcquiring } from "@/lib/workflow-states"
import { MetadataArtwork, MetadataSourceLink } from "./metadata-artwork"
import { InlineNotice, KeyValue, MetricCard, Panel, SectionLabel, StateBadge, StatusChip } from "./ui"

export { buildMediaLibraryRecords, filterMediaLibraryRecords } from "@/lib/media-library"
export type { MediaLibraryFilters, MediaLibraryRecord } from "@/lib/media-library"

type Tone = "good" | "info" | "warn" | "bad" | "neutral"

/**
 * Loads persisted state for a screen. With `autoRefresh`, read-only screens
 * re-read it on the shared interval while the page is visible.
 */
export function useLiveData<T>(loader: (signal: AbortSignal) => Promise<T>, options?: { autoRefresh?: boolean }) {
  const [data, setData] = React.useState<T | null>(null)
  const [error, setError] = React.useState("")
  const [revision, setRevision] = React.useState(0)
  useAutoRefresh(() => setRevision((current) => current + 1), LIVE_REFRESH_MS, options?.autoRefresh ?? false)
  React.useEffect(() => {
    const controller = new AbortController()
    loader(controller.signal)
      .then((next) => { setData(next); setError("") })
      .catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Could not load live data") })
    return () => controller.abort()
  }, [loader, revision])
  return { data, error, refresh: () => setRevision((current) => current + 1) }
}

export function LoadingPanel({ label = "Loading persisted state…" }: { label?: string }) {
  return <Panel><div className="py-12 text-center text-[12px] text-fg-subtle">{label}</div></Panel>
}

export function ErrorNotice({ error }: { error: string }) {
  return <InlineNotice tone="bad" title="Live data unavailable">{error}</InlineNotice>
}

export function RefreshButton({ onClick }: { onClick: () => void }) {
  return <Button variant="outline" size="sm" onClick={onClick}><RefreshCw /> Refresh</Button>
}

export function pretty(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase())
}

export function timeAgo(value: string) {
  const seconds = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 1000))
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
  return `${Math.floor(seconds / 86400)}d ago`
}

function fileTone(state: string, confirmed: boolean): Tone {
  if (state === "current" && confirmed) return "good"
  if (["planned", "publishing", "verified"].includes(state)) return "info"
  if (["quarantined"].includes(state)) return "warn"
  return "neutral"
}

function verdictTone(verdict: string, selected: boolean): Tone {
  if (selected) return "good"
  if (verdict === "eligible") return "info"
  if (verdict === "held") return "warn"
  if (verdict === "rejected") return "bad"
  return "neutral"
}

function formatBytes(value?: number) {
  if (value === undefined || value < 0) return "—"
  const units = ["B", "KiB", "MiB", "GiB", "TiB"]
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit += 1 }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`
}

function qualityFact(evidence: Record<string, unknown>, key: string) {
  const nested = evidence.facts
  const facts = nested && typeof nested === "object" && !Array.isArray(nested) ? nested as Record<string, unknown> : evidence
  const value = facts[key]
  if (typeof value === "string" || typeof value === "number") return String(value)
  if (Array.isArray(value)) return value.filter((item): item is string => typeof item === "string").join(", ")
  return ""
}

const activityLoader = (signal: AbortSignal) => loadActivity(signal)
const calendarLoader = (signal: AbortSignal) => loadCalendar(signal)
const filesPageLoader = (offset: number, limit: number, signal: AbortSignal) => loadManagedFilesPage({ offset, limit, signal })
const wantedPageLoader = (offset: number, limit: number, signal: AbortSignal) => loadWantedPage({ offset, limit, signal })
const requestsPageLoader = (offset: number, limit: number, signal: AbortSignal) => loadRequestsPage({ offset, limit, signal })
const releasesLoader = (signal: AbortSignal) => loadReleaseCandidates(signal)
const blocksLoader = (signal: AbortSignal) => loadReleaseBlocks(signal)
const sourcesLoader = (signal: AbortSignal) => loadSources(signal)
const healthLoader = (signal: AbortSignal) => loadSystemHealth(signal)
const tasksLoader = (signal: AbortSignal) => loadTasks(signal)
const systemInfoLoader = (signal: AbortSignal) => loadSystemInfo(signal)
const notificationDestinationsLoader = (signal: AbortSignal) => loadNotificationDestinations(signal)
const notificationDeliveriesLoader = (signal: AbortSignal) => loadNotificationDeliveries(signal)
const importListsLoader = (signal: AbortSignal) => loadImportLists(signal)
const backupsLoader = (signal: AbortSignal) => loadBackups(signal)

// The overview groups persisted workflow states truthfully. Real acquisition
// work is "actively acquiring"; normal monitoring, pacing, and future air dates
// are "waiting / monitored". Neither group is allowed to absorb the other.
function attentionReason(item: { attention_detail?: string; current_state?: string }) {
  return item.attention_detail?.trim() || "Media Manager paused this workstream for an owner decision."
}

/**
 * A single owner-action surface used by the overview and Wanted. It shows the
 * exact persisted reason, whether automatic retry exists, and the safest next
 * step, rather than exposing raw audit events.
 */
export function OwnerAttention({ items, workflows, limit }: { items: APIWantedItem[]; workflows: APIWorkflow[]; limit?: number }) {
  const attentionItems = items.filter((item) => item.owner_action_required)
  const attentionWorkflows = workflows.filter((workflow) => (workflow.owner_action_required || ["needs_attention", "blocked", "quarantined", "transport_unknown"].includes(workflow.state)) && !attentionItems.some((item) => item.workflow_id === workflow.id))
  const total = attentionItems.length + attentionWorkflows.length
  if (total === 0) return <div className="flex items-center gap-2.5 rounded-lg border border-good-line bg-good-soft px-3.5 py-3 text-[13px] text-good"><CheckCircle2 className="size-4" aria-hidden />Nothing needs your attention. Waiting and searching continue automatically.</div>
  const shownItems = limit ? attentionItems.slice(0, limit) : attentionItems
  const shownWorkflows = limit ? attentionWorkflows.slice(0, Math.max(0, limit - shownItems.length)) : attentionWorkflows
  const hidden = total - shownItems.length - shownWorkflows.length
  return <div className="space-y-2.5">
    {shownItems.map((item) => {
      const workflow = workflows.find((entry) => entry.id === item.workflow_id)
      const href = item.media_kind === "movie" ? `/media/movies/${item.media_id}` : `/media/series/${item.media_id}`
      const retry = workflow?.retry_discovery_state === "ready" ? "A new eligible release is ready for your approval" : workflow?.next_rescan_at ? `Next paced discovery ${new Date(workflow.next_rescan_at).toLocaleString()}` : workflow?.retry_discovery_state === "waiting" ? "Automatic discovery is waiting for a new eligible release" : "No automatic retry is scheduled"
      return <div key={item.workflow_id} className="rounded-xl border border-warn-line bg-warn-soft p-4">
        <div className="flex flex-wrap items-center justify-between gap-2"><Link href={href} className="text-[14px] font-semibold text-fg hover:underline hover:underline-offset-2">{item.title}{item.year ? ` (${item.year})` : ""}</Link><StateBadge state={item.current_state || "needs_attention"} /></div>
        <div className="mt-1.5 text-[13px] leading-5 text-warn">{attentionReason(item)}</div>
        <div className="mt-2.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px] text-fg-secondary">
          <span>Scope {item.variant}</span>
          <span>Target {item.target_profile}</span>
          <span>{retry}</span>
        </div>
        {workflow?.ready_release_title ? <div className="mt-2 truncate text-[12.5px] text-fg-secondary">Candidate: {workflow.ready_release_title}{workflow.ready_indexer_name ? ` · ${workflow.ready_indexer_name}` : ""}</div> : null}
        <div className="mt-3 flex flex-wrap gap-2"><Button asChild size="sm" variant="outline"><Link href={href}>Review media</Link></Button><Button asChild size="sm" variant="ghost"><Link href="/activity/queue">Owner actions</Link></Button></div>
      </div>
    })}
    {shownWorkflows.map((workflow) => {
      const href = workflow.media_kind === "movie" ? `/media/movies/${workflow.media_id}` : `/media/series/${workflow.media_id}`
      const retry = workflow.retry_discovery_state === "ready" ? "A new eligible release is ready for approval" : workflow.next_rescan_at ? `Next paced discovery ${new Date(workflow.next_rescan_at).toLocaleString()}` : "No automatic retry is scheduled"
      return <div key={workflow.id} className="rounded-xl border border-warn-line bg-warn-soft p-4">
        <div className="flex flex-wrap items-center justify-between gap-2"><Link href={href} className="text-[14px] font-semibold text-fg hover:underline hover:underline-offset-2">{workflow.title}{workflow.year ? ` (${workflow.year})` : ""}</Link><StateBadge state={workflow.state} /></div>
        <div className="mt-1.5 text-[13px] leading-5 text-warn">{workflow.attention_detail || pretty(workflow.attention_code ?? workflow.state)}</div>
        <div className="mt-2.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px] text-fg-secondary"><span>Scope {workflow.variant}</span>{workflow.ready_release_title ? <span>Candidate {workflow.ready_release_title}</span> : null}<span>{retry}</span></div>
        <div className="mt-3"><Button asChild size="sm" variant="outline"><Link href="/activity/queue">Open owner actions</Link></Button></div>
      </div>
    })}
    {hidden > 0 ? <div className="flex flex-wrap items-center justify-between gap-2 rounded-[10px] border border-line-strong bg-surface-2 px-3.5 py-2.5 text-[13px] text-fg-muted"><span>Showing {total - hidden} of {total} items that need attention.</span><Button asChild size="sm" variant="ghost"><Link href="/wanted">View all in Wanted</Link></Button></div> : null}
  </div>
}

type OverviewQueueItem = { id: string; title: string; year?: number; variant: string; state: string; ownerActionRequired: boolean; updatedAt: string; href: string }

/**
 * The overview binds real workflow state to the bounded wanted read model.
 * Recent workflows alone are too small to show waiting work, so a wanted row
 * without a workflow row still participates in the acquiring / waiting groups.
 */
export function overviewQueueItems(workflows: APIWorkflow[], wanted: APIWantedItem[]): OverviewQueueItem[] {
  const seen = new Set<string>()
  const items: OverviewQueueItem[] = []
  for (const workflow of workflows) {
    if (seen.has(workflow.id)) continue
    seen.add(workflow.id)
    items.push({ id: workflow.id, title: workflow.title, year: workflow.year, variant: workflow.variant, state: workflow.state, ownerActionRequired: Boolean(workflow.owner_action_required), updatedAt: workflow.updated_at, href: workflow.media_kind === "movie" ? `/media/movies/${workflow.media_id}` : `/media/series/${workflow.media_id}` })
  }
  for (const item of wanted) {
    if (seen.has(item.workflow_id)) continue
    seen.add(item.workflow_id)
    items.push({ id: item.workflow_id, title: item.title, year: item.year, variant: item.variant, state: item.current_state || "waiting", ownerActionRequired: Boolean(item.owner_action_required), updatedAt: item.updated_at, href: item.media_kind === "movie" ? `/media/movies/${item.media_id}` : `/media/series/${item.media_id}` })
  }
  return items
}

async function loadAllLibraryPages<T>(loadPage: (offset: number, limit: number, signal: AbortSignal) => Promise<import("@/lib/media-manager-api").Page<T>>, signal: AbortSignal): Promise<T[]> {
  const pageSize = 500
  const maxPages = 20
  const items: T[] = []
  for (let page = 0; page < maxPages; page += 1) {
    const result = await loadPage(page * pageSize, pageSize, signal)
    items.push(...result.items)
    if (!result.hasMore) return items
  }
  throw new Error(`Library associations exceeded the safe ${pageSize * maxPages}-record load limit`)
}

async function loadMediaLibrary(kind: "movie" | "series", signal: AbortSignal) {
  const [media, files, wanted, workflows] = await Promise.all([
    loadCatalogMedia(signal),
    loadAllLibraryPages<APIManagedFile>((offset, limit, pageSignal) => loadManagedFilesPage({ offset, limit, signal: pageSignal }), signal),
    loadAllLibraryPages<APIWantedItem>((offset, limit, pageSignal) => loadWantedPage({ offset, limit, signal: pageSignal }), signal),
    loadAllLibraryPages<APIWorkflow>((offset, limit, pageSignal) => loadWorkflowsPage({ active: false, offset, limit, signal: pageSignal }), signal),
  ])
  return buildMediaLibraryRecords(kind, media, files, wanted, workflows)
}

export const movieLibraryLoader = (signal: AbortSignal) => loadMediaLibrary("movie", signal)
export const seriesLibraryLoader = (signal: AbortSignal) => loadMediaLibrary("series", signal)

function resolutionLabel(value: string) {
  return value.toLocaleLowerCase() === "2160p" ? "4K" : value
}

function compactValues(values: string[], limit = 6) {
  if (values.length <= limit) return values.join(", ")
  return `${values.slice(0, limit).join(", ")} +${values.length - limit}`
}

function mediaAvailability(item: MediaLibraryRecord) {
  if (item.kind === "series" && item.available && item.missing) return { label: "Partially available", short: "Partial", tone: "text-warn", dot: "bg-warn" }
  if (item.available && item.upgradable) return { label: "Available · upgrade wanted", short: "Upgrade wanted", tone: "text-good", dot: "bg-good" }
  if (item.available) return { label: "Available", short: "Available", tone: "text-good", dot: "bg-good" }
  if (item.missing) return { label: "Missing", short: "Missing", tone: "text-warn", dot: "bg-warn" }
  if (item.monitoring) return { label: "Monitoring", short: "Monitoring", tone: "text-brand-text", dot: "bg-brand" }
  return { label: "Cataloged", short: "Cataloged", tone: "text-fg-muted", dot: "bg-fg-subtle" }
}

const libraryCopy = {
  movie: { title: "Movies", all: "All Movies", singular: "movie", plural: "movies", add: "Add Movie" },
  series: { title: "TV Shows", all: "All TV Shows", singular: "TV show", plural: "TV shows", add: "Add TV Show" },
} as const

// A page must fill the real display without rendering hundreds of cards. The
// poster grid uses auto-fill columns, so the page size scales modestly with the
// viewport instead of hard-coding a single desktop breakpoint.
const basePageSize = 24

function densityPageSize(width: number) {
  if (width >= 3200) return 72
  if (width >= 2200) return 48
  return basePageSize
}

export function LiveLibraryScreen({ mediaKind = "movie" }: { mediaKind?: "movie" | "series" }) {
  const copy = libraryCopy[mediaKind]
  const loader = mediaKind === "movie" ? movieLibraryLoader : seriesLibraryLoader
  const [filters, setFilters] = React.useState<MediaLibraryFilters>(initialMediaFilters)
  const [view, setView] = React.useState<"grid" | "list">("grid")
  const [density, setDensity] = React.useState<"comfortable" | "compact">("compact")
  const [moreOpen, setMoreOpen] = React.useState(false)
  const [page, setPage] = React.useState(1)
  const [pageSize, setPageSize] = React.useState(basePageSize)
  const [selectedID, setSelectedID] = React.useState<string | null | undefined>(undefined)
  const [detailTab, setDetailTab] = React.useState("overview")
  const { data, error, refresh } = useLiveData(loader, { autoRefresh: true })
  React.useEffect(() => {
    function update() { setPageSize(densityPageSize(window.innerWidth)) }
    update()
    window.addEventListener("resize", update)
    return () => window.removeEventListener("resize", update)
  }, [])
  const records = React.useMemo(() => data ?? [], [data])
  const filtered = React.useMemo(() => filterMediaLibraryRecords(records, filters), [records, filters])
  const facets = React.useMemo(() => ({
    quality: uniqueValues(records.flatMap((item) => item.profiles)),
    resolution: uniqueValues(records.flatMap((item) => item.resolutions)),
    audio: uniqueValues(records.flatMap((item) => item.audioCodecs)),
    language: uniqueValues(records.flatMap((item) => item.languages)),
    root: uniqueValues(records.flatMap((item) => item.roots)),
  }), [records])
  const counts = React.useMemo(() => ({
    all: records.length,
    available: records.filter((item) => item.available).length,
    missing: records.filter((item) => item.missing).length,
    monitoring: records.filter((item) => item.monitoring).length,
    upgradable: records.filter((item) => item.upgradable).length,
  }), [records])
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize))
  const currentPage = Math.min(page, pageCount)
  const visible = filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  const requestedSelection = selectedID === undefined ? filtered[0] : records.find((item) => item.id === selectedID)
  const selected = selectedID === null ? null : requestedSelection && filtered.some((item) => item.id === requestedSelection.id) ? requestedSelection : filtered[0] ?? null

  function setFilter<K extends keyof MediaLibraryFilters>(key: K, value: MediaLibraryFilters[K]) {
    setFilters((current) => ({ ...current, [key]: value }))
    setPage(1)
    setDetailTab("overview")
  }

  function selectMedia(id: string) {
    setSelectedID(id)
    setDetailTab("overview")
  }

  function clearAllFilters() {
    setFilters(initialMediaFilters)
    setPage(1)
    setDetailTab("overview")
  }

  function clearFilter(key: keyof MediaLibraryFilters) {
    setFilters((current) => ({ ...current, [key]: initialMediaFilters[key] }))
    setPage(1)
    setDetailTab("overview")
  }

  const summaryItems: Array<{ key: MediaLibrarySummary; label: string; count: number }> = [
    { key: "all", label: copy.all, count: counts.all },
    { key: "available", label: "Available", count: counts.available },
    { key: "missing", label: "Missing", count: counts.missing },
    { key: "monitoring", label: "Monitoring", count: counts.monitoring },
    { key: "upgradable", label: "Upgradable", count: counts.upgradable },
  ]

  const activeFilterChips: Array<{ key: keyof MediaLibraryFilters; label: string }> = []
  if (filters.quality) activeFilterChips.push({ key: "quality", label: `Quality: ${pretty(filters.quality)}` })
  if (filters.resolution) activeFilterChips.push({ key: "resolution", label: `Resolution: ${pretty(filters.resolution)}` })
  if (filters.audio) activeFilterChips.push({ key: "audio", label: `Audio: ${pretty(filters.audio)}` })
  if (filters.language) activeFilterChips.push({ key: "language", label: `Language: ${pretty(filters.language)}` })
  if (filters.availability !== "all") activeFilterChips.push({ key: "availability", label: `Availability: ${pretty(filters.availability)}` })
  if (filters.root) activeFilterChips.push({ key: "root", label: `Root: ${filters.root}` })
  if (filters.plexConfirmedOnly) activeFilterChips.push({ key: "plexConfirmedOnly", label: "Plex confirmed" })
  if (filters.managedOnly) activeFilterChips.push({ key: "managedOnly", label: "Has managed file" })

  return <div className={cn("mm-library-root grid h-full min-h-0 bg-canvas", selected && "xl:grid-cols-[minmax(0,1fr)_320px] 2xl:grid-cols-[minmax(0,1fr)_380px]")}>
    <section className="flex min-h-0 min-w-0 flex-col px-4 pb-3 pt-3 sm:px-5 lg:px-9" aria-labelledby={`${mediaKind}-library-title`}>
      <div className="mb-4 flex min-h-9 items-end gap-3">
        <div className="min-w-0">
          <div className="flex items-baseline gap-2">
            <h1 id={`${mediaKind}-library-title`} className="text-[20px] font-bold tracking-[-0.02em] text-fg">{copy.title}</h1>
            <span className="rounded-full border border-line bg-surface-2 px-1.5 text-[11px] font-bold text-fg-muted" aria-label={`${records.length} ${records.length === 1 ? copy.singular : copy.plural}`}>{records.length}</span>
          </div>
          <p className="mt-1 truncate text-[12.5px] text-fg-subtle">{records.length} title{records.length === 1 ? "" : "s"} · {counts.available} available · {counts.missing} missing · {counts.upgradable} upgradable · {counts.monitoring} monitoring</p>
        </div>
        <div className="ml-auto flex items-center gap-1.5">
          <Button variant="ghost" size="icon-sm" aria-label={`Refresh ${copy.plural}`} onClick={refresh}><RefreshCw /></Button>
          <div className="flex rounded-md border border-line bg-surface-2 p-0.5" aria-label="Library view">
            <button type="button" aria-label="Grid view" aria-pressed={view === "grid"} onClick={() => setView("grid")} className={cn("grid size-7 place-items-center rounded-[4px] text-fg-subtle outline-none focus-visible:ring-2 focus-visible:ring-ring", view === "grid" && "bg-brand-soft text-brand-text")}><Grid2X2 className="size-4" /></button>
            <button type="button" aria-label="List view" aria-pressed={view === "list"} onClick={() => setView("list")} className={cn("grid size-7 place-items-center rounded-[4px] text-fg-subtle outline-none focus-visible:ring-2 focus-visible:ring-ring", view === "list" && "bg-brand-soft text-brand-text")}><List className="size-4" /></button>
          </div>
          {view === "grid" ? <label className="hidden shrink-0 sm:block"><span className="sr-only">Grid density</span><NativeSelect size="sm" aria-label="Grid density" value={density} onChange={(event) => setDensity(event.target.value as "comfortable" | "compact")} className="h-8 border-line bg-surface-2 px-2 pr-6 text-[12px] text-fg-secondary shadow-none"><NativeSelectOption value="compact">Compact</NativeSelectOption><NativeSelectOption value="comfortable">Comfortable</NativeSelectOption></NativeSelect></label> : null}
          <Button asChild size="sm" className="h-8 px-2.5 sm:px-3"><Link href="/discover" aria-label={copy.add}><Plus /><span className="hidden sm:inline">{copy.add}</span></Link></Button>
        </div>
      </div>

      <div className="mb-2.5 flex shrink-0 gap-2 overflow-x-auto pb-0.5 scrollbar-none" aria-label={`${copy.title} summaries`}>
        {summaryItems.map((item) => <button key={item.key} type="button" aria-pressed={filters.summary === item.key} onClick={() => setFilter("summary", item.key)} className={cn("h-[30px] shrink-0 rounded-lg border border-line bg-surface px-3 text-[12px] font-semibold text-fg-muted outline-none transition hover:border-line-strong hover:text-fg focus-visible:ring-2 focus-visible:ring-ring", filters.summary === item.key && "border-brand-line bg-brand-soft text-fg")}>{item.label}{" "}<em className={cn("ml-1 font-medium not-italic tabular-nums text-fg-subtle", filters.summary === item.key && "text-brand-text")}>{item.count}</em></button>)}
      </div>

      {error ? <div className="mb-2 flex items-center justify-between gap-3 rounded-md border border-bad-line bg-bad-soft px-3 py-2 text-[12px] text-bad" role="alert"><span>{error}</span><button type="button" className="font-semibold underline underline-offset-2" onClick={refresh}>Retry</button></div> : null}

      <div className="mb-2 shrink-0">
        <div className="flex flex-wrap items-center gap-1.5" aria-label={`${copy.title} filters`}>
          <label className="relative block min-w-[190px] flex-1 sm:max-w-[340px]"><span className="sr-only">Search {copy.plural}</span><Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-fg-muted" /><Input value={filters.query} onChange={(event) => setFilter("query", event.target.value)} placeholder={`Search ${copy.plural}`} className="h-8 border-line bg-surface-2 pl-8 text-[13px] shadow-none placeholder:text-fg-muted focus-visible:border-brand focus-visible:ring-ring/20" /></label>
          <LibrarySelect label="Availability" value={filters.availability} onChange={(value) => setFilter("availability", value as MediaLibrarySummary)} options={["available", "missing", "monitoring", "upgradable"]} className="w-[112px]" />
          <LibrarySelect label="Quality" value={filters.quality} onChange={(value) => setFilter("quality", value)} options={facets.quality} className="w-[120px]" />
          <Button type="button" variant="outline" size="sm" aria-expanded={moreOpen} onClick={() => setMoreOpen((current) => !current)} className="h-8 shrink-0 border-line bg-surface-2 px-2.5 text-[13px] text-fg-secondary shadow-none hover:bg-surface-3"><SlidersHorizontal /> More Filters{activeFilterChips.length ? <span className="ml-1 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-brand px-1 text-[12px] font-semibold text-brand-fg">{activeFilterChips.length}</span> : null}</Button>
          {activeFilterChips.length ? <Button type="button" variant="ghost" size="sm" onClick={clearAllFilters} className="h-8 shrink-0 text-[13px] text-fg-muted hover:text-fg"><X className="size-3.5" /> Clear all</Button> : null}
        </div>
        {activeFilterChips.length ? <div className="mt-2 flex flex-wrap gap-1.5">{activeFilterChips.map((chip) => <button key={chip.key} type="button" onClick={() => clearFilter(chip.key)} className="inline-flex items-center gap-1.5 rounded-full border border-brand-line bg-brand-soft px-2.5 py-1 text-[12px] font-semibold text-brand-text outline-none transition hover:border-brand focus-visible:ring-2 focus-visible:ring-ring">{chip.label}<X className="size-3" aria-hidden /></button>)}</div> : null}
        {moreOpen ? <div className="mt-2 grid gap-2 rounded-lg border border-line-strong bg-surface-2 p-3 sm:grid-cols-2 lg:grid-cols-4">
          <LibrarySelect label="Resolution" value={filters.resolution} onChange={(value) => setFilter("resolution", value)} options={facets.resolution} className="w-full" />
          <LibrarySelect label="Audio" value={filters.audio} onChange={(value) => setFilter("audio", value)} options={facets.audio} className="w-full" />
          <LibrarySelect label="Language" value={filters.language} onChange={(value) => setFilter("language", value)} options={facets.language} className="w-full" />
          <LibrarySelect label="Root Folder" value={filters.root} onChange={(value) => setFilter("root", value)} options={facets.root} className="w-full" />
          <label className="flex cursor-pointer items-center gap-2 rounded px-1 py-1.5 text-[13px] text-fg-secondary hover:text-fg"><input type="checkbox" checked={filters.plexConfirmedOnly} onChange={(event) => setFilter("plexConfirmedOnly", event.target.checked)} className="accent-brand" /> Plex confirmed only</label>
          <label className="flex cursor-pointer items-center gap-2 rounded px-1 py-1.5 text-[13px] text-fg-secondary hover:text-fg"><input type="checkbox" checked={filters.managedOnly} onChange={(event) => setFilter("managedOnly", event.target.checked)} className="accent-brand" /> Has managed file</label>
        </div> : null}
      </div>

      <div className="mb-1.5 flex shrink-0 items-center justify-between text-[13px] text-fg-muted" aria-live="polite"><span>{filtered.length} {filtered.length === 1 ? copy.singular : copy.plural}</span><span>{view === "grid" ? `${density === "compact" ? "Compact" : "Comfortable"} grid` : "Title order"}</span></div>

      <div className="min-h-0 flex-1 overflow-y-auto pr-0.5 scrollbar-thin">
        {!data && !error ? <div className="grid h-full min-h-44 place-items-center rounded-md border border-line bg-surface-2 text-[12px] text-fg-muted" role="status">Loading the live {copy.title.toLocaleLowerCase()} catalog…</div>
          : visible.length === 0 ? <div className="grid h-full min-h-44 place-items-center rounded-md border border-dashed border-line-strong bg-surface-2"><div className="px-6 text-center">{mediaKind === "movie" ? <Film className="mx-auto size-6 text-fg-subtle" /> : <Tv className="mx-auto size-6 text-fg-subtle" />}<div className="mt-2 text-[12px] font-semibold text-fg">{records.length === 0 ? `Your ${copy.singular} catalog is empty` : `No ${copy.plural} matched`}</div><div className="mt-1 text-[12px] text-fg-muted">{records.length === 0 ? `Use ${copy.add} to create a real catalog entry.` : "Change or clear the active filters."}</div></div></div>
          : view === "grid" ? <div className={cn("grid pb-2 pt-1", density === "compact" ? "gap-x-3.5 gap-y-5 [grid-template-columns:repeat(auto-fill,minmax(150px,1fr))]" : "gap-x-4 gap-y-6 [grid-template-columns:repeat(auto-fill,minmax(176px,1fr))]")}>
            {visible.map((item) => <MediaLibraryCard key={item.id} item={item} compact={density === "compact"} selected={item.id === selected?.id} onSelect={selectMedia} />)}
          </div> : <div className="overflow-hidden rounded-xl border border-line bg-surface">
            {visible.map((item) => <MediaLibraryRow key={item.id} item={item} selected={item.id === selected?.id} onSelect={selectMedia} />)}
          </div>}
      </div>

      <div className="sticky bottom-0 z-10 mt-2 flex h-9 shrink-0 items-center justify-between border-t border-line bg-canvas/95 pt-2 backdrop-blur-sm" aria-label={`${copy.title} pagination`}>
        <span className="text-[13px] text-fg-muted">{filtered.length ? `${(currentPage - 1) * pageSize + 1}–${Math.min(currentPage * pageSize, filtered.length)} of ${filtered.length}` : `0 ${copy.plural}`}</span>
        <div className="flex items-center gap-1"><Button type="button" variant="ghost" size="icon-xs" aria-label="Previous page" disabled={currentPage <= 1} onClick={() => setPage(Math.max(1, currentPage - 1))}><ChevronLeft /></Button><span className="min-w-14 text-center text-[13px] font-semibold text-fg-secondary">{currentPage} / {pageCount}</span><Button type="button" variant="ghost" size="icon-xs" aria-label="Next page" disabled={currentPage >= pageCount} onClick={() => setPage(Math.min(pageCount, currentPage + 1))}><ChevronRight /></Button></div>
      </div>
    </section>

    {selected ? <>
      {selectedID !== undefined ? <button type="button" aria-label={`Close ${copy.singular} details`} onClick={() => setSelectedID(null)} className="fixed inset-0 z-40 bg-black/60 xl:hidden" /> : null}
      <MediaInspector key={selected.id} item={selected} tab={detailTab} auto={selectedID === undefined} onTabChange={setDetailTab} onClose={() => setSelectedID(null)} onRefresh={refresh} />
    </> : null}

  </div>
}

function LibrarySelect({ label, value, options, onChange, className }: { label: string; value: string; options: string[]; onChange: (value: string) => void; className?: string }) {
  return <label className="shrink-0"><span className="sr-only">{label}</span><NativeSelect size="sm" value={value} onChange={(event) => onChange(event.target.value)} className={cn("h-8 border-line bg-surface-2 px-2 pr-6 text-[12px] text-fg-secondary shadow-none focus-visible:border-brand focus-visible:ring-ring/20", className)} aria-label={label}><NativeSelectOption value="">{label}</NativeSelectOption>{options.map((option) => <NativeSelectOption key={option} value={option}>{pretty(option)}</NativeSelectOption>)}</NativeSelect></label>
}

export function MediaLibraryCard({ item, compact = false, selected, onSelect }: { item: MediaLibraryRecord; compact?: boolean; selected: boolean; onSelect: (id: string) => void }) {
  const availability = mediaAvailability(item)
  const labels = uniqueValues([...item.resolutions.map(resolutionLabel), ...item.dynamicRanges])
  const poster = item.metadata?.poster_url
  const hue = titleHue(item.title)
  const meta = [labels.join(" · "), item.kind === "series" && item.metadata?.season_count ? `${item.metadata.season_count} season${item.metadata.season_count === 1 ? "" : "s"}` : "", item.profiles[0] ?? ""].filter(Boolean).join(" · ") || "No managed file yet"
  return <button type="button" onClick={() => onSelect(item.id)} aria-label={`${item.title}${item.year ? `, ${item.year}` : ""}. ${availability.label}`} aria-pressed={selected} className="group flex min-w-0 flex-col rounded-[10px] text-left outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-canvas">
    <div className={cn("relative flex aspect-[2/3] w-full items-end overflow-hidden rounded-[10px] border border-line transition duration-200 group-hover:-translate-y-[3px] group-hover:border-brand-line group-hover:shadow-[0_12px_32px_-8px_rgba(0,0,0,0.6)]", selected && "border-brand ring-1 ring-brand-line")} style={poster ? undefined : { background: `linear-gradient(160deg, hsl(${hue} 50% 36%), hsl(${hue} 55% 12%))` }}>
      {poster ? <MetadataArtwork src={poster} fallback={item.kind === "movie" ? "/media/movie-placeholder.svg" : "/media/series-placeholder.svg"} alt="" width={300} height={450} className="absolute inset-0 h-full w-full object-cover" /> : null}
      <div className="pointer-events-none absolute inset-0 bg-[linear-gradient(180deg,rgba(0,0,0,.05)_40%,rgba(0,0,0,.72))]" />
      {item.year ? <span className="absolute left-2.5 top-2 z-[1] text-[10px] font-semibold text-white/80">{item.year}</span> : null}
      {labels[0] ? <span className="absolute right-2 top-2 z-[1] rounded-[5px] border border-white/20 bg-[rgba(10,12,18,.75)] px-1.5 py-0.5 text-[9.5px] font-bold tracking-[0.05em] text-white backdrop-blur-sm">{labels[0]}</span> : null}
      {poster ? null : <b className={cn("relative z-[1] px-2.5 pb-2.5 font-bold leading-tight tracking-[-0.01em] text-white [text-shadow:0_1px_6px_rgba(0,0,0,.8)]", compact ? "text-[12px]" : "text-[13px]")}>{item.title}</b>}
    </div>
    <div className="min-w-0 px-0.5 pt-2">
      <div className={cn("truncate font-semibold tracking-[-0.01em] text-fg", compact ? "text-[12.5px]" : "text-[13px]")} title={item.title}>{item.title}</div>
      <div className="mb-1 mt-px truncate text-[11px] text-fg-subtle">{item.year ? `${item.year} · ` : ""}{meta}</div>
      <div className={cn("flex items-center gap-1.5 text-[11px] font-medium", availability.tone)}><span className={cn("size-1.5 shrink-0 rounded-full", availability.dot)} aria-hidden /><span className="truncate">{availability.short}</span></div>
    </div>
  </button>
}

function MediaLibraryRow({ item, selected, onSelect }: { item: MediaLibraryRecord; selected: boolean; onSelect: (id: string) => void }) {
  const availability = mediaAvailability(item)
  return <button type="button" onClick={() => onSelect(item.id)} aria-label={`${item.title}${item.year ? `, ${item.year}` : ""}. ${availability.label}`} aria-pressed={selected} className={cn("flex w-full items-center gap-3 border-b border-line px-3 py-2.5 text-left outline-none last:border-0 hover:bg-surface-3 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring", selected && "bg-brand-soft")}><MetadataArtwork src={item.metadata?.poster_url} fallback={item.kind === "movie" ? "/media/movie-placeholder.svg" : "/media/series-placeholder.svg"} alt="" width={72} height={96} className="h-14 w-10 shrink-0 rounded-[4px] border border-line-strong object-cover" /><div className="min-w-0 flex-1"><div className="truncate text-[13px] font-semibold text-fg">{item.title} <span className="font-normal text-fg-muted">{item.year ? `(${item.year})` : ""}</span></div><div className="mt-1 truncate text-[12px] text-fg-muted">{item.profiles.join(" · ") || "No managed quality profile"}</div></div><div className={cn("hidden items-center gap-1.5 text-[12px] font-semibold sm:flex", availability.tone)}><span className={cn("size-1.5 rounded-full", availability.dot)} />{availability.label}</div><ChevronRight className="size-4 shrink-0 text-fg-muted" /></button>
}

export function MediaInspector({ item, tab, auto, onTabChange, onClose, onRefresh }: { item: MediaLibraryRecord; tab: string; auto: boolean; onTabChange: (tab: string) => void; onClose: () => void; onRefresh?: () => void }) {
  const availability = mediaAvailability(item)
  const currentFile = [...item.files].sort((left, right) => right.generation - left.generation).find((file) => file.state === "current") ?? item.files[0]
  const qualityProfile = currentFile?.quality_profile ?? item.wanted[0]?.target_profile ?? "Not assigned"
  const canRefreshMetadata = item.identities.some((identity) => identity.state === "verified" && (identity.namespace === "tmdb" || item.kind === "series" && identity.namespace === "tvdb"))
  const [refreshing, setRefreshing] = React.useState(false)
  const seasonEntries = React.useMemo<SeriesSeasonRollup[]>(() => item.kind === "series" ? rollupSeriesSeasons(item.workflows, item.files) : [], [item.kind, item.workflows, item.files])
  async function refreshMetadata() {
    setRefreshing(true)
    try { const result = await refreshMediaMetadata(item.id); const direct = Object.entries(result.provider_states).filter(([, state]) => state === "direct" || state === "via_simkl").map(([provider]) => provider.toUpperCase()); toast.success(direct.length ? `${direct.join(", ")} metadata refreshed` : "Metadata refreshed through the configured fallback"); onRefresh?.() }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not refresh metadata") }
    finally { setRefreshing(false) }
  }
  return <aside aria-label={`Details for ${item.title}`} className={cn("fixed inset-0 z-50 min-h-0 flex-col overflow-hidden border-l border-line bg-surface sm:bottom-0 sm:left-auto sm:top-14 sm:w-[380px] xl:static xl:z-auto xl:flex xl:h-full xl:w-auto", auto ? "hidden" : "flex")}>
    <div className="flex h-11 shrink-0 items-center justify-between border-b border-line px-3.5"><span className="text-[13px] font-semibold text-fg">{item.kind === "movie" ? "Movie" : "TV show"} details</span><Button type="button" variant="ghost" size="icon-xs" aria-label="Close details" onClick={onClose}><X /></Button></div>
    <div className="min-h-0 flex-1 overflow-y-auto scrollbar-thin">
      <div className="border-b border-line p-3.5">
        <div className="flex gap-3">{item.metadata?.poster_url ? <MetadataArtwork src={item.metadata.poster_url} fallback={item.kind === "movie" ? "/media/movie-placeholder.svg" : "/media/series-placeholder.svg"} alt={`${item.title} poster`} width={300} height={450} className="aspect-[2/3] w-[104px] shrink-0 rounded-[7px] border border-line-strong object-cover" /> : <span className="flex aspect-[2/3] w-[104px] shrink-0 items-end rounded-[7px] border border-line-strong p-2 text-[12px] font-bold leading-tight text-white [text-shadow:0_1px_6px_rgba(0,0,0,.8)]" style={{ background: `linear-gradient(160deg, hsl(${titleHue(item.title)} 50% 36%), hsl(${titleHue(item.title)} 55% 12%))` }} aria-hidden>{item.title}</span>}<div className="min-w-0 pt-1"><h2 className="text-[17px] font-semibold leading-tight tracking-[-0.02em] text-fg">{item.title}</h2><div className="mt-1 text-[13px] text-fg-muted">{item.year ?? "Year unknown"}{item.kind === "series" && item.metadata?.season_count ? ` · ${item.metadata.season_count} season${item.metadata.season_count === 1 ? "" : "s"}` : ""}</div><div className={cn("mt-2 inline-flex items-center gap-1.5 rounded-full border border-white/10 bg-fg/[0.03] px-2 py-0.5 text-[12px] font-semibold", availability.tone)}><span className={cn("size-1.5 rounded-full", availability.dot)} aria-hidden />{availability.label}</div></div></div>
        <Button asChild size="sm" className="mt-3.5 h-9 w-full bg-brand text-brand-fg hover:bg-brand"><Link href={item.kind === "movie" ? `/media/movies/${item.id}` : `/media/series/${item.id}`}>{item.kind === "movie" ? <Film /> : <Tv />} Open {item.kind === "movie" ? "movie" : "TV show"} details</Link></Button>
        <Button type="button" variant="outline" size="sm" className="mt-2 h-8 w-full" disabled={refreshing || !canRefreshMetadata} onClick={() => void refreshMetadata()}><RefreshCw className={cn(refreshing && "animate-spin")} /> Refresh metadata</Button>
      </div>

      <Tabs value={tab} onValueChange={onTabChange} className="gap-0">
        <TabsList variant="line" className="grid h-10 w-full grid-cols-4 gap-0 border-b border-line px-1">
          {(["overview", "files", "metadata", "history"] as const).map((value) => <TabsTrigger key={value} value={value} className="h-10 px-1 text-[12px] capitalize text-fg-muted after:bg-brand data-[state=active]:text-fg">{value}</TabsTrigger>)}
        </TabsList>
        <TabsContent value="overview" className="p-3.5">
          {item.metadata?.overview ? <p className="mb-3 text-[13px] leading-5 text-fg-secondary">{item.metadata.overview}</p> : null}
          {item.metadata?.ratings.length ? <div className="mb-3 grid grid-cols-2 gap-1.5">{item.metadata.ratings.map((rating) => <div key={rating.source} title={rating.provenance} className="rounded-[7px] border border-line bg-surface-3 px-2.5 py-2"><div className="text-[12px] font-semibold uppercase tracking-[0.08em] text-fg-muted">{rating.source}</div><div className="mt-0.5 text-[13px] font-semibold text-fg">{rating.value.toFixed(1)} <span className="text-[12px] font-normal text-fg-muted">/ 10</span></div>{rating.votes > 0 ? <div className="text-[12px] text-fg-muted">{rating.votes.toLocaleString()} votes</div> : null}</div>)}</div> : null}
          {item.metadata?.tagline ? <p className="mb-2 text-[13px] italic text-fg-secondary">{item.metadata.tagline}</p> : null}
          <InspectorRow label="Availability" value={availability.label} />
          <InspectorRow label="Quality profile" value={qualityProfile} />
          {item.metadata?.runtime_minutes ? <InspectorRow label="Runtime" value={`${item.metadata.runtime_minutes} minutes`} /> : null}
          {item.metadata?.release_date ? <InspectorRow label={item.kind === "movie" ? "Release date" : "First air date"} value={item.metadata.release_date} /> : null}
          {item.kind === "series" && item.metadata?.end_date ? <InspectorRow label="End date" value={item.metadata.end_date} /> : null}
          {item.kind === "series" && item.metadata?.season_count ? <InspectorRow label="Seasons" value={String(item.metadata.season_count)} /> : null}
          {item.kind === "series" && item.metadata?.episode_count ? <InspectorRow label="Episodes" value={String(item.metadata.episode_count)} /> : null}
          {item.kind === "series" && item.metadata?.networks.length ? <InspectorRow label="Networks" value={compactValues(item.metadata.networks.map((network) => network.name))} /> : null}
          {item.metadata?.certification ? <InspectorRow label="Certification" value={item.metadata.certification} /> : null}
          {item.metadata?.genres.length ? <InspectorRow label="Genres" value={compactValues(item.metadata.genres)} /> : null}
          <InspectorRow label="Resolution" value={item.resolutions.map(resolutionLabel).join(", ") || "Unknown"} />
          <InspectorRow label="Audio" value={item.audioCodecs.join(", ") || "Unknown"} />
          <InspectorRow label="Languages" value={compactValues(item.languages) || "Unknown"} />
          <InspectorRow label="Root folder" value={item.roots.join(", ") || "Not published"} />
          <InspectorRow label="Plex verification" value={currentFile ? currentFile.plex_confirmed ? "Exact path confirmed" : "Not confirmed" : "No managed file"} />
          {item.kind === "series" && seasonEntries.length > 0 ? <div className="mt-4 border-t border-line pt-3">
            <SectionLabel action={item.missing || item.upgradable ? <span className="text-[12px] font-semibold text-warn">Action may be needed</span> : undefined}>Seasons</SectionLabel>
            <div className="space-y-1.5">{seasonEntries.map((entry) => <SeasonSummary key={entry.number} season={entry} />)}</div>
          </div> : null}
        </TabsContent>
        <TabsContent value="files" className="space-y-2 p-3.5">
          {item.files.map((file) => <Link key={file.id} href={`/files/${file.id}`} className="block rounded-[7px] border border-line bg-surface-3 p-2.5 outline-none hover:border-line-strong focus-visible:ring-2 focus-visible:ring-ring"><div className="flex items-center justify-between gap-2"><span className="truncate text-[12px] font-semibold text-fg">{file.variant}</span><span className="text-[12px] text-fg-muted">Gen {file.generation}</span></div><div className="mt-1 text-[12px] text-fg-muted">{pretty(file.state)} · {formatBytes(file.source_size_bytes)}</div><div className="mt-1 truncate font-mono text-[12px] text-fg-muted">{file.destination_relative}</div></Link>)}
          {item.files.length === 0 ? <InspectorEmpty>No managed files are recorded.</InspectorEmpty> : null}
        </TabsContent>
        <TabsContent value="metadata" className="space-y-2 p-3.5">
          {item.metadata?.sources.map((source) => <div key={`${source.provider}:${source.provider_id}:${source.role}`} className="rounded-[7px] border border-line bg-surface-3 p-2.5"><div className="text-[12px] font-semibold uppercase tracking-[0.08em]"><MetadataSourceLink source={source} /></div><div className="mt-1 text-[12px] text-fg-secondary">{source.role}</div></div>)}
          {item.identities.map((identity) => <div key={`${identity.namespace}:${identity.value}`} className="rounded-[7px] border border-line bg-surface-3 p-2.5"><div className="text-[12px] font-semibold uppercase tracking-[0.08em] text-fg-muted">{identity.namespace}</div><div className="mt-1 break-all font-mono text-[12px] text-fg">{identity.value}</div>{identity.state ? <div className="mt-1 text-[12px] text-fg-muted">{pretty(identity.state)}</div> : null}</div>)}
          {(item.metadata?.companies ?? []).length ? <div className="rounded-[7px] border border-line bg-surface-3 p-2.5"><div className="text-[12px] font-semibold uppercase tracking-[0.08em] text-fg-muted">Studios</div><div className="mt-1 text-[12px] text-fg">{compactValues((item.metadata?.companies ?? []).map((company) => company.name))}</div></div> : null}
          {(item.metadata?.credits ?? []).length ? <div className="rounded-[7px] border border-line bg-surface-3 p-2.5"><div className="text-[12px] font-semibold uppercase tracking-[0.08em] text-fg-muted">Cast and crew</div><div className="mt-1.5 space-y-1.5">{(item.metadata?.credits ?? []).slice(0, 10).map((credit) => <div key={`${credit.source}:${credit.role}:${credit.name}:${credit.character ?? ""}`} className="flex justify-between gap-2 text-[12px]"><span className="truncate text-fg">{credit.name}{credit.character ? ` · ${credit.character}` : ""}</span><span className="shrink-0 text-fg-muted">{credit.role}</span></div>)}</div></div> : null}
          {item.identities.length === 0 ? <InspectorEmpty>No provider identities are recorded.</InspectorEmpty> : null}
        </TabsContent>
        <TabsContent value="history" className="space-y-2 p-3.5">
          {item.workflows.map((workflow) => <div key={workflow.id} className="rounded-[7px] border border-line bg-surface-3 p-2.5"><div className="flex items-center justify-between gap-2"><StateBadge state={workflow.state} /><span className="text-[12px] text-fg-muted">{timeAgo(workflow.updated_at)}</span></div>{workflow.attention_detail ? <div className="mt-1.5 text-[12px] leading-4 text-warn">{workflow.attention_detail}</div> : null}<div className="mt-1 font-mono text-[12px] text-fg-muted">{workflow.variant}</div></div>)}
          {item.workflows.length === 0 ? <InspectorEmpty>No workflow history is recorded.</InspectorEmpty> : null}
        </TabsContent>
      </Tabs>
    </div>
  </aside>
}

function SeasonSummary({ season }: { season: SeriesSeasonRollup }) {
  const episodeCount = Math.max(season.episodeWorkflows.length, season.episodeFiles.length)
  const packFile = season.packFiles.find((file) => file.state === "current") ?? season.packFiles[0]
  return <div className="rounded-[7px] border border-line bg-surface-3 px-2.5 py-2">
    <div className="flex items-center justify-between gap-2">
      <span className="text-[13px] font-semibold text-fg">Season {String(season.number).padStart(2, "0")}</span>
      <div className="flex items-center gap-1.5">
        {season.attentionWorkflows.length > 0 ? <StateBadge state={season.attentionWorkflows[0].state} /> : null}
        {season.packWorkflow ? <StateBadge state={season.packWorkflow.state} /> : packFile ? <StateBadge state={packFile.plex_confirmed ? "plex_confirmed" : packFile.state} /> : null}
        {!season.packWorkflow && !packFile && episodeCount === 0 ? <StatusChip tone="neutral">No workflow</StatusChip> : null}
      </div>
    </div>
    {episodeCount > 0 ? <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-fg-muted">
      <span>{episodeCount} episode scope{episodeCount === 1 ? "" : "s"}</span>
      <span>{season.acquiredEpisodes.length} acquired</span>
      {season.acquiringEpisodes.length > 0 ? <span className="text-info">{season.acquiringEpisodes.length} acquiring</span> : null}
      {season.waitingEpisodes.length > 0 ? <span>{season.waitingEpisodes.length} waiting</span> : null}
      {season.attentionEpisodes.length > 0 ? <span className="font-semibold text-warn">{season.attentionEpisodes.length} need attention</span> : null}
    </div> : null}
    {season.nextSearchAt ? <div className="mt-1 text-[12px] text-fg-subtle">Next {new Date(season.nextSearchAt).toLocaleString()}</div> : null}
  </div>
}

function InspectorRow({ label, value }: { label: string; value: string }) {
  return <div className="flex items-start justify-between gap-3 border-b border-line py-2 last:border-0"><span className="text-[12px] text-fg-muted">{label}</span><span className="max-w-[58%] text-right text-[12px] font-semibold leading-4 text-fg">{value}</span></div>
}

function InspectorEmpty({ children }: { children: React.ReactNode }) {
  return <div className="rounded-[5px] border border-dashed border-line-strong px-3 py-6 text-center text-[13px] text-fg-muted">{children}</div>
}

export function RequestInspector({ request, detail, loading, error, onDecided }: { request: APIRequest; detail: APIRequestDetail | null; loading: boolean; error: string; onDecided?: () => void }) {
  const mediaHref = request.media_id ? (request.media_kind === "series" ? `/media/series/${request.media_id}` : `/media/movies/${request.media_id}`) : null
  const noticeTone = requestLifecycleTone(request.lifecycle)
  const progressMessage = requestProgressMessage(request)
  return <Panel title="Request detail" subtitle="Durable Seerr intake and Media Manager lifecycle state." action={mediaHref ? <Button asChild size="sm" variant="outline"><Link href={mediaHref}><ExternalLink /> Open media</Link></Button> : undefined}>
    <div className="flex items-start gap-3">
      <MetadataArtwork src={request.poster_url} fallback={request.media_kind === "series" ? "/media/series-placeholder.svg" : "/media/movie-placeholder.svg"} alt="" width={108} height={162} className="aspect-[2/3] w-[72px] shrink-0 rounded border border-line-strong object-cover" />
      <div className="min-w-0">
        <div className="text-[13px] font-semibold text-fg">{request.title || `Request ${request.request_id}`}</div>
        <div className="text-[12px] text-fg-muted">{request.year ?? "Year unknown"} · {request.media_kind === "series" ? "TV series" : "Movie"}</div>
        <div className="mt-1.5 flex flex-wrap gap-1"><StateBadge state={request.lifecycle} /><StatusChip tone="neutral">Seerr {seerrStateLabel(request.seerr_state)}</StatusChip></div>
      </div>
    </div>
    {requestAwaitingApproval(request) ? <div className="mt-3 rounded-lg border border-brand-line bg-brand-soft px-3 py-2.5"><div className="text-[12.5px] font-semibold text-fg">Waiting for your approval in Seerr</div><div className="mt-0.5 text-[12px] text-fg-muted">Approving sends the decision to Seerr; Media Manager then takes the request through normal intake.</div><div className="mt-2"><RequestDecisionButtons request={request} onDone={onDecided} /></div></div> : null}
    {progressMessage ? <div className="mt-3"><InlineNotice tone={noticeTone === "neutral" ? "info" : noticeTone} title={requestLifecycleLabel(request.lifecycle)}>{progressMessage}</InlineNotice></div> : null}
    <div className="mt-3">
      <KeyValue label="Seerr request ID" value={request.request_id} />
      <KeyValue label="Seerr source" value={request.seerr_instance_name || request.seerr_instance_id} />
      <KeyValue label="Requester" value={request.requester || "Not persisted"} />
      <KeyValue label="Requested" value={request.requested_at ? new Date(request.requested_at).toLocaleString() : "Unknown"} />
      <KeyValue label="Last updated" value={new Date(request.updated_at).toLocaleString()} />
      <KeyValue label="Intake outcome" value={request.intake_outcome ? pretty(request.intake_outcome) : "—"} />
      {request.workflow_count > 0 ? <KeyValue label="Media Manager workflows" value={`${request.workflow_count} added · ${request.confirmed_count} Plex confirmed`} /> : null}
      {request.last_search_at ? <KeyValue label="Last search" value={new Date(request.last_search_at).toLocaleString()} /> : null}
      {request.next_search_at ? <KeyValue label="Next search" value={retryPlan({ next_rescan_at: request.next_search_at })} /> : null}
      <KeyValue label="Quality profile" value={request.quality_profile || "Not assigned"} />
      {request.media_kind === "series" ? <KeyValue label="Seasons" value={`${request.available_seasons} available of ${request.season_count} requested`} /> : null}
    </div>
    {loading ? <div className="mt-3 text-[12px] text-fg-subtle">Loading linked workflows…</div> : null}
    {error ? <div className="mt-3 text-[12px] text-bad">{error}</div> : null}
    {detail ? <>
      {detail.workflows.length ? <div className="mt-4"><div className="mb-2 text-[12px] font-semibold uppercase tracking-[0.06em] text-fg-muted">Workflows</div><div className="space-y-2">{detail.workflows.map((workflow) => <div key={workflow.id} className="rounded-[5px] border border-line bg-surface-3 p-2"><div className="flex items-center justify-between gap-2"><StateBadge state={workflow.state} /><span className="text-[12px] text-fg-muted">{timeAgo(workflow.updated_at)}</span></div><div className="mt-1 font-mono text-[12px] text-fg-muted">{workflow.variant}</div><div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[12px] text-fg-subtle"><span>Attempts {workflow.acquisition_attempts}</span><span>Round {workflow.round_ordinal}/{workflow.round_attempt_limit}</span></div>{workflow.ready_release_title ? <div className="mt-1 truncate text-[12px] text-brand-text">Ready: {workflow.ready_release_title}</div> : null}{workflow.attention_detail ? <div className="mt-1 text-[12px] text-warn">{workflow.attention_detail}</div> : null}</div>)}</div></div> : null}
      {detail.seasons.length ? <div className="mt-4"><div className="mb-2 text-[12px] font-semibold uppercase tracking-[0.06em] text-fg-muted">Seasons</div><div className="space-y-1">{detail.seasons.map((season) => <div key={season.number} className="flex items-center justify-between gap-2 rounded-[5px] border border-line bg-surface-3 px-2 py-1.5 text-[12px]"><span className="font-semibold text-fg">Season {String(season.number).padStart(2, "0")}</span><span className="text-fg-muted">{season.availability === 5 ? "Available in Seerr" : season.workflow_state ? pretty(season.workflow_state) : "Requested"}</span></div>)}</div></div> : null}
      {detail.files.length ? <div className="mt-4"><div className="mb-2 text-[12px] font-semibold uppercase tracking-[0.06em] text-fg-muted">Published files</div><div className="space-y-1">{detail.files.slice(0, 6).map((file) => <Link key={file.id} href={`/files/${file.id}`} className="block rounded-[5px] border border-line bg-surface-3 px-2 py-1.5 text-[12px] hover:border-line-strong"><div className="flex items-center justify-between gap-2"><span className="truncate text-fg">{file.variant}</span><StateBadge state={file.state} /></div><div className="mt-0.5 truncate font-mono text-[12px] text-fg-subtle">{file.destination_relative}</div></Link>)}</div></div> : null}
    </> : null}
  </Panel>
}

export function LiveRequestsScreen() {
  const page = usePagedList(requestsPageLoader, 100, { autoRefresh: true })
  const { error, refresh } = page
  const items = page.items
  const [filter, setFilter] = React.useState<RequestFilter>("all")
  const [query, setQuery] = React.useState("")
  const [selectedID, setSelectedID] = React.useState<string | null>(null)

  const selected = items.find((item) => item.id === selectedID) ?? null
  const detailLoader = React.useCallback((signal: AbortSignal) => (selectedID ? loadRequest(selectedID, signal) : Promise.resolve(null)), [selectedID])
  const detail = useLiveData<APIRequestDetail | null>(detailLoader, { autoRefresh: true })

  const counts = React.useMemo(() => requestFilterCounts(items), [items])
  const filtered = React.useMemo(() => filterRequests(items, filter, query), [items, filter, query])

  const filterOptions: Array<{ key: RequestFilter; label: string; count: number }> = [
    { key: "all", label: "All", count: counts.total },
    { key: "active", label: "Active", count: counts.active },
    { key: "completed", label: "Completed", count: counts.completed },
    { key: "attention", label: "Needs attention", count: counts.attention },
    { key: "movie", label: "Movies", count: counts.movie },
    { key: "series", label: "TV", count: counts.series },
  ]

  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_340px] 2xl:grid-cols-[minmax(0,1fr)_380px]">
      <Panel flush title="Seerr requests" subtitle="Every row maps to persisted Media Manager state; waiting and failed requests never look successful." action={<RefreshButton onClick={refresh} />}>
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3">
          {filterOptions.map((option) => <button key={option.key} type="button" aria-pressed={filter === option.key} onClick={() => setFilter(option.key)} className={cn("h-[30px] rounded-lg border px-3 text-[12px] font-semibold outline-none transition focus-visible:ring-2 focus-visible:ring-ring", filter === option.key ? "border-brand-line bg-brand-soft text-fg" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg")}>{option.label}{" "}<em className={cn("ml-1 font-medium not-italic tabular-nums", filter === option.key ? "text-brand-text" : "text-fg-subtle")}>{option.count}</em></button>)}
          <label className="relative ml-auto block w-full max-w-[230px]">
            <span className="sr-only">Search requests</span>
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-fg-muted" />
            <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search title, requester, ID" className="h-8 border-line bg-surface-2 pl-8 text-[12px] shadow-none" />
          </label>
        </div>
        {page.loading && items.length === 0 && !error ? <div className="p-10 text-center text-[12px] text-fg-subtle">Loading persisted requests…</div>
          : filtered.length === 0 ? <div className="p-10 text-center text-[12px] text-fg-subtle">{items.length === 0 ? "No Seerr-originated requests are persisted yet." : "No requests match the active filter on the loaded pages."}</div>
          : <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Item</TableHead><TableHead>Status</TableHead><TableHead>Profile</TableHead><TableHead>Requester</TableHead><TableHead>Updated</TableHead></TableRow></TableHeader><TableBody>
            {filtered.map((item) => <TableRow key={item.id} onClick={() => setSelectedID(item.id)} className={cn("cursor-pointer", selectedID === item.id && "bg-brand-soft")}>
              <TableCell><div className="flex min-w-[220px] items-center gap-3"><MiniPoster src={item.poster_url} title={item.title || "Request"} /><div className="min-w-0"><button type="button" onClick={(event) => { event.stopPropagation(); setSelectedID(item.id) }} aria-pressed={selectedID === item.id} className="block max-w-[260px] truncate text-left text-[13px] font-semibold text-fg outline-none hover:underline hover:underline-offset-2 focus-visible:ring-2 focus-visible:ring-ring">{item.title || `Request ${item.request_id}`}</button><div className="text-[12px] text-fg-muted">{item.media_kind === "series" ? "TV" : "Movie"} · {item.year ?? "Year unknown"} · Seerr #{item.request_id}{item.seerr_instance_name ? ` · ${item.seerr_instance_name}` : ""}</div>{item.media_kind === "series" && item.season_count ? <div className="text-[12px] text-fg-subtle">{item.available_seasons}/{item.season_count} seasons available</div> : null}</div></div></TableCell>
              <TableCell>{requestAwaitingApproval(item) ? <div onClick={(event) => event.stopPropagation()}><RequestDecisionButtons request={item} onDone={refresh} /></div> : <StateBadge state={item.lifecycle} />}{requestProgressMessage(item) ? <div className={cn("mt-1 max-w-xs text-[12px]", requestAttentionStates.has(item.lifecycle) ? "text-warn" : "text-fg-subtle")}>{requestProgressMessage(item)}</div> : null}{item.next_search_at ? <div className="mt-1 text-[12px] text-fg-subtle">{retryPlan({ next_rescan_at: item.next_search_at })}</div> : null}</TableCell>
              <TableCell className="text-[12px]">{item.quality_profile || "—"}</TableCell>
              <TableCell className="text-[12px]">{item.requester || "—"}</TableCell>
              <TableCell className="whitespace-nowrap text-[12px] text-fg-subtle">{timeAgo(item.updated_at)}</TableCell>
            </TableRow>)}
          </TableBody></Table></div>}
      </Panel>
      {selected ? <RequestInspector request={selected} detail={detail.data} loading={detail.data === null && !detail.error} error={detail.error} onDecided={refresh} /> : <Panel title="Request detail" subtitle="Select a request to inspect its exact intake and lifecycle state."><div className="rounded-lg border border-dashed border-line-strong px-3 py-10 text-center text-[13px] text-fg-muted">No request is selected.</div></Panel>}
      <ListFooter loaded={page.loaded} hasMore={page.hasMore} loading={page.loadingMore} onLoadMore={page.loadMore} />
    </div>
  </div>
}

/**
 * Wanted separates what is missing by what it is actually waiting for: an
 * owner decision, a release that does not exist yet, an upgrade, or work that
 * is already moving. Waiting is never styled as failure.
 */
export function LiveWantedScreen() {
  const [supportRevision, setSupportRevision] = React.useState(0)
  const [workflows, setWorkflows] = React.useState<APIWorkflow[]>([])
  const [pageWorkflows, setPageWorkflows] = React.useState<APIWorkflow[]>([])
  const [supportReady, setSupportReady] = React.useState(false)
  const [supportError, setSupportError] = React.useState("")
  const [pageContextError, setPageContextError] = React.useState("")
  const workflowCacheRef = React.useRef<Map<string, APIWorkflow>>(new Map())
  const failedContextRef = React.useRef<Set<string>>(new Set())
  const [posters, setPosters] = React.useState<Map<string, string | undefined>>(() => new Map())
  const refreshSupport = React.useCallback(() => {
    setSupportReady(false)
    failedContextRef.current.clear()
    setSupportRevision((current) => current + 1)
  }, [])
  const page = usePagedList(wantedPageLoader, 100, { autoRefresh: true, keyOf: (item) => item.workflow_id, onRefresh: refreshSupport })
  React.useEffect(() => {
    const controller = new AbortController()
    loadCatalogMedia(controller.signal)
      .then((media) => setPosters(new Map(media.map((item) => [item.id, item.metadata?.poster_url]))))
      .catch(() => undefined)
    return () => controller.abort()
  }, [])
  React.useEffect(() => {
    const controller = new AbortController()
    loadWorkflows(false, controller.signal)
      .then((next) => {
        if (controller.signal.aborted) return
        setWorkflows(next)
        setSupportError("")
        setSupportReady(true)
      })
      .catch((reason: unknown) => {
        if (controller.signal.aborted) return
        setSupportError(reason instanceof Error ? reason.message : "Could not load wanted workflow context")
        // A failed window is not a complete empty window: do not fan out one
        // detail request per wanted row behind it.
        setSupportReady(false)
      })
    return () => controller.abort()
  }, [supportRevision])
  const items = page.items
  // The first workflow window is a cheap hint, not a completeness claim. Fetch
  // exact context for wanted rows that fall outside it so an old attention row
  // never loses its actions merely because newer workflows filled the window.
  // The cache makes the per-row reads one-time per row/version, and the worker
  // pool keeps a large Load more page from opening hundreds of parallel reads.
  React.useEffect(() => {
    if (!supportReady) return
    const itemByID = new Map(items.map((item) => [item.workflow_id, item]))
    const wantedAt = (id: string) => new Date(itemByID.get(id)?.updated_at ?? 0).getTime()
    const known = new Set(workflows.map((workflow) => workflow.id))
    const missing = Array.from(itemByID.keys()).filter((id) => {
      if (known.has(id) || failedContextRef.current.has(id)) return false
      const cached = workflowCacheRef.current.get(id)
      return cached === undefined || new Date(cached.updated_at).getTime() < wantedAt(id)
    })
    const bounded = missing.slice(0, 500)
    if (bounded.length === 0) {
      const skipped = failedContextRef.current.size
      setPageContextError(skipped ? `Workflow context is incomplete for ${skipped} wanted row${skipped === 1 ? "" : "s"}; Refresh to retry` : "")
      return
    }
    const controller = new AbortController()
    let cursor = 0
    const worker = async () => {
      while (cursor < bounded.length && !controller.signal.aborted) {
        const id = bounded[cursor]
        cursor += 1
        try {
          const workflow = await loadWorkflow(id, controller.signal)
          if (!controller.signal.aborted) workflowCacheRef.current.set(id, workflow)
        } catch {
          if (!controller.signal.aborted) failedContextRef.current.add(id)
        }
      }
    }
    Promise.all(Array.from({ length: Math.min(6, bounded.length) }, worker))
      .then(() => {
        if (controller.signal.aborted) return
        const wantedIDs = new Set(itemByID.keys())
        for (const id of workflowCacheRef.current.keys()) if (!wantedIDs.has(id)) workflowCacheRef.current.delete(id)
        setPageWorkflows(Array.from(workflowCacheRef.current.values()))
        const incomplete = failedContextRef.current.size + Math.max(0, missing.length - bounded.length)
        setPageContextError(incomplete ? `Workflow context is incomplete for ${incomplete} wanted row${incomplete === 1 ? "" : "s"}; Refresh to retry` : "")
      })
    return () => controller.abort()
  }, [items, supportReady, workflows])
  const error = page.error || supportError || pageContextError
  const refresh = page.refresh
  // Only use workflow context for rows in the current wanted page, and never
  // let an older workflow snapshot override a newer wanted row.
  const wantedByID = new Map(items.map((item) => [item.workflow_id, item]))
  const newestByID = new Map<string, APIWorkflow>()
  for (const workflow of [...workflows, ...pageWorkflows]) {
    const current = newestByID.get(workflow.id)
    if (!current || new Date(workflow.updated_at).getTime() >= new Date(current.updated_at).getTime()) newestByID.set(workflow.id, workflow)
  }
  const relevantWorkflows = Array.from(newestByID.values()).filter((workflow) => {
    const item = wantedByID.get(workflow.id)
    return item !== undefined && new Date(workflow.updated_at).getTime() >= new Date(item.updated_at).getTime()
  })
  const byID = new Map(relevantWorkflows.map((workflow) => [workflow.id, workflow]))
  const attention = attentionEntries(items, relevantWorkflows)
  const attentionIDs = new Set(attention.map((entry) => entry.key))
  const inProgress = items.filter((item) => !attentionIDs.has(item.workflow_id) && isActivelyAcquiring(byID.get(item.workflow_id)?.state ?? item.current_state))
  const progressIDs = new Set(inProgress.map((item) => item.workflow_id))
  const missing = items.filter((item) => item.gap === "missing" && !attentionIDs.has(item.workflow_id) && !progressIDs.has(item.workflow_id))
  const upgrades = items.filter((item) => item.gap === "upgrade" && !attentionIDs.has(item.workflow_id) && !progressIDs.has(item.workflow_id))
  const posterFor = (id: string) => posters.get(id)
  const itemCell = (item: APIWantedItem) => <div className="flex min-w-[220px] items-center gap-2.5"><MiniPoster src={posterFor(item.media_id)} title={item.title} /><div className="min-w-0"><Link className="block truncate text-[12.5px] font-semibold text-fg hover:underline" href={mediaHref(item.media_kind, item.media_id)}>{item.title}{item.year ? ` (${item.year})` : ""}</Link><small className="text-[10.5px] text-fg-subtle">{item.media_kind === "series" ? "TV" : "Movie"} · {item.variant}</small></div></div>
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    {page.loading && items.length === 0 && !error ? <LoadingPanel label="Deriving wanted and upgrade gaps…" /> : <Tabs defaultValue={attention.length ? "attention" : missing.length ? "missing" : upgrades.length ? "upgrades" : "progress"} className="gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <TabsList className="mm-seg" aria-label="Wanted sections">
          <TabsTrigger value="attention">Needs attention <em className={cn("ml-1 not-italic", attention.length && "text-warn")}>{attention.length}</em></TabsTrigger>
          <TabsTrigger value="missing">Missing <em className="ml-1 not-italic">{missing.length}</em></TabsTrigger>
          <TabsTrigger value="upgrades">Upgrades <em className="ml-1 not-italic">{upgrades.length}</em></TabsTrigger>
          <TabsTrigger value="progress">In progress <em className="ml-1 not-italic">{inProgress.length}</em></TabsTrigger>
        </TabsList>
        <RefreshButton onClick={refresh} />
      </div>
      <p className="text-[12px] leading-5 text-fg-subtle"><strong className="text-fg-secondary">Needs attention</strong> requires an owner decision; <strong className="text-fg-secondary">Missing</strong> and <strong className="text-fg-secondary">Upgrades</strong> include normal waiting and paced discovery; <strong className="text-fg-secondary">In progress</strong> is automatic work. Counts and filters cover loaded rows; use <strong className="text-fg-secondary">Load more</strong> before treating an empty group as global.</p>
      <TabsContent value="attention">
        <Panel flush title="Needs attention" subtitle="What happened, what Media Manager will do next, and what you can do." className={cn(attention.length && "border-warn-line")}>
          {attention.length === 0 ? <div className={cn("flex items-center gap-2.5 px-4 py-4 text-[12.5px]", page.hasMore ? "text-fg-subtle" : "text-good")}><CheckCircle2 className="size-4" aria-hidden />{page.hasMore ? "No loaded row needs attention; load more to check the remaining rows." : "Nothing needs your attention. Waiting and searching continue automatically."}</div>
            : attention.map((entry) => <AttentionCard key={entry.key} item={entry.item} workflow={entry.workflow} poster={posterFor(entry.item.mediaId)} />)}
        </Panel>
      </TabsContent>
      <TabsContent value="missing">
        <Panel flush title="Missing" subtitle="Monitored items without a current publication, and when Media Manager looks again.">
          <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Item</TableHead><TableHead>Why missing</TableHead><TableHead>Next attempt</TableHead><TableHead>Profile</TableHead><TableHead /></TableRow></TableHeader><TableBody>{missing.map((item) => { const workflow = byID.get(item.workflow_id); return <TableRow key={item.workflow_id}><TableCell>{itemCell(item)}</TableCell><TableCell><StateBadge state={item.current_state || "waiting"} />{workflow?.search_blocked_reason ? <div className="mt-1 max-w-xs text-[11.5px] leading-4 text-fg-subtle">{workflow.search_blocked_reason}</div> : null}</TableCell><TableCell className="whitespace-nowrap text-[12px]">{retryPlan(workflow)}</TableCell><TableCell className="text-[12px]">{item.target_profile}</TableCell><TableCell className="text-right"><Button asChild size="sm" variant="outline"><Link href={mediaHref(item.media_kind, item.media_id)}>Open</Link></Button></TableCell></TableRow> })}</TableBody></Table></div>
          {missing.length === 0 ? <div className="p-6 text-center text-[12px] text-fg-subtle">{page.hasMore ? "No missing items in the loaded rows; load more to check the remaining rows." : "Nothing is missing outside attention and active work."}</div> : null}
        </Panel>
      </TabsContent>
      <TabsContent value="upgrades">
        <Panel flush title="Upgrades" subtitle="Current copies stay until the replacement passes verification and Plex confirms it.">
          <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Item</TableHead><TableHead>Workflow</TableHead><TableHead>Target profile</TableHead><TableHead>Updated</TableHead><TableHead /></TableRow></TableHeader><TableBody>{upgrades.map((item) => <TableRow key={item.workflow_id}><TableCell>{itemCell(item)}</TableCell><TableCell><StateBadge state={item.current_state || "waiting"} /></TableCell><TableCell className="text-[12px]">{item.target_profile}<div className="text-[11px] text-fg-subtle">Upgrades {item.upgrades_enabled ? "enabled" : "disabled"}</div></TableCell><TableCell className="text-[12px]">{timeAgo(item.updated_at)}</TableCell><TableCell className="text-right"><Button asChild size="sm" variant="outline"><Link href={mediaHref(item.media_kind, item.media_id)}>Open</Link></Button></TableCell></TableRow>)}</TableBody></Table></div>
          {upgrades.length === 0 ? <div className="p-6 text-center text-[12px] text-fg-subtle">{page.hasMore ? "No upgrades in the loaded rows; load more to check the remaining rows." : "No upgrades are wanted right now."}</div> : null}
          <div className="border-t border-line px-4 py-2.5 text-[11.5px] text-fg-subtle">A failed verification leaves your current file untouched.</div>
        </Panel>
      </TabsContent>
      <TabsContent value="progress">
        <Panel flush title="In progress" subtitle="Searching, downloading, verifying, or publishing right now.">
          {inProgress.length === 0 ? <div className="px-4 py-5 text-[12.5px] text-fg-subtle">{page.hasMore ? "Nothing in the loaded rows is actively acquiring; load more to check the remaining rows." : "Nothing is actively acquiring right now."}</div>
            : inProgress.map((item) => { const state = byID.get(item.workflow_id)?.state ?? item.current_state; return <ConsoleRow key={item.workflow_id} href={mediaHref(item.media_kind, item.media_id)} leading={<MiniPoster src={posterFor(item.media_id)} title={item.title} />} title={`${item.title}${item.year ? ` (${item.year})` : ""}`} caption={`${item.gap === "upgrade" ? "Upgrade" : "Acquisition"} · ${item.variant}`}>{byID.get(item.workflow_id)?.download_percent !== undefined ? <DownloadProgress workflow={byID.get(item.workflow_id)!} className="hidden w-[150px] sm:inline-flex" /> : <StageStepper state={state} className="hidden w-[150px] sm:flex" />}<StateBadge state={state} /><span className="w-12 shrink-0 text-right text-[11px] text-fg-subtle">{timeAgo(item.updated_at)}</span></ConsoleRow> })}
        </Panel>
      </TabsContent>
    </Tabs>}
    <ListFooter loaded={page.loaded} hasMore={page.hasMore} loading={page.loadingMore} onLoadMore={page.loadMore} />
  </div>
}

/** Monday-first week of persisted air dates; a date never triggers a search. */
function CalendarWeek({ items }: { items: APICalendarEntry[] }) {
  const [offset, setOffset] = React.useState(0)
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const monday = new Date(today)
  monday.setDate(today.getDate() - ((today.getDay() + 6) % 7) + offset * 7)
  const days = Array.from({ length: 7 }, (_, index) => { const day = new Date(monday); day.setDate(monday.getDate() + index); return day })
  const key = (value: Date) => `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, "0")}-${String(value.getDate()).padStart(2, "0")}`
  const byDay = new Map<string, APICalendarEntry[]>()
  for (const item of items) { const day = item.air_date.slice(0, 10); byDay.set(day, [...(byDay.get(day) ?? []), item]) }
  const end = days[6]
  const label = `${monday.toLocaleDateString(undefined, { day: "numeric", month: "short" })} → ${end.toLocaleDateString(undefined, { day: "numeric", month: "short" })}`
  return <section aria-label="Week view" className="space-y-3">
    <div className="flex flex-wrap items-center justify-between gap-2"><div className="text-[12.5px] text-fg-subtle">Air dates and planned activity · {label}</div><div className="flex gap-1.5"><Button variant="outline" size="sm" onClick={() => setOffset((value) => value - 1)}><ChevronLeft /> Previous</Button><Button variant="outline" size="sm" disabled={offset === 0} onClick={() => setOffset(0)}>Today</Button><Button variant="outline" size="sm" onClick={() => setOffset((value) => value + 1)}>Next <ChevronRight /></Button></div></div>
    <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-7">
      {days.map((day) => { const entries = byDay.get(key(day)) ?? []; const isToday = day.getTime() === today.getTime(); return <div key={key(day)} className={cn("flex min-h-[150px] flex-col gap-1.5 rounded-[11px] border bg-surface p-2.5", isToday ? "border-brand-line bg-brand-soft/60" : "border-line")}>
        <h5 className={cn("text-[11px] font-bold uppercase tracking-[0.06em]", isToday ? "text-brand-text" : "text-fg-subtle")}>{day.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" })}{isToday ? " · today" : ""}</h5>
        {entries.length === 0 ? <span className="text-[11px] text-fg-subtle">Nothing scheduled</span> : entries.map((entry) => { const done = entry.workflow_state === "plex_confirmed"; const attention = entry.workflow_state === "needs_attention"; return <Link key={`${entry.workflow_id}:${entry.season}:${entry.episode}`} href={`/media/series/${entry.media_id}`} className={cn("rounded-lg border bg-surface-2 px-2 py-1.5 text-[11px] leading-[15px] outline-none transition hover:border-line-strong focus-visible:ring-2 focus-visible:ring-ring", done ? "border-good-line" : attention ? "border-warn-line" : "border-line")}><b className="block truncate font-semibold text-fg">{entry.media_title}</b><small className={cn(done ? "text-good" : attention ? "text-warn" : "text-fg-subtle")}>S{String(entry.season).padStart(2, "0")}E{String(entry.episode).padStart(2, "0")} · {done ? "In Plex" : attention ? "Needs you" : entry.monitored ? "auto-acquire" : "not monitored"}</small></Link> })}
      </div> })}
    </div>
    <p className="text-[11.5px] text-fg-subtle">The calendar shows media timing from your providers. A date never triggers a search on its own; internal scheduler details stay in System → Tasks.</p>
  </section>
}

export function LiveCalendarScreen() {
  const { data, error, refresh } = useLiveData(calendarLoader, { autoRefresh: true })
  const items = data ?? []
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}<CalendarWeek items={items} />{!data && !error ? <LoadingPanel /> : <Panel flush title="Episode availability calendar" subtitle="Exact Seerr air dates persisted with immutable season expectations; unknown dates are omitted." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Date</TableHead><TableHead>Series</TableHead><TableHead>Episode</TableHead><TableHead>Monitoring</TableHead><TableHead>Workflow</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={`${item.workflow_id}:${item.season}:${item.episode}`}><TableCell>{new Date(item.air_date).toLocaleDateString()}</TableCell><TableCell><Link className="font-semibold text-fg hover:text-info" href={`/media/series/${item.media_id}`}>{item.media_title}</Link></TableCell><TableCell>S{String(item.season).padStart(2, "0")}E{String(item.episode).padStart(2, "0")}{item.episode_title ? <div className="mt-1 text-[12px] text-fg-subtle">{item.episode_title}</div> : null}</TableCell><TableCell><StateBadge state={item.monitored ? "monitored" : "unmonitored"} /></TableCell><TableCell><StateBadge state={item.workflow_state} /></TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No exact episode air dates are persisted in the current calendar window.</div> : null}</Panel>}</div>
}

function activityReason(item: APIActivity) {
  const value = item.detail?.reason ?? item.detail?.destination_relative
  return typeof value === "string" ? value : ""
}

export function LiveActivityHistoryScreen({ logs = false }: { logs?: boolean }) {
  const { data, error, refresh } = useLiveData(activityLoader)
  const items = data ?? []
  const audit = items.filter((item) => item.kind === "audit").length
  const transitions = items.length - audit
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Loaded events" value={String(items.length)} note="Bounded to the latest 300" icon={Activity} /><MetricCard label="Audit writes" value={String(audit)} note="Persisted owner and worker actions" tone="info" icon={ShieldCheck} /><MetricCard label="Transitions" value={String(transitions)} note="Workflow state changes" icon={ListChecks} /></div>
    {!data && !error ? <LoadingPanel /> : <Panel flush title={logs ? "Application audit" : "Activity history"} subtitle={logs ? "Persisted structured writes; container stdout is intentionally not presented as durable audit." : "Append-only audit writes and workflow transitions."} action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Time</TableHead><TableHead>Event</TableHead><TableHead>Object</TableHead><TableHead>Actor</TableHead><TableHead>Detail</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell className="whitespace-nowrap">{new Date(item.occurred_at).toLocaleString()}</TableCell><TableCell className="font-semibold">{pretty(item.action)}</TableCell><TableCell>{item.aggregate_type}</TableCell><TableCell>{item.actor}</TableCell><TableCell className="max-w-md text-[12px] text-fg-subtle">{activityReason(item) || "—"}</TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No persisted events.</div> : null}</Panel>}
  </div>
}

export function LiveFilesScreen() {
  const page = usePagedList(filesPageLoader, 100)
  const items = page.items
  const current = items.filter((item) => item.state === "current").length
  const confirmed = items.filter((item) => item.plex_confirmed).length
  const attention = items.filter((item) => item.state === "quarantined").length
  return <div className="space-y-4">
    {page.error ? <ErrorNotice error={page.error} /> : null}
    <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Current" value={String(current)} note={`${page.loaded} loaded; active managed library files`} tone="good" icon={Files} /><MetricCard label="Plex confirmed" value={String(confirmed)} note="Exact observed path and identity" tone="info" icon={CheckCircle2} /><MetricCard label="Quarantined" value={String(attention)} note="Publication needs review" tone={attention ? "warn" : "good"} icon={AlertTriangle} /></div>
    {page.loading && items.length === 0 ? <LoadingPanel /> : <Panel flush title="Managed files" subtitle="Plex-safe AltMount symlinks and reviewed adopted real files with persisted provenance. Pages load on demand." action={<RefreshButton onClick={page.refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Media</TableHead><TableHead>Destination</TableHead><TableHead>Origin</TableHead><TableHead>Quality evidence</TableHead><TableHead>State</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell><Link className="font-semibold text-fg hover:text-info" href={`/files/${item.id}`}>{item.title}{item.year ? ` (${item.year})` : ""}</Link><div className="mt-1 text-[12px] text-fg-subtle">{item.root_name} · {item.variant}</div></TableCell><TableCell className="max-w-xs break-all font-mono text-[12px]">{item.destination_relative}</TableCell><TableCell>{item.origin === "adoption" ? "Adopted real file" : "AltMount symlink"}<div className="mt-1 max-w-xs break-all font-mono text-[12px] text-fg-subtle">{item.source_relative}</div></TableCell><TableCell><StateBadge state={item.quality_evidence_state} /><div className="mt-1 text-[12px] text-fg-subtle">{[qualityFact(item.quality_evidence, "resolution"), qualityFact(item.quality_evidence, "video_codec")].filter(Boolean).join(" · ") || item.quality_profile}</div></TableCell><TableCell><StateBadge state={item.state} /></TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No managed files exist. AltMount source remains read-only.</div> : null}<ListFooter loaded={page.loaded} hasMore={page.hasMore} loading={page.loadingMore} onLoadMore={page.loadMore} /></Panel>}
  </div>
}

function ListFooter({ loaded, hasMore, loading, onLoadMore }: { loaded: number; hasMore: boolean; loading: boolean; onLoadMore: () => void }) {
  if (!hasMore && loaded === 0) return null
  return <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line px-4 py-3 text-[12px] text-fg-subtle"><span>{loaded.toLocaleString()} loaded{hasMore ? " · more available" : " · end of list"}</span>{hasMore ? <Button size="sm" variant="outline" disabled={loading} onClick={onLoadMore}>{loading ? "Loading…" : "Load more"}</Button> : null}</div>
}

export function LiveFileDetailScreen({ fileId }: { fileId: string }) {
  const loader = React.useCallback((signal: AbortSignal) => loadManagedFile(fileId, signal), [fileId])
  const { data, error, refresh } = useLiveData(loader)
  if (!data && !error) return <LoadingPanel label="Loading file provenance…" />
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <>
    <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Origin" value={data.origin === "adoption" ? "Adopted" : `Generation ${data.generation}`} note={data.origin === "adoption" ? "Reviewed real file" : "Immutable link generation"} icon={Files} /><MetricCard label="Publication" value={pretty(data.state)} note={data.verified_at ? `Verified ${timeAgo(data.verified_at)}` : "Not verified yet"} tone={fileTone(data.state, data.plex_confirmed)} icon={FileCheck2} /><MetricCard label="Plex evidence" value={data.plex_confirmed ? "Confirmed" : "Not confirmed"} note="Exact path and provider identity" tone={data.plex_confirmed ? "good" : "neutral"} icon={SearchCheck} /></div>
    {data.origin === "adoption" && data.quality_evidence_state === "unassessed" ? <InlineNotice tone="warn" title="Current quality is unassessed">The adoption journal persisted the file fingerprint, reviewed season scope, profile, and Plex identity. It did not invent media-info or quality facts.</InlineNotice> : null}
    <div className="grid gap-4 xl:grid-cols-[1.3fr_1fr]"><Panel title="Path relationship" subtitle={data.origin === "adoption" ? "A reviewed real file was renamed inside its configured library root and then confirmed by Plex." : "Media Manager publishes a symlink; the AltMount source is never renamed or modified."}><KeyValue label="Library root" value={data.root_name} /><KeyValue label="Destination" value={<span className="break-all font-mono">{data.destination_relative}</span>} /><KeyValue label={data.origin === "adoption" ? "Discovered source" : "AltMount source"} value={<span className="break-all font-mono">{data.original_source_root ? `${data.original_source_root}/${data.source_relative}` : data.source_relative}</span>} /><KeyValue label="Plex observed path" value={<span className="break-all font-mono">{data.plex_visible_target}</span>} /><KeyValue label="Provider identity" value={data.plex_provider_guid || "—"} /></Panel><Panel title="Lifecycle and quality" action={<RefreshButton onClick={refresh} />}><KeyValue label="State" value={pretty(data.state)} /><KeyValue label="Scope" value={data.variant} /><KeyValue label="Target profile" value={data.quality_profile} /><KeyValue label="Quality evidence" value={pretty(data.quality_evidence_state)} /><KeyValue label="Resolution" value={qualityFact(data.quality_evidence, "resolution") || "—"} /><KeyValue label="Video codec" value={qualityFact(data.quality_evidence, "video_codec") || "—"} /><KeyValue label="Dynamic range" value={qualityFact(data.quality_evidence, "dynamic_range") || "—"} /><KeyValue label="Audio" value={[qualityFact(data.quality_evidence, "audio_codecs"), qualityFact(data.quality_evidence, "audio_languages")].filter(Boolean).join(" · ") || "—"} /><KeyValue label="Subtitles" value={qualityFact(data.quality_evidence, "subtitle_languages") || "—"} /><KeyValue label="Source size" value={formatBytes(data.source_size_bytes)} /><KeyValue label="Source modified" value={data.source_modified_at ? new Date(data.source_modified_at).toLocaleString() : "—"} /><KeyValue label="Verified" value={data.verified_at ? new Date(data.verified_at).toLocaleString() : "—"} /><KeyValue label="Displaced" value={data.displaced_at ? new Date(data.displaced_at).toLocaleString() : "—"} /><KeyValue label="Removed" value={data.removed_at ? new Date(data.removed_at).toLocaleString() : "—"} /><Button asChild variant="outline" className="mt-4 w-full"><Link href={data.media_kind === "movie" ? `/media/movies/${data.media_id}` : `/media/series/${data.media_id}`}>Open media record</Link></Button></Panel></div>
  </> : null}</div>
}

export function LiveReleaseIntelligenceScreen({ mode = "releases" }: { mode?: "releases" | "evidence" | "groups" }) {
  const { data, error, refresh } = useLiveData(releasesLoader)
  const items = data ?? []
  if (mode === "groups") return <LiveReleaseGroups items={items} error={error} loading={!data && !error} refresh={refresh} />
  const eligible = items.filter((item) => item.verdict === "eligible").length
  const held = items.filter((item) => item.verdict === "held").length
  const rejected = items.filter((item) => item.verdict === "rejected").length
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Eligible" value={String(eligible)} note="Passed required rules" tone="good" icon={CheckCircle2} /><MetricCard label="Held" value={String(held)} note="Unknown or conflicting evidence" tone={held ? "warn" : "good"} icon={AlertTriangle} /><MetricCard label="Rejected" value={String(rejected)} note="Failed a hard requirement" tone={rejected ? "bad" : "neutral"} icon={ShieldCheck} /></div>
    {!data && !error ? <LoadingPanel label="Loading persisted release evidence…" /> : mode === "evidence" ? <EvidenceTable items={items} refresh={refresh} /> : <ReleaseTable items={items} refresh={refresh} />}
  </div>
}

export function LiveBlocklistScreen() {
  const blocksState = useLiveData(blocksLoader)
  const candidatesState = useLiveData(releasesLoader)
  const blocks = blocksState.data ?? []
  const candidates = candidatesState.data ?? []
  const [candidateID, setCandidateID] = React.useState("")
  const [scope, setScope] = React.useState<"native_release" | "pack_release">("native_release")
  const [reason, setReason] = React.useState("")
  const [retention, setRetention] = React.useState("90")
  const [busy, setBusy] = React.useState("")
  const selected = candidates.find((candidate) => candidate.id === candidateID)
  const active = blocks.filter((block) => !block.expires_at || new Date(block.expires_at) > new Date())
  async function add(event: React.FormEvent) {
    event.preventDefault()
    if (!selected) return
    setBusy("create")
    try {
      const days = Number(retention) as 0 | 30 | 90
      await createReleaseBlock({ candidate_id: selected.id, scope, reason: reason.trim(), ...(days ? { retention_days: days } : {}) })
      toast.success("Release block persisted"); setReason(""); await blocksState.refresh()
    } catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not block release") }
    finally { setBusy("") }
  }
  async function remove(id: string) {
    if (!window.confirm("Remove this release block? Future searches may consider the release again.")) return
    setBusy(id)
    try { await deleteReleaseBlock(id); toast.success("Release block removed"); await blocksState.refresh() }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not remove block") }
    finally { setBusy("") }
  }
  const error = blocksState.error || candidatesState.error
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Active blocks" value={String(active.length)} note="Exact release scopes only" tone={active.length ? "warn" : "good"} icon={ShieldCheck} /><MetricCard label="Native release" value={String(active.filter((item) => item.scope === "native_release").length)} note="Indexer plus native GUID" icon={Globe2} /><MetricCard label="Season packs" value={String(active.filter((item) => item.scope === "pack_release").length)} note="Media, season, and exact title" icon={ListChecks} /></div>
    <div className="grid gap-4 xl:grid-cols-[1.3fr_380px]"><Panel flush title="Release blocklist" subtitle="Blocks reject only the persisted native release or exact media/season pack; provider health is unaffected." action={<RefreshButton onClick={() => { blocksState.refresh(); candidatesState.refresh() }} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Release</TableHead><TableHead>Scope</TableHead><TableHead>Reason</TableHead><TableHead>Expires</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{blocks.map((block) => { const expired = Boolean(block.expires_at && new Date(block.expires_at) <= new Date()); return <TableRow key={block.id}><TableCell className="max-w-lg font-semibold text-fg">{block.release_title}<div className="mt-1 text-[12px] text-fg-subtle">{block.scope === "native_release" ? block.indexer_name : `${block.media_title} · ${block.variant}`}</div></TableCell><TableCell><StatusChip tone={expired ? "neutral" : "warn"}>{expired ? "Expired" : pretty(block.scope)}</StatusChip></TableCell><TableCell className="max-w-sm">{block.reason}</TableCell><TableCell>{block.expires_at ? new Date(block.expires_at).toLocaleDateString() : "Manual removal"}</TableCell><TableCell><Button size="sm" variant="ghost" disabled={busy === block.id} onClick={() => void remove(block.id)}>Remove</Button></TableCell></TableRow> })}</TableBody></Table></div>{blocks.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No releases are blocked.</div> : null}</Panel><Panel title="Block observed release" subtitle="Only a persisted candidate can become a block; arbitrary broad patterns are not accepted."><form className="grid gap-3" onSubmit={add}><label className="grid gap-1.5 text-[12.5px] font-medium text-fg-secondary">Observed candidate<select className="mm-input h-10 rounded-md px-3" value={candidateID} onChange={(event) => { const value = event.target.value; setCandidateID(value); const item = candidates.find((candidate) => candidate.id === value); if (parseSeriesVariant(item?.variant).kind !== "season") setScope("native_release") }} required><option value="">Select a persisted candidate</option>{candidates.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.media_title} · {candidate.title}</option>)}</select></label><label className="grid gap-1.5 text-[12.5px] font-medium text-fg-secondary">Scope<select className="mm-input h-10 rounded-md px-3" value={scope} onChange={(event) => setScope(event.target.value as "native_release" | "pack_release")}><option value="native_release">This indexer + native release</option><option value="pack_release" disabled={parseSeriesVariant(selected?.variant).kind !== "season"}>This exact media + season pack title</option></select></label><label className="grid gap-1.5 text-[12.5px] font-medium text-fg-secondary">Retention<select className="mm-input h-10 rounded-md px-3" value={retention} onChange={(event) => setRetention(event.target.value)}><option value="30">30 days</option><option value="90">90 days</option><option value="0">Until manually removed</option></select></label><label className="grid gap-1.5 text-[12.5px] font-medium text-fg-secondary">Reason<Input className="mm-input" minLength={3} maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} placeholder="Why this exact release is unsafe" required /></label><Button type="submit" disabled={!selected || busy === "create"}><ShieldCheck /> Persist narrow block</Button></form><InlineNotice tone="info" title="Search behavior">Blocked observations remain in evidence history with a rejected policy decision. No request is sent to an indexer from this screen.</InlineNotice></Panel></div>
  </div>
}

function ReleaseTable({ items, refresh }: { items: APIReleaseCandidate[]; refresh: () => void }) {
  return <Panel flush title="Release candidates" subtitle="Persisted observations and policy decisions from paced searches." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Candidate</TableHead><TableHead>Media</TableHead><TableHead>Indexer</TableHead><TableHead>Size</TableHead><TableHead>Decision</TableHead><TableHead>Last seen</TableHead><TableHead>Evidence</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell className="max-w-lg font-semibold text-fg">{item.title}{item.selected ? <div className="mt-1 text-[12px] text-good">Selected for this workflow</div> : null}</TableCell><TableCell><Link className="hover:text-info" href={item.media_kind === "movie" ? `/media/movies/${item.media_id}` : `/media/series/${item.media_id}`}>{item.media_title}</Link></TableCell><TableCell>{item.indexer_name}</TableCell><TableCell>{formatBytes(item.size_bytes)}</TableCell><TableCell><StatusChip tone={verdictTone(item.verdict ?? "", item.selected)}>{item.selected ? "Selected" : item.verdict ? pretty(item.verdict) : "Unevaluated"}{item.score !== undefined ? ` · ${item.score}` : ""}</StatusChip></TableCell><TableCell>{timeAgo(item.last_observed_at)}</TableCell><TableCell className="text-fg-subtle">{timeAgo(item.observed_at)}</TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No release observations have been persisted.</div> : null}</Panel>
}

// The evidence view expands every candidate into one row per normalized fact.
// A full production candidate page is tens of thousands of rows, which is both
// slow to render and hostile to assistive technology. Keep a bounded, truthful
// window and tell the owner when the view is truncated.
const evidenceRowLimit = 300

function EvidenceTable({ items, refresh }: { items: APIReleaseCandidate[]; refresh: () => void }) {
  const rows = React.useMemo(() => items.flatMap((item) => Object.entries(item.normalized_facts ?? {}).slice(0, 20).map(([fact, value]) => ({ id: item.id, title: item.title, verdict: item.verdict, selected: item.selected, fact, value }))), [items])
  const visible = rows.slice(0, evidenceRowLimit)
  const hidden = rows.length - visible.length
  return <Panel flush title="Evidence explorer" subtitle="Normalized facts point to a persisted raw observation; unknowns stay visible." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Candidate</TableHead><TableHead>Fact</TableHead><TableHead>Value</TableHead><TableHead>Decision</TableHead></TableRow></TableHeader><TableBody>{visible.map((row) => <TableRow key={`${row.id}:${row.fact}`}><TableCell className="max-w-sm truncate font-semibold">{row.title}</TableCell><TableCell className="font-mono text-[12px]">{row.fact}</TableCell><TableCell className="max-w-lg break-words">{formatFact(row.value)}</TableCell><TableCell><StatusChip tone={verdictTone(row.verdict ?? "", row.selected)}>{row.verdict ? pretty(row.verdict) : "Unevaluated"}</StatusChip></TableCell></TableRow>)}</TableBody></Table></div>{hidden > 0 ? <div className="border-t border-line p-3 text-[12px] text-fg-subtle">Showing the first {visible.length.toLocaleString()} of {rows.length.toLocaleString()} persisted evidence facts from {items.length.toLocaleString()} candidates. Narrow the release set above to inspect the rest.</div> : null}{items.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No evidence has been persisted.</div> : null}</Panel>
}

function formatFact(value: unknown) {
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") return String(value)
  if (Array.isArray(value)) return value.map(String).join(", ")
  return value ? JSON.stringify(value) : "Unknown"
}

function LiveReleaseGroups({ items, error, loading, refresh }: { items: APIReleaseCandidate[]; error: string; loading: boolean; refresh: () => void }) {
  const groups = React.useMemo(() => {
    const result = new Map<string, { observations: number; eligible: number; held: number; rejected: number }>()
    for (const item of items) {
      const facts = item.normalized_facts ?? {}
      const raw = facts.release_group ?? facts.group
      const name = typeof raw === "string" && raw.trim() ? raw.trim() : "Unknown"
      const current = result.get(name) ?? { observations: 0, eligible: 0, held: 0, rejected: 0 }
      current.observations += 1
      if (item.verdict === "eligible") current.eligible += 1
      if (item.verdict === "held") current.held += 1
      if (item.verdict === "rejected") current.rejected += 1
      result.set(name, current)
    }
    return Array.from(result.entries()).sort((left, right) => right[1].observations - left[1].observations)
  }, [items])
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{loading ? <LoadingPanel /> : <Panel flush title="Observed release groups" subtitle="Counts summarize current persisted evidence; they are not permanent trust labels." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Group</TableHead><TableHead>Observations</TableHead><TableHead>Eligible</TableHead><TableHead>Held</TableHead><TableHead>Rejected</TableHead></TableRow></TableHeader><TableBody>{groups.map(([name, counts]) => <TableRow key={name}><TableCell className="font-semibold">{name}</TableCell><TableCell>{counts.observations}</TableCell><TableCell>{counts.eligible}</TableCell><TableCell>{counts.held}</TableCell><TableCell>{counts.rejected}</TableCell></TableRow>)}</TableBody></Table></div>{groups.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No group evidence has been observed.</div> : null}</Panel>}</div>
}

export function LiveSourcesScreen({ kind }: { kind?: "newznab" | "altmount" }) {
  const { data, error, refresh } = useLiveData(sourcesLoader)
  const items = (data ?? []).filter((item) => !kind || item.kind === kind || kind === "newznab" && item.kind === "oldboys")
  const configured = items.filter((item) => item.configuration_state === "configured").length
  const waiting = items.filter((item) => item.configuration_state === "awaiting_commission").length
  const cooling = items.filter((item) => item.cooldown_until && new Date(item.cooldown_until) > new Date()).length
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Configured" value={String(configured)} note="Enabled records; not a synthetic health probe" tone="info" icon={kind === "altmount" ? HardDrive : Globe2} /><MetricCard label="Awaiting commission" value={String(waiting)} note="Disabled until paced validation" tone={waiting ? "warn" : "good"} icon={Gauge} /><MetricCard label="Cooling down" value={String(cooling)} note="Persisted indexer budget" tone={cooling ? "warn" : "good"} icon={Activity} /></div>
    {!data && !error ? <LoadingPanel /> : <SourceTable items={items} refresh={refresh} />}
  </div>
}

function SourceTable({ items, refresh }: { items: APISourceStatus[]; refresh: () => void }) {
  return <Panel flush title="Configured sources" subtitle="Connection configuration and observed scheduler state remain separate." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Type</TableHead><TableHead>Endpoint</TableHead><TableHead>Configuration</TableHead><TableHead>Budget / latest outcome</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell><Link className="font-semibold text-fg hover:text-info" href={(item.kind === "newznab" || item.kind === "oldboys") ? `/sources/indexers/${item.id}` : "/settings/connections"}>{item.name}</Link></TableCell><TableCell>{item.kind}</TableCell><TableCell className="max-w-sm break-all font-mono text-[12px]">{sourceEndpoint(item)}</TableCell><TableCell><StateBadge state={item.configuration_state} /></TableCell><TableCell>{item.minimum_interval_ms ? `${item.minimum_interval_ms} ms` : "—"}<div className="mt-1 text-[12px] text-fg-subtle">{item.last_outcome ? pretty(item.last_outcome) : "No observed request outcome"}</div></TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No matching sources are configured.</div> : null}</Panel>
}

function sourceEndpoint(item: APISourceStatus) {
  const endpoint = item.config?.base_url
  return typeof endpoint === "string" ? endpoint : "—"
}

function indexerDialect(item: APISourceStatus, kind: "movie" | "series") {
  const dialects = item.config.search_dialects
  if (!dialects || typeof dialects !== "object" || Array.isArray(dialects)) return item.config.mode_verified === true ? String(item.config.mode ?? "search") : "Not verified"
  const dialect = (dialects as Record<string, unknown>)[kind]
  if (!dialect || typeof dialect !== "object" || Array.isArray(dialect)) return "Not verified"
  const mode = (dialect as Record<string, unknown>).mode
  return typeof mode === "string" && mode ? mode : "Not verified"
}

function indexerRequestBudget(item: APISourceStatus) {
  const query = typeof item.config.query_limit === "number" ? item.config.query_limit : undefined
  const grab = typeof item.config.grab_limit === "number" ? item.config.grab_limit : undefined
  const window = typeof item.config.budget_window_seconds === "number" ? item.config.budget_window_seconds : undefined
  if (!query || !grab || !window) return "Not commissioned"
  const windowLabel = window === 86400 ? "24h" : window === 3600 ? "1h" : `${window}s`
  return `${query.toLocaleString()} queries · ${grab.toLocaleString()} grabs / ${windowLabel}`
}

export function LiveIndexerDetailScreen({ indexerId }: { indexerId: string }) {
  const { data, error, refresh } = useLiveData(sourcesLoader)
  const item = data?.find((source) => source.id === indexerId && (source.kind === "newznab" || source.kind === "oldboys"))
  if (!data && !error) return <LoadingPanel label="Loading indexer state…" />
  if (data && !item) return <ErrorNotice error="The indexer was not found." />
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{item ? <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Configuration" value={pretty(item.configuration_state)} note="Enabled only after commissioning" tone={item.configuration_state === "configured" ? "info" : item.configuration_state === "disabled" ? "neutral" : "warn"} icon={Globe2} /><MetricCard label="Minimum interval" value={item.minimum_interval_ms ? `${item.minimum_interval_ms} ms` : "Not observed"} note="One in-flight request per indexer" icon={Gauge} /><MetricCard label="Latest outcome" value={item.last_outcome ? pretty(item.last_outcome) : "None"} note={item.budget_updated_at ? timeAgo(item.budget_updated_at) : "No runtime request recorded"} tone={!item.enabled ? "neutral" : item.last_outcome === "auth_failed" || item.last_outcome === "system_failure" ? "warn" : "neutral"} icon={Activity} /></div><div className="grid gap-4 xl:grid-cols-[1.2fr_1fr]"><Panel title={item.name} subtitle={item.kind === "oldboys" ? "Native Danish film/TV provider. Credentials stay encrypted on the server." : "Persisted Newznab configuration. Secrets remain environment references."}><KeyValue label="Endpoint" value={<span className="break-all font-mono">{sourceEndpoint(item)}</span>} /><KeyValue label="Secret reference" value={<span className="font-mono">{item.secret_ref ?? "—"}</span>} /><KeyValue label="Default" value={item.is_default ? "Yes" : "No"} /><KeyValue label="Movie search" value={item.kind === "oldboys" ? "Native film category (1)" : indexerDialect(item, "movie")} /><KeyValue label="TV search" value={item.kind === "oldboys" ? "Native TV category (2)" : indexerDialect(item, "series")} /><KeyValue label="Request ceiling" value={indexerRequestBudget(item)} /></Panel><Panel title="Pacing state" action={<RefreshButton onClick={refresh} />}><KeyValue label="Next allowed" value={item.next_allowed_at ? new Date(item.next_allowed_at).toLocaleString() : "No persisted delay"} /><KeyValue label="Cooldown" value={item.cooldown_until ? new Date(item.cooldown_until).toLocaleString() : "None"} /><KeyValue label="Consecutive failures" value={String(item.consecutive_failure)} /><InlineNotice tone="info" title="No automatic test">Opening this screen never contacts the indexer. Commissioning remains the only owner-triggered diagnostic path.</InlineNotice></Panel></div></> : null}</div>
}

export function LiveComponentHealthScreen() {
  const { data, error, refresh } = useLiveData(healthLoader, { autoRefresh: true })
  if (!data && !error) return <LoadingPanel label="Loading component health…" />
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <HealthContent data={data} refresh={refresh} /> : null}</div>
}

export function LiveTasksScreen() {
  const { data, error, refresh } = useLiveData(tasksLoader, { autoRefresh: true })
  if (!data && !error) return <LoadingPanel label="Loading persisted worker state…" />
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <TasksContent data={data} refresh={refresh} /> : null}</div>
}

function TasksContent({ data, refresh }: { data: APITasks; refresh: () => void }) {
  const pending = data.event_types.reduce((total, item) => total + item.pending, 0)
  const due = data.event_types.reduce((total, item) => total + item.due, 0)
  const retrying = data.event_types.reduce((total, item) => total + item.retrying, 0)
  return <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Pending" value={String(pending)} note="Durable outbox events" tone="info" icon={ListChecks} /><MetricCard label="Due now" value={String(due)} note="Available for a worker lease" tone={due ? "warn" : "good"} icon={Activity} /><MetricCard label="Retried" value={String(retrying)} note="Pending events with prior attempts" tone={retrying ? "warn" : "good"} icon={RefreshCw} /></div><div className="grid gap-4 xl:grid-cols-[1.35fr_1fr]"><Panel flush title="Durable event work" subtitle="Persisted event-type aggregates; opening this page never triggers a task." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Event type</TableHead><TableHead>Pending</TableHead><TableHead>Due</TableHead><TableHead>Retrying</TableHead><TableHead>Completed</TableHead><TableHead>Highest attempts</TableHead><TableHead>Latest event</TableHead></TableRow></TableHeader><TableBody>{data.event_types.map((item) => <TableRow key={item.event_type}><TableCell className="font-mono text-[12px]">{item.event_type}</TableCell><TableCell>{item.pending}</TableCell><TableCell>{item.due}</TableCell><TableCell>{item.retrying}</TableCell><TableCell>{item.completed}</TableCell><TableCell>{item.highest_attempts}</TableCell><TableCell>{timeAgo(item.latest_at)}</TableCell></TableRow>)}</TableBody></Table></div>{data.event_types.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No durable worker events have been created.</div> : null}</Panel><Panel title="Recurring work" subtitle="Configured server schedules; automatic does not claim a successful external operation.">{data.recurring.map((item) => <div key={item.name} className="mb-2 rounded-xl border border-line bg-surface-2 p-3"><div className="flex items-center justify-between gap-3"><span className="text-[12px] font-semibold text-fg">{item.name}</span><StatusChip tone={item.automatic ? "info" : "neutral"}>{item.automatic ? "Automatic" : "Disabled"}</StatusChip></div><div className="mt-1 text-[12px] text-fg-subtle">{item.schedule}</div></div>)}</Panel></div></>
}

export function LiveSystemInfoScreen({ mode }: { mode: "media-management" | "security" | "api" }) {
  const { data, error, refresh } = useLiveData(systemInfoLoader)
  if (!data && !error) return <LoadingPanel label="Loading server-enforced policy…" />
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <SystemInfoContent data={data} mode={mode} refresh={refresh} /> : null}</div>
}

export function LiveMetadataSettingsScreen() {
  const { data, error, refresh } = useLiveData(systemInfoLoader)
  if (!data && !error) return <LoadingPanel label="Loading metadata-provider state…" />
  const configured = (provider: "tmdb" | "tvdb" | "simkl") => data?.metadata_providers?.[provider] ?? "not_configured"
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <><div className="grid gap-3 sm:grid-cols-4"><MetricCard label="TMDB" value={pretty(configured("tmdb"))} note="Primary descriptions, artwork, credits, dates, genres, and rating" tone={configured("tmdb") !== "not_configured" ? "info" : "neutral"} icon={SearchCheck} /><MetricCard label="TheTVDB" value={pretty(configured("tvdb"))} note="Exact-ID TV and movie supplementary evidence" tone={configured("tvdb") === "direct_configured" ? "info" : "neutral"} icon={ShieldCheck} /><MetricCard label="Simkl" value={pretty(configured("simkl"))} note="Exact-ID ratings and lifecycle evidence" tone={configured("simkl") === "direct_configured" ? "info" : "neutral"} icon={ListChecks} /><MetricCard label="IMDb" value={pretty(data.metadata_providers?.imdb ?? "identity_only")} note="Identity and labeled Simkl rating evidence; never scraped" icon={Film} /></div><Panel title="Metadata and artwork" subtitle="Provider roles and persistence are reported without synthetic connectivity claims." action={<RefreshButton onClick={refresh} />}><KeyValue label="Primary descriptive source" value={configured("tmdb") === "direct_configured" ? "Direct TMDB API" : configured("tmdb") === "seerr_fallback" ? "TMDB response through Seerr fallback" : "Not configured"} /><KeyValue label="TV and movie supplement" value={configured("tvdb") === "direct_configured" ? "Direct TheTVDB API" : "Not configured"} /><KeyValue label="Ratings and lifecycle" value={configured("simkl") === "direct_configured" ? "Direct Simkl API" : "Not configured"} /><KeyValue label="IMDb policy" value="Verified identity and clearly labeled Simkl evidence; direct API requires a separate licensed source" /><KeyValue label="Merge gate" value="Exact verified TMDB, TVDB, IMDb, or Simkl identity; never title-only" /><KeyValue label="Stored fields" value="Overview, artwork, tagline, dates, certification, runtime, genres, keywords, studios, cast/crew, networks, aliases, ratings, events, and provider provenance" /><InlineNotice tone="info" title="Owner-triggered and bounded">A refresh performs at most one exact detail request per configured provider, plus TheTVDB login when needed and one Simkl ID-resolution request. Provider conflicts are rejected independently and cannot replace verified identities.</InlineNotice><p className="mt-3 text-[12px] text-fg-subtle">This product uses the TMDB API but is not endorsed or certified by TMDB. Simkl evidence links to its item source.</p></Panel></> : null}</div>
}

export function LiveNotificationsScreen() {
  const destinationsState = useLiveData(notificationDestinationsLoader)
  const deliveriesState = useLiveData(notificationDeliveriesLoader)
  const [name, setName] = React.useState("")
  const [endpoint, setEndpoint] = React.useState("")
  const [secretRef, setSecretRef] = React.useState("")
  const [events, setEvents] = React.useState("workflow.created, workflow.recycled, collection.created")
  const [allowHTTP, setAllowHTTP] = React.useState(false)
  const [busy, setBusy] = React.useState("")
  async function create(event: React.FormEvent) {
    event.preventDefault(); setBusy("create")
    try { await createNotificationDestination({ name: name.trim(), endpoint: endpoint.trim(), allow_http: allowHTTP, enabled: true, events: events.split(",").map((item) => item.trim()).filter(Boolean), ...(secretRef.trim() ? { secret_ref: secretRef.trim() } : {}) }); toast.success("Notification destination persisted"); setName(""); setEndpoint(""); setSecretRef(""); destinationsState.refresh() }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not create destination") }
    finally { setBusy("") }
  }
  async function toggle(id: string, enabled: boolean) { setBusy(id); try { await setNotificationDestinationEnabled(id, enabled); toast.success(enabled ? "Destination enabled" : "Destination disabled"); destinationsState.refresh() } catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not update destination") } finally { setBusy("") } }
  const destinations = destinationsState.data ?? []
  const deliveries = deliveriesState.data ?? []
  const error = destinationsState.error || deliveriesState.error
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}<div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Destinations" value={String(destinations.length)} note={`${destinations.filter((item) => item.enabled).length} enabled`} icon={Globe2} /><MetricCard label="Delivered" value={String(deliveries.filter((item) => item.state === "delivered").length)} note="Bounded durable history" tone="good" icon={CheckCircle2} /><MetricCard label="Failed" value={String(deliveries.filter((item) => item.state === "failed").length)} note="Worker retries remain visible" tone={deliveries.some((item) => item.state === "failed") ? "warn" : "good"} icon={AlertTriangle} /></div><div className="grid gap-4 xl:grid-cols-[1.3fr_380px]"><Panel flush title="Notification destinations" subtitle="Payloads contain event identity and object references, never audit detail or secret values." action={<RefreshButton onClick={() => { destinationsState.refresh(); deliveriesState.refresh() }} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Endpoint</TableHead><TableHead>Events</TableHead><TableHead>Secret reference</TableHead><TableHead>State</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{destinations.map((item) => <TableRow key={item.id}><TableCell className="font-semibold">{item.name}</TableCell><TableCell className="max-w-sm break-all font-mono text-[12px]">{item.endpoint}</TableCell><TableCell className="max-w-sm text-[12px]">{item.events.join(" · ")}</TableCell><TableCell className="font-mono text-[12px]">{item.secret_ref || "—"}</TableCell><TableCell><StateBadge state={item.enabled ? "enabled" : "disabled"} /></TableCell><TableCell><Button size="sm" variant="outline" disabled={busy === item.id} onClick={() => void toggle(item.id, !item.enabled)}>{item.enabled ? "Disable" : "Enable"}</Button></TableCell></TableRow>)}</TableBody></Table></div>{destinations.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No notification destinations configured.</div> : null}</Panel><Panel title="Add owner webhook" subtitle="Saving does not send a test message. HTTPS is required unless local HTTP is explicitly allowed."><form className="grid gap-3" onSubmit={create}><label className="grid gap-1.5 text-[12.5px] font-medium text-fg-secondary">Name<Input className="mm-input" value={name} onChange={(event) => setName(event.target.value)} required /></label><label className="grid gap-1.5 text-[12.5px] font-medium text-fg-secondary">Webhook endpoint<Input className="mm-input" type="url" value={endpoint} onChange={(event) => setEndpoint(event.target.value)} placeholder="https://notify.example/hook" required /></label><label className="grid gap-1.5 text-[12.5px] font-medium text-fg-secondary">Events<Input className="mm-input" value={events} onChange={(event) => setEvents(event.target.value)} required /></label><label className="grid gap-1.5 text-[12.5px] font-medium text-fg-secondary">Bearer secret environment name<Input className="mm-input font-mono" value={secretRef} onChange={(event) => setSecretRef(event.target.value)} placeholder="Optional" /></label><label className="flex items-center gap-2 text-[12px] text-fg-muted"><input type="checkbox" checked={allowHTTP} onChange={(event) => setAllowHTTP(event.target.checked)} />Allow plain HTTP for an explicitly local destination</label><Button type="submit" disabled={busy === "create"}>Save destination</Button></form></Panel></div><Panel flush title="Delivery history" subtitle="Durable attempts and bounded, redacted event payloads."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Created</TableHead><TableHead>Destination</TableHead><TableHead>Event</TableHead><TableHead>Object</TableHead><TableHead>State</TableHead><TableHead>Attempts</TableHead></TableRow></TableHeader><TableBody>{deliveries.map((item) => <TableRow key={item.id}><TableCell>{new Date(item.created_at).toLocaleString()}</TableCell><TableCell>{item.destination}</TableCell><TableCell className="font-mono text-[12px]">{item.payload.action}</TableCell><TableCell>{item.payload.aggregate_type}</TableCell><TableCell><StateBadge state={item.state} label={item.state === "delivered" ? "Delivered" : undefined} />{item.response_status ? ` · ${item.response_status}` : ""}{item.last_error ? <div className="mt-1 text-[12px] text-warn">{item.last_error}</div> : null}</TableCell><TableCell>{item.attempts}</TableCell></TableRow>)}</TableBody></Table></div>{deliveries.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No delivery attempts have been queued.</div> : null}</Panel></div>
}

export function LiveImportListsScreen() {
  const listsState = useLiveData(importListsLoader)
  const [selectedID, setSelectedID] = React.useState("")
  const lists = listsState.data ?? []
  const effectiveSelectedID = selectedID || lists[0]?.id || ""
  const selectedLoader = React.useCallback((signal: AbortSignal) => effectiveSelectedID ? loadImportList(effectiveSelectedID, signal) : Promise.resolve(null), [effectiveSelectedID])
  const selectedState = useLiveData<APIImportList | null>(selectedLoader)
  const [name, setName] = React.useState("")
  const [file, setFile] = React.useState<File | null>(null)
  const [busy, setBusy] = React.useState("")
  async function create(event: React.FormEvent) {
    event.preventDefault(); setBusy("create")
    try { const created = await createImportList({ name: name.trim(), provider: "imdb_export", enabled: true }); toast.success("Import list persisted"); setName(""); setSelectedID(created.id); listsState.refresh() }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not create import list") }
    finally { setBusy("") }
  }
  async function upload(event: React.FormEvent) {
    event.preventDefault(); if (!effectiveSelectedID || !file) return; setBusy("upload")
    try { await uploadImportListSnapshot(effectiveSelectedID, file); toast.success("IMDb snapshot persisted for review; no monitoring changed"); setFile(null); selectedState.refresh(); listsState.refresh() }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not review IMDb export") }
    finally { setBusy("") }
  }
  const selected = selectedState.data
  const snapshot = selected?.latest_snapshot
  const items = snapshot?.items ?? []
  const error = listsState.error || selectedState.error
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}<InlineNotice tone="info" title="Review only">IMDb exports are parsed into persisted review rows. Uploading a snapshot creates no media, workflows, searches, release evidence, or downloads. TV rows remain unsupported until an exact season can be chosen.</InlineNotice><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Lists" value={String(lists.length)} note="Owner-supplied IMDb exports" icon={ListChecks} /><MetricCard label="Pending review" value={String(snapshot?.pending_count ?? 0)} note="No monitoring change has been applied" tone={snapshot?.pending_count ? "warn" : "neutral"} icon={SearchCheck} /><MetricCard label="Already cataloged" value={String(snapshot?.existing_count ?? 0)} note={`${snapshot?.unsupported_count ?? 0} unsupported rows`} tone="info" icon={CheckCircle2} /></div><div className="grid gap-4 xl:grid-cols-[1.3fr_380px]"><Panel flush title="Import lists" subtitle="Persisted source configuration and latest bounded snapshot counts." action={<RefreshButton onClick={listsState.refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Source</TableHead><TableHead>Movie profile</TableHead><TableHead>Latest snapshot</TableHead><TableHead>Items</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{lists.map((item) => <TableRow key={item.id}><TableCell className="font-semibold">{item.name}</TableCell><TableCell>IMDb CSV export</TableCell><TableCell>{item.movie_quality_profile || "Type default"}</TableCell><TableCell>{item.latest_snapshot ? timeAgo(item.latest_snapshot.created_at) : "Never"}</TableCell><TableCell>{item.latest_snapshot?.item_count ?? 0}</TableCell><TableCell><Button size="sm" variant={effectiveSelectedID === item.id ? "default" : "outline"} onClick={() => setSelectedID(item.id)}>Review</Button></TableCell></TableRow>)}</TableBody></Table></div>{lists.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No import lists configured.</div> : null}</Panel><div className="space-y-4"><Panel title="Add IMDb export list" subtitle="This creates configuration only; it does not fetch or import anything."><form className="grid gap-3" onSubmit={create}><label className="grid gap-1.5 text-[12.5px] font-medium text-fg-secondary">Name<Input className="mm-input" value={name} onChange={(event) => setName(event.target.value)} placeholder="IMDb owner watchlist" required /></label><KeyValue label="Provider" value="IMDb CSV export" /><KeyValue label="Monitoring defaults" value="Movie type defaults; search off" /><Button type="submit" disabled={busy === "create"}>Save list</Button></form></Panel><Panel title="Review a new snapshot" subtitle={selected ? `Selected: ${selected.name}` : "Select or create a list first."}><form className="grid gap-3" onSubmit={upload}><Input aria-label="IMDb CSV snapshot" className="mm-input" type="file" accept=".csv,text/csv" onChange={(event) => setFile(event.target.files?.[0] ?? null)} disabled={!selected} /><Button type="submit" disabled={!selected || !file || busy === "upload"}>Parse snapshot for review</Button></form></Panel></div></div><Panel flush title={selected ? `${selected.name} · latest review` : "Latest review"} subtitle={snapshot ? `${snapshot.item_count} rows persisted ${new Date(snapshot.created_at).toLocaleString()}. Digest ${snapshot.source_digest.slice(0, 12)}…` : "No snapshot has been uploaded."}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>#</TableHead><TableHead>Title</TableHead><TableHead>Identity</TableHead><TableHead>Kind</TableHead><TableHead>Review state</TableHead><TableHead>Attention</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell>{item.ordinal}</TableCell><TableCell className="font-semibold">{item.title}{item.year ? ` (${item.year})` : ""}</TableCell><TableCell className="font-mono text-[12px]">IMDb {item.provider_id}</TableCell><TableCell>{pretty(item.media_kind)}</TableCell><TableCell><StateBadge state={item.state} /></TableCell><TableCell className="max-w-md text-[12px]">{item.attention || (item.state === "pending_review" ? "Awaiting an implemented monitoring review action." : "—")}</TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No persisted review rows.</div> : null}</Panel></div>
}

export function LiveBackupsScreen() {
  const { data, error, refresh } = useLiveData(backupsLoader)
  const [busy, setBusy] = React.useState(false)
  const [offsiteBusy, setOffsiteBusy] = React.useState<string | null>(null)
  async function create() {
    setBusy(true)
    try { await createBackup(); toast.success("Encrypted backup queued"); refresh() }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not queue backup") }
    finally { setBusy(false) }
  }
  async function copyOffsite(id: string) {
    setOffsiteBusy(id)
    try { await retryBackupOffsite(id); toast.success("Off-site copy queued"); refresh() }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not queue off-site copy") }
    finally { setOffsiteBusy(null) }
  }
  if (!data && !error) return <LoadingPanel label="Loading durable backup records…" />
  const records = data?.records ?? []
  const verified = records.filter((item) => item.state === "verified")
  const active = records.some((item) => item.state === "queued" || item.state === "running")
  const latest = records[0]
  const offsiteCopied = verified.filter((item) => item.offsite_state === "uploaded").length
  const latestOffsite = verified.map((item) => item.offsite_uploaded_at).filter((value): value is string => Boolean(value)).sort().at(-1)
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Verified backups" value={String(verified.length)} note="Durable records with restore verification" tone={verified.length ? "good" : "neutral"} icon={DatabaseBackup} /><MetricCard label="Latest state" value={latest ? pretty(latest.state) : "No records"} note={latest ? timeAgo(latest.created_at) : "No backup has been requested"} tone={latest?.state === "verified" ? "good" : latest?.state === "failed" ? "warn" : latest ? "info" : "neutral"} icon={Database} /><MetricCard label="Encryption key" value={data.configured ? "Configured" : "Unavailable"} note="External BACKUP_ENCRYPTION_KEY; value never returned" tone={data.configured ? "good" : "warn"} icon={ShieldCheck} /></div><InlineNotice tone={data.configured ? "info" : "warn"} title={data.configured ? "Verified encrypted backups" : "Backup creation is disabled"}>{data.configured ? "Each PostgreSQL custom dump is encrypted with chunked AES-256-GCM, checksummed, restored into an isolated temporary database, and accepted only at the exact running schema version." : "Configure a base64-encoded 32-byte BACKUP_ENCRYPTION_KEY outside the repository, then recreate only the API service. Existing .env files and integration secret values are never included in backups."}</InlineNotice><Panel flush title="Media Manager backups" subtitle="Database, configuration references, and audit state are included. Secret values, media, AltMount content, and .env files are excluded." action={<div className="flex gap-2"><RefreshButton onClick={refresh} /><Button size="sm" disabled={!data.configured || busy || active} onClick={() => void create()}><DatabaseBackup />Create and verify</Button></div>}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Requested</TableHead><TableHead>State</TableHead><TableHead>Size</TableHead><TableHead>Schema</TableHead><TableHead>Attempts</TableHead><TableHead>Integrity</TableHead><TableHead>Download</TableHead></TableRow></TableHeader><TableBody>{records.map((item) => <TableRow key={item.id}><TableCell>{new Date(item.created_at).toLocaleString()}</TableCell><TableCell><StateBadge state={item.state} />{item.last_error ? <div className="mt-1 text-[12px] text-warn">{item.last_error}</div> : null}</TableCell><TableCell>{formatBytes(item.size_bytes)}</TableCell><TableCell>{item.schema_version ?? "—"}</TableCell><TableCell>{item.attempts}</TableCell><TableCell className="font-mono text-[12px]">{item.sha256 ? `${item.sha256.slice(0, 16)}…` : "—"}</TableCell><TableCell>{item.state === "verified" ? <Button asChild size="sm" variant="outline"><a href={`/api/v1/backups/${encodeURIComponent(item.id)}/download`}><Download />Encrypted file</a></Button> : "—"}</TableCell></TableRow>)}</TableBody></Table></div>{records.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No backup has been requested. No success state is inferred from the project tar archive.</div> : null}</Panel><Panel flush title="Off-site copies" subtitle="Encrypted, restore-verified artifacts copied to the configured S3-compatible or WebDAV destination. The destination never receives a plaintext dump." action={<RefreshButton onClick={refresh} />}><div className="grid gap-3 border-b border-line p-4 sm:grid-cols-3"><MetricCard label="Destination" value={data.offsite?.configured ? pretty(data.offsite.kind) : "Not configured"} note={data.offsite?.configured ? (data.offsite.prefix || "Destination root") : "Set BACKUP_OFFSITE_KIND outside the repository"} tone={data.offsite?.configured ? "good" : "warn"} icon={ShieldCheck} /><MetricCard label="Copied" value={`${offsiteCopied} of ${verified.length}`} note="Verified backups with an off-site copy" tone={offsiteCopied === verified.length && verified.length > 0 ? "good" : offsiteCopied ? "info" : "neutral"} icon={DatabaseBackup} /><MetricCard label="Latest copy" value={latestOffsite ? timeAgo(latestOffsite) : "None"} note="Uploaded only after local verification" tone={latestOffsite ? "good" : "neutral"} icon={Database} /></div><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Verified backup</TableHead><TableHead>Off-site state</TableHead><TableHead>Object</TableHead><TableHead>Copied</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{verified.map((item) => <TableRow key={`offsite-${item.id}`}><TableCell>{new Date(item.created_at).toLocaleString()}</TableCell><TableCell><StatusChip tone={item.offsite_state === "uploaded" ? "good" : item.offsite_state === "failed" ? "warn" : "neutral"}>{item.offsite_state === "uploaded" ? "Copied" : item.offsite_state === "failed" ? "Failed" : "Not copied"}</StatusChip>{item.offsite_error ? <div className="mt-1 max-w-sm text-[12px] text-warn">{item.offsite_error}</div> : null}</TableCell><TableCell className="max-w-sm break-all font-mono text-[12px]">{item.offsite_key || "—"}</TableCell><TableCell>{item.offsite_uploaded_at ? timeAgo(item.offsite_uploaded_at) : "—"}</TableCell><TableCell>{data.offsite?.configured ? <Button size="sm" variant="outline" disabled={offsiteBusy === item.id || item.offsite_state === "uploaded"} onClick={() => void copyOffsite(item.id)}>{offsiteBusy === item.id ? "Queueing…" : item.offsite_state === "failed" ? "Retry copy" : "Copy off-site"}</Button> : <span className="text-[12px] text-fg-subtle">Not configured</span>}</TableCell></TableRow>)}</TableBody></Table></div>{verified.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">A verified local backup is required before it can be copied off-site.</div> : null}</Panel><div className="grid gap-4 xl:grid-cols-2"><Panel title="Restore verification" subtitle="A verified record is more than a successful dump command."><KeyValue label="Format" value={data.format} /><KeyValue label="Verification" value={data.restore_verification} /><KeyValue label="Production restore" value={data.production_restore ? "Available" : "Not implemented"} /></Panel><Panel title="Retention and safety" subtitle="No backup is deleted automatically without an approved retention policy."><KeyValue label="Automatic deletion" value={data.automatic_deletion ? "Enabled" : "Disabled"} /><KeyValue label="Media contents" value="Excluded" /><KeyValue label="Secret values and .env" value="Excluded" /></Panel></div></> : null}</div>
}

function SystemInfoContent({ data, mode, refresh }: { data: APISystemInfo; mode: "media-management" | "security" | "api"; refresh: () => void }) {
  if (mode === "media-management") return <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Source policy" value={pretty(data.altmount_source_policy)} note="AltMount content is never renamed or deleted" tone="good" icon={HardDrive} /><MetricCard label="Publication" value={pretty(data.publication_type)} note="Plex-safe managed link generations" tone="info" icon={FileCheck2} /><MetricCard label="Recycle retention" value={`${data.recycle_retention_days} days`} note="Managed links stay recoverable" icon={RefreshCw} /></div><Panel title="Enforced lifecycle policy" subtitle="These values report the running server contract; unsupported settings are not presented as editable."><KeyValue label="Upgrade replacement gate" value={pretty(data.upgrade_replacement_gate)} /><KeyValue label="AltMount source" value={pretty(data.altmount_source_policy)} /><KeyValue label="Publication type" value={pretty(data.publication_type)} /><KeyValue label="Recycle retention" value={`${data.recycle_retention_days} days`} /><InlineNotice tone="info" title="Safe replacement">An old current link is displaced only after the replacement is verified and Plex confirms exact path and provider identity.</InlineNotice></Panel></>
  if (mode === "security") return <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Administration" value={data.owner_only ? "Owner only" : "Not owner-only"} note="No multi-user administration" tone={data.owner_only ? "good" : "warn"} icon={ShieldCheck} /><MetricCard label="Browser boundary" value={pretty(data.browser_auth)} note={`Public edge: ${data.public_edge_owner}`} tone="info" icon={Globe2} /><MetricCard label="Webhook" value={data.seerr_webhook_enabled ? "Enabled" : "Disabled"} note="Seerr uses its separate authenticated route" icon={SearchCheck} /></div><Panel title="Running access boundaries" subtitle="Secret values are never returned by this endpoint." action={<RefreshButton onClick={refresh} />}><KeyValue label="Owner API authentication" value={pretty(data.api_auth)} /><KeyValue label="Browser authentication" value={pretty(data.browser_auth)} /><KeyValue label="Public edge owner" value={data.public_edge_owner} /><KeyValue label="Machine API keys" value={data.machine_api_keys ? "Enabled" : "Not implemented"} /><InlineNotice tone="info" title="Secret handling">Integration records expose environment variable names only. Credential values remain outside browser responses and durable audit detail.</InlineNotice></Panel></>
  return <div className="grid gap-4 xl:grid-cols-[1fr_1.2fr]"><Panel title="Owner API contract" subtitle="The live API is versioned and protected by the owner boundary." action={<RefreshButton onClick={refresh} />}><KeyValue label="Version" value={data.version || "Unversioned development build"} /><KeyValue label="Prefix" value={<span className="font-mono">{data.api_prefix}</span>} /><KeyValue label="Authentication" value={pretty(data.api_auth)} /><KeyValue label="Machine API keys" value={data.machine_api_keys ? "Enabled" : "Not implemented"} /></Panel><Panel title="Supported access" subtitle="Browser requests use the gateway-injected owner bearer. External scoped keys are not claimed."><pre className="overflow-x-auto rounded-xl border border-line bg-canvas p-4 text-[12px] leading-5 text-fg-secondary">{`GET ${data.api_prefix}/dashboard\nGET ${data.api_prefix}/media\nGET ${data.api_prefix}/workflows\nGET ${data.api_prefix}/wanted\nGET ${data.api_prefix}/files\nGET ${data.api_prefix}/activity\nGET ${data.api_prefix}/system/health\nGET ${data.api_prefix}/system/tasks`}</pre><InlineNotice tone="warn" title="No machine-key lifecycle">Scoped machine keys, issuance, rotation, and revocation are not implemented. Do not reuse the internal owner bearer outside the gateway.</InlineNotice></Panel></div>
}

function HealthTile({ ok, idle = false, title, children }: { ok: boolean; idle?: boolean; title: string; children: React.ReactNode }) {
  return <div className="rounded-[11px] border border-line bg-surface p-4">
    <h4 className="mb-1.5 flex items-center gap-2 text-[12.5px] font-bold text-fg"><span className={cn("size-2 shrink-0 rounded-full", idle ? "bg-fg-subtle" : ok ? "bg-good-solid" : "bg-warn-solid")} aria-hidden />{title}<span className="sr-only">{idle ? "disabled" : ok ? "healthy" : "needs attention"}</span></h4>
    <div className="text-[11.5px] leading-[18px] text-fg-subtle">{children}</div>
  </div>
}

function HealthContent({ data, refresh }: { data: APISystemHealth; refresh: () => void }) {
  return <><div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
    <HealthTile ok={data.database_state === "ready" && data.schema_version === data.expected_schema} title="PostgreSQL">{pretty(data.database_state)} · schema {data.schema_version}/{data.expected_schema} · {data.database_latency_ms} ms</HealthTile>
    <HealthTile ok={!(data.outbox.due > 0 && data.outbox.retrying > 0)} title="Worker">{data.outbox.pending} pending · {data.outbox.due} due · {data.outbox.retrying} retried</HealthTile>
    {(data.mounts ?? []).map((mount) => <HealthTile key={`mount-${mount.integration_id}`} ok={mount.state === "available"} title={`${mount.name} output mount`}>{mount.path} · {mount.state === "available" ? "readable" : mount.detail ?? "unavailable"}</HealthTile>)}
    {data.components.map((component) => <HealthTile key={component.id} ok={component.configuration_state === "configured" && component.consecutive_failure === 0} idle={!component.enabled} title={component.name}>{component.kind} · {pretty(component.configuration_state)}{component.last_outcome ? ` · ${pretty(component.last_outcome)}` : ""}{component.consecutive_failure ? ` · ${component.consecutive_failure} failures` : ""}</HealthTile>)}
  </div><Panel flush title="Component state" subtitle="Configuration and last observed operations; this endpoint does not manufacture connection-test success." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Component</TableHead><TableHead>Type</TableHead><TableHead>Configuration</TableHead><TableHead>Latest observed outcome</TableHead><TableHead>Updated</TableHead></TableRow></TableHeader><TableBody><TableRow><TableCell className="font-semibold">PostgreSQL</TableCell><TableCell>database</TableCell><TableCell><StateBadge state="ready" /></TableCell><TableCell>Schema and query ready</TableCell><TableCell>{new Date(data.checked_at).toLocaleTimeString()}</TableCell></TableRow>{data.components.map((item) => <TableRow key={item.id}><TableCell className="font-semibold">{item.name}</TableCell><TableCell>{item.kind}</TableCell><TableCell><StateBadge state={item.configuration_state === "configured" ? "configured" : item.configuration_state} /></TableCell><TableCell>{item.last_outcome ? pretty(item.last_outcome) : "No live operation observed"}</TableCell><TableCell>{item.budget_updated_at ? timeAgo(item.budget_updated_at) : timeAgo(item.updated_at)}</TableCell></TableRow>)}</TableBody></Table></div></Panel><Panel flush title="Advanced tools" subtitle="Intentionally out of the everyday path.">
    {[{ href: "/activity/history", title: "History & evidence", note: "Raw workflow events and state-machine transitions" }, { href: "/intelligence/evidence", title: "Evidence explorer", note: "Release evidence behind every decision" }, { href: "/activity/blocklist", title: "Blocklist", note: "Blocked releases and release groups" }, { href: "/intelligence/releases", title: "Release intelligence", note: "Candidates, verdicts, and scores" }, { href: "/system/logs", title: "Logs & audit", note: "Owner and worker audit trail" }, { href: "/system/tasks", title: "Tasks · Backups · Security · API", note: "Operational configuration" }].map((tool) => <Link key={tool.href} href={tool.href} className="flex items-center gap-3 border-t border-line px-4 py-[11px] outline-none transition first:border-t-0 hover:bg-surface-2/60 focus-visible:bg-surface-2"><b className="text-[12.5px] font-semibold text-fg">{tool.title}</b><small className="truncate text-[11px] text-fg-subtle">{tool.note}</small><ChevronRight className="ml-auto size-4 shrink-0 text-fg-subtle" aria-hidden /></Link>)}
  </Panel></>
}
