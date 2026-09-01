"use client"

import * as React from "react"
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
  FileCheck2,
  Files,
  Film,
  Gauge,
  Grid2X2,
  Globe2,
  HardDrive,
  HeartPulse,
  Library,
  List,
  ListChecks,
  Plus,
  RefreshCw,
  Search,
  SearchCheck,
  ShieldCheck,
  SlidersHorizontal,
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
  loadDashboard,
  loadCatalogMedia,
  loadManagedFile,
  loadManagedFiles,
  loadNotificationDeliveries,
  loadNotificationDestinations,
  loadImportList,
  loadImportLists,
  loadBackups,
  loadReleaseCandidates,
  loadReleaseBlocks,
  loadSources,
  loadSystemHealth,
  loadSystemInfo,
  loadTasks,
  loadWanted,
  loadWorkflows,
  refreshMediaMetadata,
  setNotificationDestinationEnabled,
  uploadImportListSnapshot,
  type APIActivity,
  type APIReleaseCandidate,
  type APISourceStatus,
  type APISystemHealth,
  type APISystemInfo,
  type APITasks,
  type APIImportList,
} from "@/lib/media-manager-api"
import { cn } from "@/lib/utils"
import {
  buildMovieLibraryRecords,
  filterMovieLibraryRecords,
  initialMovieFilters,
  uniqueValues,
  type MovieLibraryFilters,
  type MovieLibraryRecord,
  type MovieLibrarySummary,
} from "@/lib/movie-library"
import { MetadataArtwork, MetadataSourceLink } from "./metadata-artwork"
import { InlineNotice, KeyValue, MetricCard, Panel, StatusChip } from "./ui"

export { buildMovieLibraryRecords, filterMovieLibraryRecords } from "@/lib/movie-library"
export type { MovieLibraryFilters, MovieLibraryRecord } from "@/lib/movie-library"

type Tone = "good" | "info" | "warn" | "bad" | "neutral"

function useLiveData<T>(loader: (signal: AbortSignal) => Promise<T>) {
  const [data, setData] = React.useState<T | null>(null)
  const [error, setError] = React.useState("")
  const [revision, setRevision] = React.useState(0)
  React.useEffect(() => {
    const controller = new AbortController()
    loader(controller.signal)
      .then((next) => { setData(next); setError("") })
      .catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Could not load live data") })
    return () => controller.abort()
  }, [loader, revision])
  return { data, error, refresh: () => setRevision((current) => current + 1) }
}

function LoadingPanel({ label = "Loading persisted state…" }: { label?: string }) {
  return <Panel><div className="py-12 text-center text-[11px] text-[#7f8a9a]">{label}</div></Panel>
}

function ErrorNotice({ error }: { error: string }) {
  return <InlineNotice tone="bad" title="Live data unavailable">{error}</InlineNotice>
}

function RefreshButton({ onClick }: { onClick: () => void }) {
  return <Button variant="outline" size="sm" onClick={onClick}><RefreshCw /> Refresh</Button>
}

function pretty(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase())
}

function timeAgo(value: string) {
  const seconds = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 1000))
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
  return `${Math.floor(seconds / 86400)}d ago`
}

function workflowTone(state: string): Tone {
  if (state === "plex_confirmed") return "good"
  if (["needs_attention", "held", "blocked", "quarantined", "transport_unknown"].includes(state)) return "warn"
  if (state === "cancelled") return "bad"
  if (["searching", "submitting", "queued", "streaming_ready", "verifying", "publishing", "plex_scanning"].includes(state)) return "info"
  return "neutral"
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

const dashboardLoader = (signal: AbortSignal) => loadDashboard(signal)
const activityLoader = (signal: AbortSignal) => loadActivity(signal)
const calendarLoader = (signal: AbortSignal) => loadCalendar(signal)
const filesLoader = (signal: AbortSignal) => loadManagedFiles(signal)
const releasesLoader = (signal: AbortSignal) => loadReleaseCandidates(signal)
const blocksLoader = (signal: AbortSignal) => loadReleaseBlocks(signal)
const sourcesLoader = (signal: AbortSignal) => loadSources(signal)
const healthLoader = (signal: AbortSignal) => loadSystemHealth(signal)
const wantedLoader = (signal: AbortSignal) => loadWanted(signal)
const tasksLoader = (signal: AbortSignal) => loadTasks(signal)
const systemInfoLoader = (signal: AbortSignal) => loadSystemInfo(signal)
const notificationDestinationsLoader = (signal: AbortSignal) => loadNotificationDestinations(signal)
const notificationDeliveriesLoader = (signal: AbortSignal) => loadNotificationDeliveries(signal)
const importListsLoader = (signal: AbortSignal) => loadImportLists(signal)
const backupsLoader = (signal: AbortSignal) => loadBackups(signal)

export function LiveOperationalDashboardScreen() {
  const { data, error, refresh } = useLiveData(dashboardLoader)
  if (!data && !error) return <LoadingPanel label="Loading the live overview…" />
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    {data ? <>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard label="Library" value={String(data.counts.movies + data.counts.series)} note={`${data.counts.movies} movies · ${data.counts.series} series`} icon={Library} />
        <MetricCard label="Managed files" value={String(data.counts.current_files)} note="Plex-confirmed current links" tone="good" icon={FileCheck2} />
        <MetricCard label="Active work" value={String(data.counts.active_workflows)} note={`${data.counts.pending_events} persisted worker events`} tone="info" icon={Activity} />
        <MetricCard label="Needs attention" value={String(data.counts.needs_attention)} note={`${data.counts.recycle_entries} retained recycle entries`} tone={data.counts.needs_attention ? "warn" : "good"} icon={AlertTriangle} />
      </div>
      <div className="grid gap-4 xl:grid-cols-[1.35fr_1fr]">
        <Panel className="!p-0" title="Recent workflows" subtitle="Persisted lifecycle state; no invented percentages." action={<RefreshButton onClick={refresh} />}>
          <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Media</TableHead><TableHead>Scope</TableHead><TableHead>State</TableHead><TableHead>Updated</TableHead></TableRow></TableHeader><TableBody>{data.recent_workflows.map((workflow) => <TableRow key={workflow.id}><TableCell><Link className="font-semibold text-[#e5eaf0] hover:text-[#aebdff]" href={workflow.media_kind === "movie" ? `/media/movies/${workflow.media_id}` : `/media/series/${workflow.media_id}`}>{workflow.title}</Link></TableCell><TableCell className="font-mono text-[9px]">{workflow.variant}</TableCell><TableCell><StatusChip tone={workflowTone(workflow.state)}>{pretty(workflow.state)}</StatusChip></TableCell><TableCell>{timeAgo(workflow.updated_at)}</TableCell></TableRow>)}</TableBody></Table></div>
          {data.recent_workflows.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No workflows have been created.</div> : null}
        </Panel>
        <Panel title="Recent durable activity" subtitle="Audit writes and workflow transitions.">
          <ActivityList items={data.recent_activity} />
          <Button asChild variant="outline" size="sm" className="mt-4"><Link href="/activity/history">Open full history</Link></Button>
        </Panel>
      </div>
    </> : null}
  </div>
}

export const movieLibraryLoader = async (signal: AbortSignal) => {
  const [media, files, wanted, workflows] = await Promise.all([
    loadCatalogMedia(signal),
    loadManagedFiles(signal),
    loadWanted(signal),
    loadWorkflows(false, signal),
  ])
  return buildMovieLibraryRecords(media, files, wanted, workflows)
}

function resolutionLabel(value: string) {
  return value.toLocaleLowerCase() === "2160p" ? "4K" : value
}

function compactValues(values: string[], limit = 6) {
  if (values.length <= limit) return values.join(", ")
  return `${values.slice(0, limit).join(", ")} +${values.length - limit}`
}

function movieAvailability(item: MovieLibraryRecord) {
  if (item.available && item.upgradable) return { label: "Available · upgrade wanted", tone: "text-[#63d29f]", dot: "bg-[#54c48e]" }
  if (item.available) return { label: "Available", tone: "text-[#63d29f]", dot: "bg-[#54c48e]" }
  if (item.missing) return { label: "Missing", tone: "text-[#e89b65]", dot: "bg-[#df8752]" }
  if (item.monitoring) return { label: "Monitoring", tone: "text-[#b59af7]", dot: "bg-[#8b5cf6]" }
  return { label: "Cataloged", tone: "text-[#929dac]", dot: "bg-[#667085]" }
}

export function LiveLibraryScreen() {
  const [filters, setFilters] = React.useState<MovieLibraryFilters>(initialMovieFilters)
  const [view, setView] = React.useState<"grid" | "list">("grid")
  const [moreOpen, setMoreOpen] = React.useState(false)
  const [page, setPage] = React.useState(1)
  const [selectedID, setSelectedID] = React.useState<string | null | undefined>(undefined)
  const [detailTab, setDetailTab] = React.useState("overview")
  const { data, error, refresh } = useLiveData(movieLibraryLoader)
  const records = React.useMemo(() => data ?? [], [data])
  const filtered = React.useMemo(() => filterMovieLibraryRecords(records, filters), [records, filters])
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
  const pageSize = 10
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize))
  const currentPage = Math.min(page, pageCount)
  const visible = filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  const requestedSelection = selectedID === undefined ? filtered[0] : records.find((item) => item.id === selectedID)
  const selected = selectedID === null ? null : requestedSelection && filtered.some((item) => item.id === requestedSelection.id) ? requestedSelection : filtered[0] ?? null

  function setFilter<K extends keyof MovieLibraryFilters>(key: K, value: MovieLibraryFilters[K]) {
    setFilters((current) => ({ ...current, [key]: value }))
    setPage(1)
    setDetailTab("overview")
  }

  function selectMovie(id: string) {
    setSelectedID(id)
    setDetailTab("overview")
  }

  const summaryItems: Array<{ key: MovieLibrarySummary; label: string; count: number }> = [
    { key: "all", label: "All Movies", count: counts.all },
    { key: "available", label: "Available", count: counts.available },
    { key: "missing", label: "Missing", count: counts.missing },
    { key: "monitoring", label: "Monitoring", count: counts.monitoring },
    { key: "upgradable", label: "Upgradable", count: counts.upgradable },
  ]

  return <div className={cn("mm-library-root grid h-full min-h-0 bg-[#080b12]", selected && "xl:grid-cols-[minmax(0,1fr)_245px]")}>
    <section className="flex min-h-0 min-w-0 flex-col px-3 pb-12 pt-3 sm:px-4 sm:pb-3 sm:pt-3" aria-labelledby="movie-library-title">
      <div className="mb-2 flex min-h-8 items-center gap-2">
        <div className="min-w-0">
          <div className="flex items-baseline gap-2">
            <h1 id="movie-library-title" className="text-[18px] font-[800] tracking-[-0.025em] text-[#f4f6fa]">Movies</h1>
            <span className="text-[10px] font-semibold text-[#929dac]" aria-label={`${records.length} movies`}>{records.length}</span>
          </div>
        </div>
        <div className="ml-auto flex items-center gap-1">
          <Button variant="ghost" size="icon-sm" aria-label="Refresh movies" onClick={refresh}><RefreshCw /></Button>
          <div className="flex rounded-md border border-[#202a39] bg-[#0e141e] p-0.5" aria-label="Library view">
            <button type="button" aria-label="Grid view" aria-pressed={view === "grid"} onClick={() => setView("grid")} className={cn("grid size-7 place-items-center rounded-[4px] text-[#7e8998] outline-none focus-visible:ring-2 focus-visible:ring-[#7c3aed]", view === "grid" && "bg-[#25203a] text-[#bca7ff]")}><Grid2X2 className="size-3.5" /></button>
            <button type="button" aria-label="List view" aria-pressed={view === "list"} onClick={() => setView("list")} className={cn("grid size-7 place-items-center rounded-[4px] text-[#7e8998] outline-none focus-visible:ring-2 focus-visible:ring-[#7c3aed]", view === "list" && "bg-[#25203a] text-[#bca7ff]")}><List className="size-3.5" /></button>
          </div>
          <Button asChild size="sm" className="h-8 bg-[#7c3aed] px-2.5 text-white hover:bg-[#6d2fd4]"><Link href="/discover"><Plus /> Add Movie</Link></Button>
        </div>
      </div>

      {error ? <div className="mb-2 flex items-center justify-between gap-3 rounded-md border border-[#5c3339] bg-[#211419] px-3 py-2 text-[10px] text-[#f1a6a6]" role="alert"><span>{error}</span><button type="button" className="font-bold underline underline-offset-2" onClick={refresh}>Retry</button></div> : null}

      <div className="relative mb-2 shrink-0">
        <div className="flex gap-1.5 overflow-x-auto pb-0.5 scrollbar-thin" aria-label="Movie filters">
          <label className="relative block w-[140px] shrink-0"><span className="sr-only">Search movies</span><Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-[#8d99aa]" /><Input value={filters.query} onChange={(event) => setFilter("query", event.target.value)} placeholder="Search movies" className="h-8 border-[#202a39] bg-[#0e141e] pl-8 text-[11px] shadow-none placeholder:text-[#8d99aa] focus-visible:border-[#7c3aed] focus-visible:ring-[#7c3aed]/20" /></label>
          <LibrarySelect label="Quality" value={filters.quality} onChange={(value) => setFilter("quality", value)} options={facets.quality} className="w-[82px]" />
          <LibrarySelect label="Resolution" value={filters.resolution} onChange={(value) => setFilter("resolution", value)} options={facets.resolution} className="w-[82px]" />
          <LibrarySelect label="Audio" value={filters.audio} onChange={(value) => setFilter("audio", value)} options={facets.audio} className="w-[76px]" />
          <LibrarySelect label="Language" value={filters.language} onChange={(value) => setFilter("language", value)} options={facets.language} className="w-[84px]" />
          <LibrarySelect label="Availability" value={filters.availability} onChange={(value) => setFilter("availability", value as MovieLibrarySummary)} options={["available", "missing", "monitoring", "upgradable"]} className="w-[96px]" />
          <LibrarySelect label="Root Folder" value={filters.root} onChange={(value) => setFilter("root", value)} options={facets.root} className="w-[96px]" />
          <Button type="button" variant="outline" size="sm" aria-expanded={moreOpen} onClick={() => setMoreOpen((current) => !current)} className="h-8 shrink-0 border-[#202a39] bg-[#0e141e] px-2 text-[11px] text-[#aab3bf] shadow-none hover:bg-[#141c28]"><SlidersHorizontal /> More Filters{filters.plexConfirmedOnly || filters.managedOnly ? <span className="size-1.5 rounded-full bg-[#7c3aed]" aria-label="Additional filters active" /> : null}</Button>
        </div>
        {moreOpen ? <div className="absolute right-0 top-9 z-20 w-48 rounded-md border border-[#283447] bg-[#111925] p-2 shadow-xl">
          <label className="flex cursor-pointer items-center gap-2 rounded px-2 py-2 text-[10px] text-[#c5ccd5] hover:bg-[#17202d]"><input type="checkbox" checked={filters.plexConfirmedOnly} onChange={(event) => setFilter("plexConfirmedOnly", event.target.checked)} className="accent-[#7c3aed]" /> Plex confirmed only</label>
          <label className="flex cursor-pointer items-center gap-2 rounded px-2 py-2 text-[10px] text-[#c5ccd5] hover:bg-[#17202d]"><input type="checkbox" checked={filters.managedOnly} onChange={(event) => setFilter("managedOnly", event.target.checked)} className="accent-[#7c3aed]" /> Has managed file</label>
        </div> : null}
      </div>

      <div className="mb-2 flex shrink-0 gap-1.5 overflow-x-auto pb-0.5 scrollbar-none sm:grid sm:grid-cols-5 sm:overflow-visible sm:pb-0" aria-label="Movie summaries">
        {summaryItems.map((item) => <button key={item.key} type="button" aria-pressed={filters.summary === item.key} onClick={() => setFilter("summary", item.key)} className={cn("w-[88px] shrink-0 rounded-md border border-[#202a39] bg-[#0e141e] px-1.5 py-1.5 text-left outline-none transition hover:border-[#344155] focus-visible:ring-2 focus-visible:ring-[#7c3aed] sm:w-auto sm:min-w-0 sm:px-2", filters.summary === item.key && "border-[#6d3add] bg-[#171428]")}><span className="block truncate text-[10px] font-bold uppercase tracking-[0.08em] text-[#9ba5b2]">{item.label}</span><span className={cn("mt-0.5 block text-[16px] font-[800] leading-none text-[#f4f6fa]", filters.summary === item.key && "text-[#c6b5ff]")}>{item.count}</span></button>)}
      </div>

      <div className="mb-1 flex shrink-0 items-center justify-between text-[10px] text-[#929dac]" aria-live="polite"><span>{filtered.length} {filtered.length === 1 ? "movie" : "movies"}</span><span>Title order</span></div>

      <div className="min-h-0 flex-1 overflow-y-auto pr-0.5 scrollbar-thin">
        {!data && !error ? <div className="grid h-full min-h-44 place-items-center rounded-md border border-[#202a39] bg-[#0e141e] text-[10px] text-[#929dac]" role="status">Loading the live movie catalog…</div>
          : visible.length === 0 ? <div className="grid h-full min-h-44 place-items-center rounded-md border border-dashed border-[#283447] bg-[#0e141e]"><div className="px-6 text-center"><Film className="mx-auto size-6 text-[#596579]" /><div className="mt-2 text-[11px] font-bold text-[#f4f6fa]">{records.length === 0 ? "Your movie catalog is empty" : "No movies matched"}</div><div className="mt-1 text-[10px] text-[#9ba5b2]">{records.length === 0 ? "Use Add Movie to create a real catalog entry." : "Change or clear the active filters."}</div></div></div>
          : view === "grid" ? <div className="grid grid-cols-1 gap-2 sm:grid-cols-3 lg:grid-cols-5">
            {visible.map((item) => <MovieLibraryCard key={item.id} item={item} selected={item.id === selected?.id} onSelect={selectMovie} />)}
          </div> : <div className="overflow-hidden rounded-md border border-[#202a39] bg-[#0e141e]">
            {visible.map((item) => <MovieLibraryRow key={item.id} item={item} selected={item.id === selected?.id} onSelect={selectMovie} />)}
          </div>}
      </div>

      <div className="sticky bottom-0 z-10 mt-2 flex h-9 shrink-0 items-center justify-between border-t border-[#202a39] bg-[#080b12]/95 pt-2 backdrop-blur-sm" aria-label="Movie pagination">
        <span className="text-[10px] text-[#929dac]">{filtered.length ? `${(currentPage - 1) * pageSize + 1}–${Math.min(currentPage * pageSize, filtered.length)} of ${filtered.length}` : "0 movies"}</span>
        <div className="flex items-center gap-1"><Button type="button" variant="ghost" size="icon-xs" aria-label="Previous page" disabled={currentPage <= 1} onClick={() => setPage(Math.max(1, currentPage - 1))}><ChevronLeft /></Button><span className="min-w-14 text-center text-[10px] font-semibold text-[#aeb7c3]">{currentPage} / {pageCount}</span><Button type="button" variant="ghost" size="icon-xs" aria-label="Next page" disabled={currentPage >= pageCount} onClick={() => setPage(Math.min(pageCount, currentPage + 1))}><ChevronRight /></Button></div>
      </div>
    </section>

    {selected ? <>
      {selectedID !== undefined ? <button type="button" aria-label="Close movie details" onClick={() => setSelectedID(null)} className="fixed inset-0 z-40 bg-black/60 xl:hidden" /> : null}
      <MovieInspector key={selected.id} item={selected} tab={detailTab} auto={selectedID === undefined} onTabChange={setDetailTab} onClose={() => setSelectedID(null)} onRefresh={refresh} />
    </> : null}

    <nav className="fixed inset-x-0 bottom-0 z-30 flex h-12 items-center justify-around border-t border-[#202a39] bg-[#0e141e] px-3 md:hidden" aria-label="Mobile navigation">
      <Link href="/media" aria-current="page" className="flex flex-col items-center gap-0.5 text-[10px] font-bold text-[#bca7ff]"><Library className="size-4" />Library</Link>
      <Link href="/discover" className="flex flex-col items-center gap-0.5 text-[10px] font-semibold text-[#929dac]"><Plus className="size-4" />Add</Link>
      <Link href="/activity/queue" className="flex flex-col items-center gap-0.5 text-[10px] font-semibold text-[#929dac]"><Activity className="size-4" />Activity</Link>
    </nav>
  </div>
}

function LibrarySelect({ label, value, options, onChange, className }: { label: string; value: string; options: string[]; onChange: (value: string) => void; className?: string }) {
  return <label className="shrink-0"><span className="sr-only">{label}</span><NativeSelect size="sm" value={value} onChange={(event) => onChange(event.target.value)} className={cn("h-8 border-[#202a39] bg-[#0e141e] px-2 pr-6 text-[11px] text-[#aab3bf] shadow-none focus-visible:border-[#7c3aed] focus-visible:ring-[#7c3aed]/20", className)} aria-label={label}><NativeSelectOption value="">{label}</NativeSelectOption>{options.map((option) => <NativeSelectOption key={option} value={option}>{pretty(option)}</NativeSelectOption>)}</NativeSelect></label>
}

export function MovieLibraryCard({ item, selected, onSelect }: { item: MovieLibraryRecord; selected: boolean; onSelect: (id: string) => void }) {
  const availability = movieAvailability(item)
  const labels = uniqueValues([...item.resolutions.map(resolutionLabel), ...item.dynamicRanges])
  return <button type="button" onClick={() => onSelect(item.id)} aria-label={`${item.title}${item.year ? `, ${item.year}` : ""}. ${availability.label}`} aria-pressed={selected} className={cn("group flex min-w-0 overflow-hidden rounded-[6px] border border-[#202a39] bg-[#0e141e] text-left outline-none transition hover:border-[#3a4658] focus-visible:ring-2 focus-visible:ring-[#7c3aed] sm:block", selected && "border-[#7c3aed] ring-1 ring-[#7c3aed]/45")}>
    <div className="relative h-[94px] w-[70px] shrink-0 overflow-hidden border-r border-[#202a39] bg-[#111925] sm:h-auto sm:w-auto sm:border-b sm:border-r-0 sm:aspect-[2/3]"><MetadataArtwork src={item.metadata?.poster_url} fallback="/media/movie-placeholder.svg" alt="" width={300} height={450} className="h-full w-full object-cover" /><div className="absolute left-1.5 top-1.5 flex flex-wrap gap-1">{labels.slice(0, 2).map((label) => <span key={label} className="rounded-[3px] border border-white/15 bg-[#080b12]/90 px-1 py-0.5 text-[9px] font-extrabold uppercase text-[#f4f6fa]">{label}</span>)}</div></div>
    <div className="min-w-0 flex-1 p-1"><div className="truncate text-[11px] font-[750] leading-tight text-[#f4f6fa]">{item.title}</div><div className="text-[10px] text-[#929dac]">{item.year ?? "Year unknown"}</div><div className={cn("mt-0.5 flex items-center gap-1.5 text-[10px] font-semibold", availability.tone)}><span className={cn("size-1.5 shrink-0 rounded-full", availability.dot)} aria-hidden /> <span className="truncate">{availability.label}</span></div></div>
  </button>
}

function MovieLibraryRow({ item, selected, onSelect }: { item: MovieLibraryRecord; selected: boolean; onSelect: (id: string) => void }) {
  const availability = movieAvailability(item)
  return <button type="button" onClick={() => onSelect(item.id)} aria-label={`${item.title}${item.year ? `, ${item.year}` : ""}. ${availability.label}`} aria-pressed={selected} className={cn("flex w-full items-center gap-3 border-b border-[#202a39] px-3 py-2 text-left outline-none last:border-0 hover:bg-[#111925] focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[#7c3aed]", selected && "bg-[#171428]")}><MetadataArtwork src={item.metadata?.poster_url} fallback="/media/movie-placeholder.svg" alt="" width={72} height={96} className="h-12 w-9 rounded-[3px] border border-[#283447] object-cover" /><div className="min-w-0 flex-1"><div className="truncate text-[11px] font-bold text-[#f4f6fa]">{item.title} <span className="font-normal text-[#9ba5b2]">{item.year ? `(${item.year})` : ""}</span></div><div className="mt-1 truncate text-[10px] text-[#929dac]">{item.profiles.join(" · ") || "No managed quality profile"}</div></div><div className={cn("hidden items-center gap-1.5 text-[10px] font-semibold sm:flex", availability.tone)}><span className={cn("size-1.5 rounded-full", availability.dot)} />{availability.label}</div><ChevronRight className="size-3.5 text-[#8792a1]" /></button>
}

export function MovieInspector({ item, tab, auto, onTabChange, onClose, onRefresh }: { item: MovieLibraryRecord; tab: string; auto: boolean; onTabChange: (tab: string) => void; onClose: () => void; onRefresh?: () => void }) {
  const availability = movieAvailability(item)
  const currentFile = [...item.files].sort((left, right) => right.generation - left.generation).find((file) => file.state === "current") ?? item.files[0]
  const qualityProfile = currentFile?.quality_profile ?? item.wanted[0]?.target_profile ?? "Not assigned"
  const [refreshing, setRefreshing] = React.useState(false)
  async function refreshMetadata() {
    setRefreshing(true)
    try { const result = await refreshMediaMetadata(item.id); const direct = Object.entries(result.provider_states).filter(([, state]) => state === "direct" || state === "via_simkl").map(([provider]) => provider.toUpperCase()); toast.success(direct.length ? `${direct.join(", ")} metadata refreshed` : "TMDB metadata refreshed through Seerr"); onRefresh?.() }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not refresh metadata") }
    finally { setRefreshing(false) }
  }
  return <aside aria-label={`Details for ${item.title}`} className={cn("fixed inset-0 z-50 min-h-0 flex-col overflow-hidden border-l border-[#202a39] bg-[#0e141e] sm:bottom-0 sm:left-auto sm:top-[60px] sm:w-[330px] xl:static xl:z-auto xl:flex xl:h-full xl:w-auto", auto ? "hidden" : "flex")}>
    <div className="flex h-10 shrink-0 items-center justify-between border-b border-[#202a39] px-3"><span className="text-[10px] font-extrabold uppercase tracking-[0.12em] text-[#aab3bf]">Movie details</span><Button type="button" variant="ghost" size="icon-xs" aria-label="Close details" onClick={onClose}><X /></Button></div>
    <div className="min-h-0 flex-1 overflow-y-auto scrollbar-thin">
      <div className="border-b border-[#202a39] p-3">
        <div className="flex gap-3 xl:block"><MetadataArtwork src={item.metadata?.poster_url} fallback="/media/movie-placeholder.svg" alt={`${item.title} poster`} width={300} height={450} className="aspect-[2/3] w-[86px] shrink-0 rounded-[5px] border border-[#283447] object-cover xl:w-full" /><div className="min-w-0 pt-1 xl:pt-3"><h2 className="text-[15px] font-[800] leading-tight tracking-[-0.02em] text-[#f4f6fa]">{item.title}</h2><div className="mt-1 text-[10px] text-[#929dac]">{item.year ?? "Year unknown"}</div><div className={cn("mt-2 flex items-center gap-1.5 text-[10px] font-bold", availability.tone)}><span className={cn("size-1.5 rounded-full", availability.dot)} />{availability.label}</div></div></div>
        <Button asChild size="sm" className="mt-3 h-8 w-full bg-[#7c3aed] text-white hover:bg-[#6d2fd4]"><Link href={`/media/movies/${item.id}`}><Film /> Open movie details</Link></Button>
        <Button type="button" variant="outline" size="sm" className="mt-2 h-8 w-full" disabled={refreshing || !item.identities.some((identity) => identity.namespace === "tmdb" && identity.state === "verified")} onClick={() => void refreshMetadata()}><RefreshCw className={cn(refreshing && "animate-spin")} /> Refresh metadata</Button>
      </div>

      <Tabs value={tab} onValueChange={onTabChange} className="gap-0">
        <TabsList variant="line" className="grid h-9 w-full grid-cols-4 gap-0 border-b border-[#202a39] px-1">
          {(["overview", "files", "metadata", "history"] as const).map((value) => <TabsTrigger key={value} value={value} className="h-9 px-1 text-[10px] capitalize text-[#9ba5b2] after:bg-[#7c3aed] data-[state=active]:text-[#f4f6fa]">{value}</TabsTrigger>)}
        </TabsList>
        <TabsContent value="overview" className="p-3">
          {item.metadata?.overview ? <p className="mb-3 text-[11px] leading-4 text-[#b9c1cc]">{item.metadata.overview}</p> : null}
          {item.metadata?.ratings.length ? <div className="mb-2 grid grid-cols-2 gap-1.5">{item.metadata.ratings.map((rating) => <div key={rating.source} title={rating.provenance} className="rounded-[4px] border border-[#202a39] bg-[#111925] px-2 py-1.5"><div className="text-[9px] font-bold uppercase tracking-[0.08em] text-[#9ba5b2]">{rating.source}</div><div className="mt-0.5 text-[11px] font-extrabold text-[#f4f6fa]">{rating.value.toFixed(1)} <span className="text-[9px] font-normal text-[#929dac]">/ 10</span></div>{rating.votes > 0 ? <div className="text-[9px] text-[#929dac]">{rating.votes.toLocaleString()} votes</div> : null}</div>)}</div> : null}
          {item.metadata?.tagline ? <p className="mb-2 text-[10px] italic text-[#aab3bf]">{item.metadata.tagline}</p> : null}
          {item.metadata?.runtime_minutes ? <InspectorRow label="Runtime" value={`${item.metadata.runtime_minutes} minutes`} /> : null}
          {item.metadata?.release_date ? <InspectorRow label="Release date" value={item.metadata.release_date} /> : null}
          {item.metadata?.certification ? <InspectorRow label="Certification" value={item.metadata.certification} /> : null}
          {item.metadata?.genres.length ? <InspectorRow label="Genres" value={compactValues(item.metadata.genres)} /> : null}
          <InspectorRow label="Availability" value={availability.label} />
          <InspectorRow label="Quality profile" value={qualityProfile} />
          <InspectorRow label="Resolution" value={item.resolutions.map(resolutionLabel).join(", ") || "Unknown"} />
          <InspectorRow label="Audio" value={item.audioCodecs.join(", ") || "Unknown"} />
          <InspectorRow label="Languages" value={compactValues(item.languages) || "Unknown"} />
          <InspectorRow label="Root folder" value={item.roots.join(", ") || "Not published"} />
          <InspectorRow label="Plex verification" value={currentFile ? currentFile.plex_confirmed ? "Exact path confirmed" : "Not confirmed" : "No managed file"} />
        </TabsContent>
        <TabsContent value="files" className="space-y-2 p-3">
          {item.files.map((file) => <Link key={file.id} href={`/files/${file.id}`} className="block rounded-[5px] border border-[#202a39] bg-[#111925] p-2 outline-none hover:border-[#344155] focus-visible:ring-2 focus-visible:ring-[#7c3aed]"><div className="flex items-center justify-between gap-2"><span className="truncate text-[10px] font-bold text-[#e8ebf0]">{file.variant}</span><span className="text-[9px] text-[#9ba5b2]">Gen {file.generation}</span></div><div className="mt-1 text-[10px] text-[#9ba5b2]">{pretty(file.state)} · {formatBytes(file.source_size_bytes)}</div><div className="mt-1 truncate font-mono text-[9px] text-[#929dac]">{file.destination_relative}</div></Link>)}
          {item.files.length === 0 ? <InspectorEmpty>No managed files are recorded.</InspectorEmpty> : null}
        </TabsContent>
        <TabsContent value="metadata" className="space-y-2 p-3">
          {item.metadata?.sources.map((source) => <div key={`${source.provider}:${source.provider_id}:${source.role}`} className="rounded-[5px] border border-[#202a39] bg-[#111925] p-2"><div className="text-[10px] font-bold uppercase tracking-[0.08em]"><MetadataSourceLink source={source} /></div><div className="mt-1 text-[10px] text-[#cfd5dd]">{source.role}</div></div>)}
          {item.identities.map((identity) => <div key={`${identity.namespace}:${identity.value}`} className="rounded-[5px] border border-[#202a39] bg-[#111925] p-2"><div className="text-[10px] font-bold uppercase tracking-[0.08em] text-[#9ba5b2]">{identity.namespace}</div><div className="mt-1 break-all font-mono text-[10px] text-[#e4e8ed]">{identity.value}</div>{identity.state ? <div className="mt-1 text-[9px] text-[#929dac]">{pretty(identity.state)}</div> : null}</div>)}
          {(item.metadata?.companies ?? []).length ? <div className="rounded-[5px] border border-[#202a39] bg-[#111925] p-2"><div className="text-[10px] font-bold uppercase tracking-[0.08em] text-[#9ba5b2]">Studios</div><div className="mt-1 text-[10px] text-[#e4e8ed]">{compactValues((item.metadata?.companies ?? []).map((company) => company.name))}</div></div> : null}
          {(item.metadata?.credits ?? []).length ? <div className="rounded-[5px] border border-[#202a39] bg-[#111925] p-2"><div className="text-[10px] font-bold uppercase tracking-[0.08em] text-[#9ba5b2]">Cast and crew</div><div className="mt-1 space-y-1">{(item.metadata?.credits ?? []).slice(0, 10).map((credit) => <div key={`${credit.source}:${credit.role}:${credit.name}:${credit.character ?? ""}`} className="flex justify-between gap-2 text-[10px]"><span className="truncate text-[#e4e8ed]">{credit.name}{credit.character ? ` · ${credit.character}` : ""}</span><span className="shrink-0 text-[#929dac]">{credit.role}</span></div>)}</div></div> : null}
          {item.identities.length === 0 ? <InspectorEmpty>No provider identities are recorded.</InspectorEmpty> : null}
        </TabsContent>
        <TabsContent value="history" className="space-y-2 p-3">
          {item.workflows.map((workflow) => <div key={workflow.id} className="rounded-[5px] border border-[#202a39] bg-[#111925] p-2"><div className="flex items-center justify-between gap-2"><span className="text-[10px] font-bold text-[#e4e8ed]">{pretty(workflow.state)}</span><span className="text-[9px] text-[#929dac]">{timeAgo(workflow.updated_at)}</span></div><div className="mt-1 font-mono text-[9px] text-[#9ba5b2]">{workflow.variant}</div></div>)}
          {item.workflows.length === 0 ? <InspectorEmpty>No workflow history is recorded.</InspectorEmpty> : null}
        </TabsContent>
      </Tabs>
    </div>
  </aside>
}

function InspectorRow({ label, value }: { label: string; value: string }) {
  return <div className="flex items-start justify-between gap-3 border-b border-[#202a39] py-2 last:border-0"><span className="text-[10px] text-[#9ba5b2]">{label}</span><span className="max-w-[58%] text-right text-[10px] font-semibold leading-4 text-[#dce1e7]">{value}</span></div>
}

function InspectorEmpty({ children }: { children: React.ReactNode }) {
  return <div className="rounded-[5px] border border-dashed border-[#283447] px-3 py-6 text-center text-[10px] text-[#9ba5b2]">{children}</div>
}

export function LiveWantedScreen() {
  const { data, error, refresh } = useLiveData(wantedLoader)
  const items = data ?? []
  const missing = items.filter((item) => item.gap === "missing").length
  const upgrades = items.filter((item) => item.gap === "upgrade").length
  const attention = items.filter((item) => ["needs_attention", "held", "blocked", "quarantined", "transport_unknown"].includes(item.current_state)).length
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Missing" value={String(missing)} note="Active scope has no current publication" tone={missing ? "info" : "good"} icon={Search} /><MetricCard label="Upgrades" value={String(upgrades)} note="Current publication remains until replacement verification" tone={upgrades ? "info" : "good"} icon={Gauge} /><MetricCard label="Needs attention" value={String(attention)} note="Persisted workflow state, not a global failure" tone={attention ? "warn" : "good"} icon={AlertTriangle} /></div>
    {!data && !error ? <LoadingPanel label="Deriving wanted and upgrade gaps…" /> : <Panel className="!p-0" title="Wanted and upgrades" subtitle="Derived from active workflows, current publications, and persisted quality policy." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Media</TableHead><TableHead>Scope</TableHead><TableHead>Gap</TableHead><TableHead>Workflow</TableHead><TableHead>Target profile</TableHead><TableHead>Updated</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.workflow_id}><TableCell><Link className="font-semibold text-[#e5eaf0] hover:text-[#aebdff]" href={item.media_kind === "movie" ? `/media/movies/${item.media_id}` : `/media/series/${item.media_id}`}>{item.title}{item.year ? ` (${item.year})` : ""}</Link></TableCell><TableCell className="font-mono text-[9px]">{item.variant}</TableCell><TableCell><StatusChip tone={item.gap === "upgrade" ? "info" : "warn"}>{pretty(item.gap)}</StatusChip></TableCell><TableCell><StatusChip tone={workflowTone(item.current_state)}>{pretty(item.current_state)}</StatusChip>{item.attention_detail ? <div className="mt-1 max-w-sm text-[9px] text-[#d6b36d]">{item.attention_detail}</div> : null}</TableCell><TableCell>{item.target_profile}<div className="mt-1 text-[9px] text-[#748090]">Upgrades {item.upgrades_enabled ? "enabled" : "disabled"}</div></TableCell><TableCell>{timeAgo(item.updated_at)}</TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No active missing or upgrade workflows.</div> : null}<div className="border-t border-white/[0.06] p-3 text-[9.5px] text-[#748090]">An existing current file is never replaced here. Replacement occurs only after the new generation is verified and Plex confirms its exact path and identity.</div></Panel>}
  </div>
}

export function LiveCalendarScreen() {
  const { data, error, refresh } = useLiveData(calendarLoader)
  const items = data ?? []
  const monitored = items.filter((item) => item.monitored).length
  const future = items.filter((item) => new Date(item.air_date).getTime() >= new Date().setHours(0, 0, 0, 0)).length
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}<div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Dated episodes" value={String(items.length)} note="Persisted provider dates only" icon={ListChecks} /><MetricCard label="Monitored" value={String(monitored)} note="Season monitoring state" tone="info" icon={SearchCheck} /><MetricCard label="Upcoming" value={String(future)} note="No date triggers an automatic search" tone="good" icon={Activity} /></div>{!data && !error ? <LoadingPanel /> : <Panel className="!p-0" title="Episode availability calendar" subtitle="Exact Seerr air dates persisted with immutable season expectations; unknown dates are omitted." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Date</TableHead><TableHead>Series</TableHead><TableHead>Episode</TableHead><TableHead>Monitoring</TableHead><TableHead>Workflow</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={`${item.workflow_id}:${item.season}:${item.episode}`}><TableCell>{new Date(item.air_date).toLocaleDateString()}</TableCell><TableCell><Link className="font-semibold text-[#e5eaf0] hover:text-[#aebdff]" href={`/media/series/${item.media_id}`}>{item.media_title}</Link></TableCell><TableCell>S{String(item.season).padStart(2, "0")}E{String(item.episode).padStart(2, "0")}{item.episode_title ? <div className="mt-1 text-[9px] text-[#748090]">{item.episode_title}</div> : null}</TableCell><TableCell><StatusChip tone={item.monitored ? "good" : "neutral"}>{item.monitored ? "Monitored" : "Unmonitored"}</StatusChip></TableCell><TableCell><StatusChip tone={workflowTone(item.workflow_state)}>{pretty(item.workflow_state)}</StatusChip></TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No exact episode air dates are persisted in the current calendar window.</div> : null}</Panel>}</div>
}

function ActivityList({ items }: { items: APIActivity[] }) {
  if (items.length === 0) return <div className="py-8 text-center text-[11px] text-[#7f8a9a]">No durable activity yet.</div>
  return <div className="space-y-2">{items.map((item) => <div key={item.id} className="rounded-xl border border-white/[0.06] bg-[#0c1015] px-3 py-2.5"><div className="flex items-center justify-between gap-3"><span className="text-[10px] font-semibold text-[#dce2e9]">{pretty(item.action.replace(/^workflow\./, ""))}</span><span className="text-[9px] text-[#6f7b8a]">{timeAgo(item.occurred_at)}</span></div><div className="mt-1 text-[9px] text-[#788493]">{item.aggregate_type} · {item.actor}{activityReason(item) ? ` · ${activityReason(item)}` : ""}</div></div>)}</div>
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
    {!data && !error ? <LoadingPanel /> : <Panel className="!p-0" title={logs ? "Application audit" : "Activity history"} subtitle={logs ? "Persisted structured writes; container stdout is intentionally not presented as durable audit." : "Append-only audit writes and workflow transitions."} action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Time</TableHead><TableHead>Event</TableHead><TableHead>Object</TableHead><TableHead>Actor</TableHead><TableHead>Detail</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell className="whitespace-nowrap">{new Date(item.occurred_at).toLocaleString()}</TableCell><TableCell className="font-semibold">{pretty(item.action)}</TableCell><TableCell>{item.aggregate_type}</TableCell><TableCell>{item.actor}</TableCell><TableCell className="max-w-md text-[9px] text-[#7f8a9a]">{activityReason(item) || "—"}</TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No persisted events.</div> : null}</Panel>}
  </div>
}

export function LiveFilesScreen() {
  const { data, error, refresh } = useLiveData(filesLoader)
  const items = data ?? []
  const current = items.filter((item) => item.state === "current").length
  const confirmed = items.filter((item) => item.plex_confirmed).length
  const attention = items.filter((item) => item.state === "quarantined").length
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Current" value={String(current)} note="Active managed library files" tone="good" icon={Files} /><MetricCard label="Plex confirmed" value={String(confirmed)} note="Exact observed path and identity" tone="info" icon={CheckCircle2} /><MetricCard label="Quarantined" value={String(attention)} note="Publication needs review" tone={attention ? "warn" : "good"} icon={AlertTriangle} /></div>
    {!data && !error ? <LoadingPanel /> : <Panel className="!p-0" title="Managed files" subtitle="Plex-safe AltMount symlinks and reviewed adopted real files with persisted provenance." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Media</TableHead><TableHead>Destination</TableHead><TableHead>Origin</TableHead><TableHead>Quality evidence</TableHead><TableHead>State</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell><Link className="font-semibold text-[#e5eaf0] hover:text-[#aebdff]" href={`/files/${item.id}`}>{item.title}{item.year ? ` (${item.year})` : ""}</Link><div className="mt-1 text-[9px] text-[#707c8c]">{item.root_name} · {item.variant}</div></TableCell><TableCell className="max-w-xs break-all font-mono text-[9px]">{item.destination_relative}</TableCell><TableCell>{item.origin === "adoption" ? "Adopted real file" : "AltMount symlink"}<div className="mt-1 max-w-xs break-all font-mono text-[9px] text-[#707c8c]">{item.source_relative}</div></TableCell><TableCell><StatusChip tone={item.quality_evidence_state === "unassessed" ? "warn" : "info"}>{pretty(item.quality_evidence_state)}</StatusChip><div className="mt-1 text-[9px] text-[#707c8c]">{[qualityFact(item.quality_evidence, "resolution"), qualityFact(item.quality_evidence, "video_codec")].filter(Boolean).join(" · ") || item.quality_profile}</div></TableCell><TableCell><StatusChip tone={fileTone(item.state, item.plex_confirmed)}>{pretty(item.state)}</StatusChip></TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No managed files exist. AltMount source remains read-only.</div> : null}</Panel>}
  </div>
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
    <div className="grid gap-4 xl:grid-cols-[1.3fr_380px]"><Panel className="!p-0" title="Release blocklist" subtitle="Blocks reject only the persisted native release or exact media/season pack; provider health is unaffected." action={<RefreshButton onClick={() => { blocksState.refresh(); candidatesState.refresh() }} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Release</TableHead><TableHead>Scope</TableHead><TableHead>Reason</TableHead><TableHead>Expires</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{blocks.map((block) => { const expired = Boolean(block.expires_at && new Date(block.expires_at) <= new Date()); return <TableRow key={block.id}><TableCell className="max-w-lg font-semibold text-[#dfe5ec]">{block.release_title}<div className="mt-1 text-[9px] text-[#748090]">{block.scope === "native_release" ? block.indexer_name : `${block.media_title} · ${block.variant}`}</div></TableCell><TableCell><StatusChip tone={expired ? "neutral" : "warn"}>{expired ? "Expired" : pretty(block.scope)}</StatusChip></TableCell><TableCell className="max-w-sm">{block.reason}</TableCell><TableCell>{block.expires_at ? new Date(block.expires_at).toLocaleDateString() : "Manual removal"}</TableCell><TableCell><Button size="sm" variant="ghost" disabled={busy === block.id} onClick={() => void remove(block.id)}>Remove</Button></TableCell></TableRow> })}</TableBody></Table></div>{blocks.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No releases are blocked.</div> : null}</Panel><Panel title="Block observed release" subtitle="Only a persisted candidate can become a block; arbitrary broad patterns are not accepted."><form className="grid gap-3" onSubmit={add}><label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">Observed candidate<select className="mm-input h-10 rounded-md px-3" value={candidateID} onChange={(event) => { const value = event.target.value; setCandidateID(value); const item = candidates.find((candidate) => candidate.id === value); if (!item?.variant.startsWith("season:")) setScope("native_release") }} required><option value="">Select a persisted candidate</option>{candidates.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.media_title} · {candidate.title}</option>)}</select></label><label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">Scope<select className="mm-input h-10 rounded-md px-3" value={scope} onChange={(event) => setScope(event.target.value as "native_release" | "pack_release")}><option value="native_release">This indexer + native release</option><option value="pack_release" disabled={!selected?.variant.startsWith("season:")}>This exact media + season pack title</option></select></label><label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">Retention<select className="mm-input h-10 rounded-md px-3" value={retention} onChange={(event) => setRetention(event.target.value)}><option value="30">30 days</option><option value="90">90 days</option><option value="0">Until manually removed</option></select></label><label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">Reason<Input className="mm-input" minLength={3} maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} placeholder="Why this exact release is unsafe" required /></label><Button type="submit" disabled={!selected || busy === "create"}><ShieldCheck /> Persist narrow block</Button></form><InlineNotice tone="info" title="Search behavior">Blocked observations remain in evidence history with a rejected policy decision. No request is sent to an indexer from this screen.</InlineNotice></Panel></div>
  </div>
}

function ReleaseTable({ items, refresh }: { items: APIReleaseCandidate[]; refresh: () => void }) {
  return <Panel className="!p-0" title="Release candidates" subtitle="Persisted observations and policy decisions from paced searches." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Candidate</TableHead><TableHead>Media</TableHead><TableHead>Indexer</TableHead><TableHead>Size</TableHead><TableHead>Decision</TableHead><TableHead>Observed</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell className="max-w-lg font-semibold text-[#dfe5ec]">{item.title}{item.selected ? <div className="mt-1 text-[9px] text-[#6fd0a2]">Selected for this workflow</div> : null}</TableCell><TableCell><Link className="hover:text-[#aebdff]" href={item.media_kind === "movie" ? `/media/movies/${item.media_id}` : `/media/series/${item.media_id}`}>{item.media_title}</Link></TableCell><TableCell>{item.indexer_name}</TableCell><TableCell>{formatBytes(item.size_bytes)}</TableCell><TableCell><StatusChip tone={verdictTone(item.verdict ?? "", item.selected)}>{item.selected ? "Selected" : item.verdict ? pretty(item.verdict) : "Unevaluated"}{item.score !== undefined ? ` · ${item.score}` : ""}</StatusChip></TableCell><TableCell>{timeAgo(item.observed_at)}</TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No release observations have been persisted.</div> : null}</Panel>
}

function EvidenceTable({ items, refresh }: { items: APIReleaseCandidate[]; refresh: () => void }) {
  return <Panel className="!p-0" title="Evidence explorer" subtitle="Normalized facts point to a persisted raw observation; unknowns stay visible." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Candidate</TableHead><TableHead>Fact</TableHead><TableHead>Value</TableHead><TableHead>Decision</TableHead></TableRow></TableHeader><TableBody>{items.flatMap((item) => Object.entries(item.normalized_facts ?? {}).slice(0, 20).map(([fact, value]) => <TableRow key={`${item.id}:${fact}`}><TableCell className="max-w-sm truncate font-semibold">{item.title}</TableCell><TableCell className="font-mono text-[9px]">{fact}</TableCell><TableCell className="max-w-lg break-words">{formatFact(value)}</TableCell><TableCell><StatusChip tone={verdictTone(item.verdict ?? "", item.selected)}>{item.verdict ? pretty(item.verdict) : "Unevaluated"}</StatusChip></TableCell></TableRow>))}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No evidence has been persisted.</div> : null}</Panel>
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
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{loading ? <LoadingPanel /> : <Panel className="!p-0" title="Observed release groups" subtitle="Counts summarize current persisted evidence; they are not permanent trust labels." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Group</TableHead><TableHead>Observations</TableHead><TableHead>Eligible</TableHead><TableHead>Held</TableHead><TableHead>Rejected</TableHead></TableRow></TableHeader><TableBody>{groups.map(([name, counts]) => <TableRow key={name}><TableCell className="font-semibold">{name}</TableCell><TableCell>{counts.observations}</TableCell><TableCell>{counts.eligible}</TableCell><TableCell>{counts.held}</TableCell><TableCell>{counts.rejected}</TableCell></TableRow>)}</TableBody></Table></div>{groups.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No group evidence has been observed.</div> : null}</Panel>}</div>
}

export function LiveSourcesScreen({ kind }: { kind?: "newznab" | "altmount" }) {
  const { data, error, refresh } = useLiveData(sourcesLoader)
  const items = (data ?? []).filter((item) => !kind || item.kind === kind)
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
  return <Panel className="!p-0" title="Configured sources" subtitle="Connection configuration and observed scheduler state remain separate." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Type</TableHead><TableHead>Endpoint</TableHead><TableHead>Configuration</TableHead><TableHead>Budget / latest outcome</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell><Link className="font-semibold text-[#e5eaf0] hover:text-[#aebdff]" href={item.kind === "newznab" ? `/sources/indexers/${item.id}` : "/settings/connections"}>{item.name}</Link></TableCell><TableCell>{item.kind}</TableCell><TableCell className="max-w-sm break-all font-mono text-[9px]">{sourceEndpoint(item)}</TableCell><TableCell><StatusChip tone={item.configuration_state === "configured" ? "info" : item.configuration_state === "awaiting_commission" ? "warn" : "neutral"}>{pretty(item.configuration_state)}</StatusChip></TableCell><TableCell>{item.minimum_interval_ms ? `${item.minimum_interval_ms} ms` : "—"}<div className="mt-1 text-[9px] text-[#748090]">{item.last_outcome ? pretty(item.last_outcome) : "No observed request outcome"}</div></TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No matching sources are configured.</div> : null}</Panel>
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
  const item = data?.find((source) => source.id === indexerId && source.kind === "newznab")
  if (!data && !error) return <LoadingPanel label="Loading indexer state…" />
  if (data && !item) return <ErrorNotice error="The Newznab indexer was not found." />
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{item ? <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Configuration" value={pretty(item.configuration_state)} note="Enabled only after commissioning" tone={item.configuration_state === "configured" ? "info" : "warn"} icon={Globe2} /><MetricCard label="Minimum interval" value={item.minimum_interval_ms ? `${item.minimum_interval_ms} ms` : "Not observed"} note="One in-flight request per indexer" icon={Gauge} /><MetricCard label="Latest outcome" value={item.last_outcome ? pretty(item.last_outcome) : "None"} note={item.budget_updated_at ? timeAgo(item.budget_updated_at) : "No runtime request recorded"} tone={item.last_outcome === "auth_failed" || item.last_outcome === "system_failure" ? "warn" : "neutral"} icon={Activity} /></div><div className="grid gap-4 xl:grid-cols-[1.2fr_1fr]"><Panel title={item.name} subtitle="Persisted Newznab configuration. Secrets remain environment references."><KeyValue label="Endpoint" value={<span className="break-all font-mono">{sourceEndpoint(item)}</span>} /><KeyValue label="Secret reference" value={<span className="font-mono">{item.secret_ref ?? "—"}</span>} /><KeyValue label="Default" value={item.is_default ? "Yes" : "No"} /><KeyValue label="Movie search" value={indexerDialect(item, "movie")} /><KeyValue label="TV search" value={indexerDialect(item, "series")} /><KeyValue label="Request ceiling" value={indexerRequestBudget(item)} /></Panel><Panel title="Pacing state" action={<RefreshButton onClick={refresh} />}><KeyValue label="Next allowed" value={item.next_allowed_at ? new Date(item.next_allowed_at).toLocaleString() : "No persisted delay"} /><KeyValue label="Cooldown" value={item.cooldown_until ? new Date(item.cooldown_until).toLocaleString() : "None"} /><KeyValue label="Consecutive failures" value={String(item.consecutive_failure)} /><InlineNotice tone="info" title="No automatic test">Opening this screen never contacts the indexer. Commissioning remains the only owner-triggered diagnostic path.</InlineNotice></Panel></div></> : null}</div>
}

export function LiveComponentHealthScreen() {
  const { data, error, refresh } = useLiveData(healthLoader)
  if (!data && !error) return <LoadingPanel label="Loading component health…" />
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <HealthContent data={data} refresh={refresh} /> : null}</div>
}

export function LiveTasksScreen() {
  const { data, error, refresh } = useLiveData(tasksLoader)
  if (!data && !error) return <LoadingPanel label="Loading persisted worker state…" />
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <TasksContent data={data} refresh={refresh} /> : null}</div>
}

function TasksContent({ data, refresh }: { data: APITasks; refresh: () => void }) {
  const pending = data.event_types.reduce((total, item) => total + item.pending, 0)
  const due = data.event_types.reduce((total, item) => total + item.due, 0)
  const retrying = data.event_types.reduce((total, item) => total + item.retrying, 0)
  return <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Pending" value={String(pending)} note="Durable outbox events" tone="info" icon={ListChecks} /><MetricCard label="Due now" value={String(due)} note="Available for a worker lease" tone={due ? "warn" : "good"} icon={Activity} /><MetricCard label="Retried" value={String(retrying)} note="Pending events with prior attempts" tone={retrying ? "warn" : "good"} icon={RefreshCw} /></div><div className="grid gap-4 xl:grid-cols-[1.35fr_1fr]"><Panel className="!p-0" title="Durable event work" subtitle="Persisted event-type aggregates; opening this page never triggers a task." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Event type</TableHead><TableHead>Pending</TableHead><TableHead>Due</TableHead><TableHead>Retrying</TableHead><TableHead>Completed</TableHead><TableHead>Highest attempts</TableHead><TableHead>Latest event</TableHead></TableRow></TableHeader><TableBody>{data.event_types.map((item) => <TableRow key={item.event_type}><TableCell className="font-mono text-[9px]">{item.event_type}</TableCell><TableCell>{item.pending}</TableCell><TableCell>{item.due}</TableCell><TableCell>{item.retrying}</TableCell><TableCell>{item.completed}</TableCell><TableCell>{item.highest_attempts}</TableCell><TableCell>{timeAgo(item.latest_at)}</TableCell></TableRow>)}</TableBody></Table></div>{data.event_types.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No durable worker events have been created.</div> : null}</Panel><Panel title="Recurring work" subtitle="Configured server schedules; automatic does not claim a successful external operation.">{data.recurring.map((item) => <div key={item.name} className="mb-2 rounded-xl border border-white/[0.06] bg-[#0c1015] p-3"><div className="flex items-center justify-between gap-3"><span className="text-[10px] font-semibold text-[#dfe5ec]">{item.name}</span><StatusChip tone={item.automatic ? "info" : "neutral"}>{item.automatic ? "Automatic" : "Disabled"}</StatusChip></div><div className="mt-1 text-[9px] text-[#748090]">{item.schedule}</div></div>)}</Panel></div></>
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
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <><div className="grid gap-3 sm:grid-cols-4"><MetricCard label="TMDB" value={pretty(configured("tmdb"))} note="Primary descriptions, artwork, credits, dates, genres, and rating" tone={configured("tmdb") !== "not_configured" ? "info" : "neutral"} icon={SearchCheck} /><MetricCard label="TheTVDB" value={pretty(configured("tvdb"))} note="Exact-ID TV and movie supplementary evidence" tone={configured("tvdb") === "direct_configured" ? "info" : "neutral"} icon={ShieldCheck} /><MetricCard label="Simkl" value={pretty(configured("simkl"))} note="Exact-ID ratings and lifecycle evidence" tone={configured("simkl") === "direct_configured" ? "info" : "neutral"} icon={ListChecks} /><MetricCard label="IMDb" value={pretty(data.metadata_providers?.imdb ?? "identity_only")} note="Identity and labeled Simkl rating evidence; never scraped" icon={Film} /></div><Panel title="Metadata and artwork" subtitle="Provider roles and persistence are reported without synthetic connectivity claims." action={<RefreshButton onClick={refresh} />}><KeyValue label="Primary descriptive source" value={configured("tmdb") === "direct_configured" ? "Direct TMDB API" : configured("tmdb") === "seerr_fallback" ? "TMDB response through Seerr fallback" : "Not configured"} /><KeyValue label="TV and movie supplement" value={configured("tvdb") === "direct_configured" ? "Direct TheTVDB API" : "Not configured"} /><KeyValue label="Ratings and lifecycle" value={configured("simkl") === "direct_configured" ? "Direct Simkl API" : "Not configured"} /><KeyValue label="IMDb policy" value="Verified identity and clearly labeled Simkl evidence; direct API requires a separate licensed source" /><KeyValue label="Merge gate" value="Exact verified TMDB, TVDB, IMDb, or Simkl identity; never title-only" /><KeyValue label="Stored fields" value="Overview, artwork, tagline, dates, certification, runtime, genres, keywords, studios, cast/crew, networks, aliases, ratings, events, and provider provenance" /><InlineNotice tone="info" title="Owner-triggered and bounded">A refresh performs at most one exact detail request per configured provider, plus TheTVDB login when needed and one Simkl ID-resolution request. Provider conflicts are rejected independently and cannot replace verified identities.</InlineNotice><p className="mt-3 text-[8px] text-[#6f7a89]">This product uses the TMDB API but is not endorsed or certified by TMDB. Simkl evidence links to its item source.</p></Panel></> : null}</div>
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
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}<div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Destinations" value={String(destinations.length)} note={`${destinations.filter((item) => item.enabled).length} enabled`} icon={Globe2} /><MetricCard label="Delivered" value={String(deliveries.filter((item) => item.state === "delivered").length)} note="Bounded durable history" tone="good" icon={CheckCircle2} /><MetricCard label="Failed" value={String(deliveries.filter((item) => item.state === "failed").length)} note="Worker retries remain visible" tone={deliveries.some((item) => item.state === "failed") ? "warn" : "good"} icon={AlertTriangle} /></div><div className="grid gap-4 xl:grid-cols-[1.3fr_380px]"><Panel className="!p-0" title="Notification destinations" subtitle="Payloads contain event identity and object references, never audit detail or secret values." action={<RefreshButton onClick={() => { destinationsState.refresh(); deliveriesState.refresh() }} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Endpoint</TableHead><TableHead>Events</TableHead><TableHead>Secret reference</TableHead><TableHead>State</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{destinations.map((item) => <TableRow key={item.id}><TableCell className="font-semibold">{item.name}</TableCell><TableCell className="max-w-sm break-all font-mono text-[9px]">{item.endpoint}</TableCell><TableCell className="max-w-sm text-[9px]">{item.events.join(" · ")}</TableCell><TableCell className="font-mono text-[9px]">{item.secret_ref || "—"}</TableCell><TableCell><StatusChip tone={item.enabled ? "good" : "neutral"}>{item.enabled ? "Enabled" : "Disabled"}</StatusChip></TableCell><TableCell><Button size="sm" variant="outline" disabled={busy === item.id} onClick={() => void toggle(item.id, !item.enabled)}>{item.enabled ? "Disable" : "Enable"}</Button></TableCell></TableRow>)}</TableBody></Table></div>{destinations.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No notification destinations configured.</div> : null}</Panel><Panel title="Add owner webhook" subtitle="Saving does not send a test message. HTTPS is required unless local HTTP is explicitly allowed."><form className="grid gap-3" onSubmit={create}><label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">Name<Input className="mm-input" value={name} onChange={(event) => setName(event.target.value)} required /></label><label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">Webhook endpoint<Input className="mm-input" type="url" value={endpoint} onChange={(event) => setEndpoint(event.target.value)} placeholder="https://notify.example/hook" required /></label><label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">Events<Input className="mm-input" value={events} onChange={(event) => setEvents(event.target.value)} required /></label><label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">Bearer secret environment name<Input className="mm-input font-mono" value={secretRef} onChange={(event) => setSecretRef(event.target.value)} placeholder="Optional" /></label><label className="flex items-center gap-2 text-[9.5px] text-[#8d98a6]"><input type="checkbox" checked={allowHTTP} onChange={(event) => setAllowHTTP(event.target.checked)} />Allow plain HTTP for an explicitly local destination</label><Button type="submit" disabled={busy === "create"}>Save destination</Button></form></Panel></div><Panel className="!p-0" title="Delivery history" subtitle="Durable attempts and bounded, redacted event payloads."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Created</TableHead><TableHead>Destination</TableHead><TableHead>Event</TableHead><TableHead>Object</TableHead><TableHead>State</TableHead><TableHead>Attempts</TableHead></TableRow></TableHeader><TableBody>{deliveries.map((item) => <TableRow key={item.id}><TableCell>{new Date(item.created_at).toLocaleString()}</TableCell><TableCell>{item.destination}</TableCell><TableCell className="font-mono text-[9px]">{item.payload.action}</TableCell><TableCell>{item.payload.aggregate_type}</TableCell><TableCell><StatusChip tone={item.state === "delivered" ? "good" : item.state === "failed" ? "warn" : "info"}>{pretty(item.state)}{item.response_status ? ` · ${item.response_status}` : ""}</StatusChip>{item.last_error ? <div className="mt-1 text-[9px] text-[#c6a867]">{item.last_error}</div> : null}</TableCell><TableCell>{item.attempts}</TableCell></TableRow>)}</TableBody></Table></div>{deliveries.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No delivery attempts have been queued.</div> : null}</Panel></div>
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
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}<InlineNotice tone="info" title="Review only">IMDb exports are parsed into persisted review rows. Uploading a snapshot creates no media, workflows, searches, release evidence, or downloads. TV rows remain unsupported until an exact season can be chosen.</InlineNotice><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Lists" value={String(lists.length)} note="Owner-supplied IMDb exports" icon={ListChecks} /><MetricCard label="Pending review" value={String(snapshot?.pending_count ?? 0)} note="No monitoring change has been applied" tone={snapshot?.pending_count ? "warn" : "neutral"} icon={SearchCheck} /><MetricCard label="Already cataloged" value={String(snapshot?.existing_count ?? 0)} note={`${snapshot?.unsupported_count ?? 0} unsupported rows`} tone="info" icon={CheckCircle2} /></div><div className="grid gap-4 xl:grid-cols-[1.3fr_380px]"><Panel className="!p-0" title="Import lists" subtitle="Persisted source configuration and latest bounded snapshot counts." action={<RefreshButton onClick={listsState.refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Source</TableHead><TableHead>Movie profile</TableHead><TableHead>Latest snapshot</TableHead><TableHead>Items</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{lists.map((item) => <TableRow key={item.id}><TableCell className="font-semibold">{item.name}</TableCell><TableCell>IMDb CSV export</TableCell><TableCell>{item.movie_quality_profile || "Type default"}</TableCell><TableCell>{item.latest_snapshot ? timeAgo(item.latest_snapshot.created_at) : "Never"}</TableCell><TableCell>{item.latest_snapshot?.item_count ?? 0}</TableCell><TableCell><Button size="sm" variant={effectiveSelectedID === item.id ? "default" : "outline"} onClick={() => setSelectedID(item.id)}>Review</Button></TableCell></TableRow>)}</TableBody></Table></div>{lists.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No import lists configured.</div> : null}</Panel><div className="space-y-4"><Panel title="Add IMDb export list" subtitle="This creates configuration only; it does not fetch or import anything."><form className="grid gap-3" onSubmit={create}><label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">Name<Input className="mm-input" value={name} onChange={(event) => setName(event.target.value)} placeholder="IMDb owner watchlist" required /></label><KeyValue label="Provider" value="IMDb CSV export" /><KeyValue label="Monitoring defaults" value="Movie type defaults; search off" /><Button type="submit" disabled={busy === "create"}>Save list</Button></form></Panel><Panel title="Review a new snapshot" subtitle={selected ? `Selected: ${selected.name}` : "Select or create a list first."}><form className="grid gap-3" onSubmit={upload}><Input className="mm-input" type="file" accept=".csv,text/csv" onChange={(event) => setFile(event.target.files?.[0] ?? null)} disabled={!selected} /><Button type="submit" disabled={!selected || !file || busy === "upload"}>Parse snapshot for review</Button></form></Panel></div></div><Panel className="!p-0" title={selected ? `${selected.name} · latest review` : "Latest review"} subtitle={snapshot ? `${snapshot.item_count} rows persisted ${new Date(snapshot.created_at).toLocaleString()}. Digest ${snapshot.source_digest.slice(0, 12)}…` : "No snapshot has been uploaded."}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>#</TableHead><TableHead>Title</TableHead><TableHead>Identity</TableHead><TableHead>Kind</TableHead><TableHead>Review state</TableHead><TableHead>Attention</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell>{item.ordinal}</TableCell><TableCell className="font-semibold">{item.title}{item.year ? ` (${item.year})` : ""}</TableCell><TableCell className="font-mono text-[9px]">IMDb {item.provider_id}</TableCell><TableCell>{pretty(item.media_kind)}</TableCell><TableCell><StatusChip tone={item.state === "already_cataloged" ? "good" : item.state === "pending_review" ? "warn" : "neutral"}>{pretty(item.state)}</StatusChip></TableCell><TableCell className="max-w-md text-[9px]">{item.attention || (item.state === "pending_review" ? "Awaiting an implemented monitoring review action." : "—")}</TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No persisted review rows.</div> : null}</Panel></div>
}

export function LiveBackupsScreen() {
  const { data, error, refresh } = useLiveData(backupsLoader)
  const [busy, setBusy] = React.useState(false)
  async function create() {
    setBusy(true)
    try { await createBackup(); toast.success("Encrypted backup queued"); refresh() }
    catch (cause) { toast.error(cause instanceof Error ? cause.message : "Could not queue backup") }
    finally { setBusy(false) }
  }
  if (!data && !error) return <LoadingPanel label="Loading durable backup records…" />
  const records = data?.records ?? []
  const verified = records.filter((item) => item.state === "verified")
  const active = records.some((item) => item.state === "queued" || item.state === "running")
  const latest = records[0]
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Verified backups" value={String(verified.length)} note="Durable records with restore verification" tone={verified.length ? "good" : "neutral"} icon={DatabaseBackup} /><MetricCard label="Latest state" value={latest ? pretty(latest.state) : "No records"} note={latest ? timeAgo(latest.created_at) : "No backup has been requested"} tone={latest?.state === "verified" ? "good" : latest?.state === "failed" ? "warn" : latest ? "info" : "neutral"} icon={Database} /><MetricCard label="Encryption key" value={data.configured ? "Configured" : "Unavailable"} note="External BACKUP_ENCRYPTION_KEY; value never returned" tone={data.configured ? "good" : "warn"} icon={ShieldCheck} /></div><InlineNotice tone={data.configured ? "info" : "warn"} title={data.configured ? "Verified encrypted backups" : "Backup creation is disabled"}>{data.configured ? "Each PostgreSQL custom dump is encrypted with chunked AES-256-GCM, checksummed, restored into an isolated temporary database, and accepted only at the exact running schema version." : "Configure a base64-encoded 32-byte BACKUP_ENCRYPTION_KEY outside the repository, then recreate only the API service. Existing .env files and integration secret values are never included in backups."}</InlineNotice><Panel className="!p-0" title="Media Manager backups" subtitle="Database, configuration references, and audit state are included. Secret values, media, AltMount content, and .env files are excluded." action={<div className="flex gap-2"><RefreshButton onClick={refresh} /><Button size="sm" disabled={!data.configured || busy || active} onClick={() => void create()}><DatabaseBackup />Create and verify</Button></div>}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Requested</TableHead><TableHead>State</TableHead><TableHead>Size</TableHead><TableHead>Schema</TableHead><TableHead>Attempts</TableHead><TableHead>Integrity</TableHead><TableHead>Download</TableHead></TableRow></TableHeader><TableBody>{records.map((item) => <TableRow key={item.id}><TableCell>{new Date(item.created_at).toLocaleString()}</TableCell><TableCell><StatusChip tone={item.state === "verified" ? "good" : item.state === "failed" ? "warn" : "info"}>{pretty(item.state)}</StatusChip>{item.last_error ? <div className="mt-1 text-[9px] text-[#c6a867]">{item.last_error}</div> : null}</TableCell><TableCell>{formatBytes(item.size_bytes)}</TableCell><TableCell>{item.schema_version ?? "—"}</TableCell><TableCell>{item.attempts}</TableCell><TableCell className="font-mono text-[9px]">{item.sha256 ? `${item.sha256.slice(0, 16)}…` : "—"}</TableCell><TableCell>{item.state === "verified" ? <Button asChild size="sm" variant="outline"><a href={`/api/v1/backups/${encodeURIComponent(item.id)}/download`}><Download />Encrypted file</a></Button> : "—"}</TableCell></TableRow>)}</TableBody></Table></div>{records.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No backup has been requested. No success state is inferred from the project tar archive.</div> : null}</Panel><div className="grid gap-4 xl:grid-cols-2"><Panel title="Restore verification" subtitle="A verified record is more than a successful dump command."><KeyValue label="Format" value={data.format} /><KeyValue label="Verification" value={data.restore_verification} /><KeyValue label="Production restore" value={data.production_restore ? "Available" : "Not implemented"} /></Panel><Panel title="Retention and safety" subtitle="No backup is deleted automatically without an approved retention policy."><KeyValue label="Automatic deletion" value={data.automatic_deletion ? "Enabled" : "Disabled"} /><KeyValue label="Media contents" value="Excluded" /><KeyValue label="Secret values and .env" value="Excluded" /></Panel></div></> : null}</div>
}

function SystemInfoContent({ data, mode, refresh }: { data: APISystemInfo; mode: "media-management" | "security" | "api"; refresh: () => void }) {
  if (mode === "media-management") return <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Source policy" value={pretty(data.altmount_source_policy)} note="AltMount content is never renamed or deleted" tone="good" icon={HardDrive} /><MetricCard label="Publication" value={pretty(data.publication_type)} note="Plex-safe managed link generations" tone="info" icon={FileCheck2} /><MetricCard label="Recycle retention" value={`${data.recycle_retention_days} days`} note="Managed links stay recoverable" icon={RefreshCw} /></div><Panel title="Enforced lifecycle policy" subtitle="These values report the running server contract; unsupported settings are not presented as editable."><KeyValue label="Upgrade replacement gate" value={pretty(data.upgrade_replacement_gate)} /><KeyValue label="AltMount source" value={pretty(data.altmount_source_policy)} /><KeyValue label="Publication type" value={pretty(data.publication_type)} /><KeyValue label="Recycle retention" value={`${data.recycle_retention_days} days`} /><InlineNotice tone="info" title="Safe replacement">An old current link is displaced only after the replacement is verified and Plex confirms exact path and provider identity.</InlineNotice></Panel></>
  if (mode === "security") return <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Administration" value={data.owner_only ? "Owner only" : "Not owner-only"} note="No multi-user administration" tone={data.owner_only ? "good" : "warn"} icon={ShieldCheck} /><MetricCard label="Browser boundary" value={pretty(data.browser_auth)} note={`Public edge: ${data.public_edge_owner}`} tone="info" icon={Globe2} /><MetricCard label="Webhook" value={data.seerr_webhook_enabled ? "Enabled" : "Disabled"} note="Seerr uses its separate authenticated route" icon={SearchCheck} /></div><Panel title="Running access boundaries" subtitle="Secret values are never returned by this endpoint." action={<RefreshButton onClick={refresh} />}><KeyValue label="Owner API authentication" value={pretty(data.api_auth)} /><KeyValue label="Browser authentication" value={pretty(data.browser_auth)} /><KeyValue label="Public edge owner" value={data.public_edge_owner} /><KeyValue label="Machine API keys" value={data.machine_api_keys ? "Enabled" : "Not implemented"} /><InlineNotice tone="info" title="Secret handling">Integration records expose environment variable names only. Credential values remain outside browser responses and durable audit detail.</InlineNotice></Panel></>
  return <div className="grid gap-4 xl:grid-cols-[1fr_1.2fr]"><Panel title="Owner API contract" subtitle="The live API is versioned and protected by the owner boundary." action={<RefreshButton onClick={refresh} />}><KeyValue label="Version" value={data.version || "Unversioned development build"} /><KeyValue label="Prefix" value={<span className="font-mono">{data.api_prefix}</span>} /><KeyValue label="Authentication" value={pretty(data.api_auth)} /><KeyValue label="Machine API keys" value={data.machine_api_keys ? "Enabled" : "Not implemented"} /></Panel><Panel title="Supported access" subtitle="Browser requests use the gateway-injected owner bearer. External scoped keys are not claimed."><pre className="overflow-x-auto rounded-xl border border-white/[0.06] bg-[#090c10] p-4 text-[9.5px] leading-5 text-[#aeb6c1]">{`GET ${data.api_prefix}/dashboard\nGET ${data.api_prefix}/media\nGET ${data.api_prefix}/workflows\nGET ${data.api_prefix}/wanted\nGET ${data.api_prefix}/files\nGET ${data.api_prefix}/activity\nGET ${data.api_prefix}/system/health\nGET ${data.api_prefix}/system/tasks`}</pre><InlineNotice tone="warn" title="No machine-key lifecycle">Scoped machine keys, issuance, rotation, and revocation are not implemented. Do not reuse the internal owner bearer outside the gateway.</InlineNotice></Panel></div>
}

function HealthContent({ data, refresh }: { data: APISystemHealth; refresh: () => void }) {
  const disabled = data.components.filter((item) => item.configuration_state !== "configured").length
  return <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="PostgreSQL" value={pretty(data.database_state)} note={`Schema ${data.schema_version}/${data.expected_schema} · ${data.database_latency_ms} ms`} tone="good" icon={Database} /><MetricCard label="Worker events" value={String(data.outbox.pending)} note={`${data.outbox.due} due · ${data.outbox.retrying} retried`} tone={data.outbox.due > 0 && data.outbox.retrying > 0 ? "warn" : "info"} icon={Activity} /><MetricCard label="Configuration attention" value={String(disabled)} note="Disabled or awaiting commission" tone={disabled ? "warn" : "good"} icon={HeartPulse} /></div><Panel className="!p-0" title="Component state" subtitle="Configuration and last observed operations; this endpoint does not manufacture connection-test success." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Component</TableHead><TableHead>Type</TableHead><TableHead>Configuration</TableHead><TableHead>Latest observed outcome</TableHead><TableHead>Updated</TableHead></TableRow></TableHeader><TableBody><TableRow><TableCell className="font-semibold">PostgreSQL</TableCell><TableCell>database</TableCell><TableCell><StatusChip tone="good">Ready</StatusChip></TableCell><TableCell>Schema and query ready</TableCell><TableCell>{new Date(data.checked_at).toLocaleTimeString()}</TableCell></TableRow>{data.components.map((item) => <TableRow key={item.id}><TableCell className="font-semibold">{item.name}</TableCell><TableCell>{item.kind}</TableCell><TableCell><StatusChip tone={item.configuration_state === "configured" ? "info" : "warn"}>{pretty(item.configuration_state)}</StatusChip></TableCell><TableCell>{item.last_outcome ? pretty(item.last_outcome) : "No live operation observed"}</TableCell><TableCell>{item.budget_updated_at ? timeAgo(item.budget_updated_at) : timeAgo(item.updated_at)}</TableCell></TableRow>)}</TableBody></Table></div></Panel></>
}
