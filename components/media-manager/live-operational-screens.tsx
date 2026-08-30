"use client"

import * as React from "react"
import Link from "next/link"
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Database,
  FileCheck2,
  Files,
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
} from "lucide-react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import {
  loadActivity,
  loadDashboard,
  loadMedia,
  loadManagedFile,
  loadManagedFiles,
  loadReleaseCandidates,
  loadSources,
  loadSystemHealth,
  loadSystemInfo,
  loadTasks,
  loadWanted,
  type APIActivity,
  type APIReleaseCandidate,
  type APISourceStatus,
  type APISystemHealth,
  type APISystemInfo,
  type APITasks,
} from "@/lib/media-manager-api"
import { InlineNotice, KeyValue, MetricCard, Panel, StatusChip } from "./ui"

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

const dashboardLoader = (signal: AbortSignal) => loadDashboard(signal)
const activityLoader = (signal: AbortSignal) => loadActivity(signal)
const filesLoader = (signal: AbortSignal) => loadManagedFiles(signal)
const releasesLoader = (signal: AbortSignal) => loadReleaseCandidates(signal)
const sourcesLoader = (signal: AbortSignal) => loadSources(signal)
const healthLoader = (signal: AbortSignal) => loadSystemHealth(signal)
const wantedLoader = (signal: AbortSignal) => loadWanted(signal)
const tasksLoader = (signal: AbortSignal) => loadTasks(signal)
const systemInfoLoader = (signal: AbortSignal) => loadSystemInfo(signal)

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

export function LiveLibraryScreen() {
  const [query, setQuery] = React.useState("")
  const [kind, setKind] = React.useState<"All" | "Movie" | "Series">("All")
  const [view, setView] = React.useState<"grid" | "list">("grid")
  const { data, error, refresh } = useLiveData(loadMedia)
  const records = data ?? []
  const filtered = records.filter((item) => (kind === "All" || item.kind === kind)
    && `${item.title} ${item.year} ${item.externalIds.join(" ")}`.toLowerCase().includes(query.toLowerCase()))
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <Panel className="!p-3"><div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between"><div className="relative min-w-0 flex-1 lg:max-w-md"><Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[#758091]" /><Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search title, year, TMDB, TVDB, IMDb…" className="mm-input pl-9" /></div><div className="flex flex-wrap items-center gap-2">{(["All", "Movie", "Series"] as const).map((value) => <Button key={value} size="sm" variant={kind === value ? "default" : "outline"} onClick={() => setKind(value)}>{value}</Button>)}<Button variant="outline" size="icon" aria-label="Grid view" onClick={() => setView("grid")}><Grid2X2 /></Button><Button variant="outline" size="icon" aria-label="List view" onClick={() => setView("list")}><List /></Button><RefreshButton onClick={refresh} /></div></div></Panel>
    <div className="flex items-center justify-between px-1 text-[10px] text-[#7f8a9a]"><span>{filtered.length} of {records.length} live records</span><span>Sorted by title</span></div>
    {!data && !error ? <LoadingPanel label="Loading the live catalog…" /> : filtered.length === 0 ? <Panel><div className="py-12 text-center"><div className="text-sm font-bold text-white">{records.length === 0 ? "Your catalog is empty" : "No media matched"}</div><div className="mt-2 text-[10px] text-[#7f8a9a]">{records.length === 0 ? "Add a movie or an exact TV season pack to begin." : "Change the search or media type filter."}</div>{records.length === 0 ? <Button asChild className="mt-4" size="sm"><Link href="/discover"><Plus /> Add media</Link></Button> : null}</div></Panel> : view === "grid" ? <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4 2xl:grid-cols-6">{filtered.map((item) => <Link key={item.id} href={item.kind === "Movie" ? `/media/movies/${item.id}` : `/media/series/${item.id}`} className="group overflow-hidden rounded-[15px] border border-white/[0.07] bg-[#0e1217] transition hover:-translate-y-0.5 hover:border-[#445064]"><div className="grid aspect-[2/3] place-items-center bg-gradient-to-b from-[#171d26] to-[#0b0f14]"><Library className="size-10 text-[#536076]" /></div><div className="p-3"><div className="truncate text-[11px] font-extrabold text-[#edf1f6]">{item.title}</div><div className="mt-1 text-[9.5px] text-[#7f8997]">{item.year || "Year unknown"} · {item.kind}</div><div className="mt-2 truncate text-[9px] text-[#8e98a6]">{item.externalIds.join(" · ") || "No verified external identity"}</div></div></Link>)}</div> : <Panel className="!p-0"><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Title</TableHead><TableHead>Type</TableHead><TableHead>Verified identities</TableHead><TableHead>Catalog state</TableHead></TableRow></TableHeader><TableBody>{filtered.map((item) => <TableRow key={item.id}><TableCell><Link className="font-semibold text-[#e9edf3] hover:text-[#aebdff]" href={item.kind === "Movie" ? `/media/movies/${item.id}` : `/media/series/${item.id}`}>{item.title} <span className="text-[#788392]">({item.year || "—"})</span></Link></TableCell><TableCell>{item.kind}</TableCell><TableCell className="font-mono text-[9px]">{item.externalIds.join(" · ") || "—"}</TableCell><TableCell><StatusChip>Cataloged</StatusChip></TableCell></TableRow>)}</TableBody></Table></div></Panel>}
  </div>
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
    <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Current" value={String(current)} note="Active library link versions" tone="good" icon={Files} /><MetricCard label="Plex confirmed" value={String(confirmed)} note="Exact observed path and identity" tone="info" icon={CheckCircle2} /><MetricCard label="Quarantined" value={String(attention)} note="Publication needs review" tone={attention ? "warn" : "good"} icon={AlertTriangle} /></div>
    {!data && !error ? <LoadingPanel /> : <Panel className="!p-0" title="Managed files" subtitle="Real symlink publications and their immutable AltMount provenance." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Media</TableHead><TableHead>Destination</TableHead><TableHead>Source</TableHead><TableHead>Generation</TableHead><TableHead>State</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => <TableRow key={item.id}><TableCell><Link className="font-semibold text-[#e5eaf0] hover:text-[#aebdff]" href={`/files/${item.id}`}>{item.title}{item.year ? ` (${item.year})` : ""}</Link><div className="mt-1 text-[9px] text-[#707c8c]">{item.root_name}</div></TableCell><TableCell className="max-w-xs break-all font-mono text-[9px]">{item.destination_relative}</TableCell><TableCell className="max-w-xs break-all font-mono text-[9px]">{item.source_relative}</TableCell><TableCell>{item.generation}</TableCell><TableCell><StatusChip tone={fileTone(item.state, item.plex_confirmed)}>{pretty(item.state)}</StatusChip></TableCell></TableRow>)}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No files have been published. AltMount source remains read-only.</div> : null}</Panel>}
  </div>
}

export function LiveFileDetailScreen({ fileId }: { fileId: string }) {
  const loader = React.useCallback((signal: AbortSignal) => loadManagedFile(fileId, signal), [fileId])
  const { data, error, refresh } = useLiveData(loader)
  if (!data && !error) return <LoadingPanel label="Loading file provenance…" />
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <>
    <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Version" value={String(data.generation)} note="Immutable link generation" icon={Files} /><MetricCard label="Publication" value={pretty(data.state)} note={data.verified_at ? `Verified ${timeAgo(data.verified_at)}` : "Not verified yet"} tone={fileTone(data.state, data.plex_confirmed)} icon={FileCheck2} /><MetricCard label="Plex evidence" value={data.plex_confirmed ? "Confirmed" : "Not confirmed"} note="Exact path and provider identity" tone={data.plex_confirmed ? "good" : "neutral"} icon={SearchCheck} /></div>
    <div className="grid gap-4 xl:grid-cols-[1.3fr_1fr]"><Panel title="Path relationship" subtitle="Media Manager publishes a symlink; the AltMount source is never renamed or modified."><KeyValue label="Library root" value={data.root_name} /><KeyValue label="Destination" value={<span className="break-all font-mono">{data.destination_relative}</span>} /><KeyValue label="AltMount source" value={<span className="break-all font-mono">{data.source_relative}</span>} /><KeyValue label="Plex sees target" value={<span className="break-all font-mono">{data.plex_visible_target}</span>} /></Panel><Panel title="Lifecycle evidence" action={<RefreshButton onClick={refresh} />}><KeyValue label="State" value={pretty(data.state)} /><KeyValue label="Verified" value={data.verified_at ? new Date(data.verified_at).toLocaleString() : "—"} /><KeyValue label="Displaced" value={data.displaced_at ? new Date(data.displaced_at).toLocaleString() : "—"} /><KeyValue label="Removed" value={data.removed_at ? new Date(data.removed_at).toLocaleString() : "—"} /><Button asChild variant="outline" className="mt-4 w-full"><Link href={data.media_kind === "movie" ? `/media/movies/${data.media_id}` : `/media/series/${data.media_id}`}>Open media record</Link></Button></Panel></div>
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

export function LiveIndexerDetailScreen({ indexerId }: { indexerId: string }) {
  const { data, error, refresh } = useLiveData(sourcesLoader)
  const item = data?.find((source) => source.id === indexerId && source.kind === "newznab")
  if (!data && !error) return <LoadingPanel label="Loading indexer state…" />
  if (data && !item) return <ErrorNotice error="The Newznab indexer was not found." />
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{item ? <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Configuration" value={pretty(item.configuration_state)} note="Enabled only after commissioning" tone={item.configuration_state === "configured" ? "info" : "warn"} icon={Globe2} /><MetricCard label="Minimum interval" value={item.minimum_interval_ms ? `${item.minimum_interval_ms} ms` : "Not observed"} note="One in-flight request per indexer" icon={Gauge} /><MetricCard label="Latest outcome" value={item.last_outcome ? pretty(item.last_outcome) : "None"} note={item.budget_updated_at ? timeAgo(item.budget_updated_at) : "No runtime request recorded"} tone={item.last_outcome === "auth_failed" || item.last_outcome === "system_failure" ? "warn" : "neutral"} icon={Activity} /></div><div className="grid gap-4 xl:grid-cols-[1.2fr_1fr]"><Panel title={item.name} subtitle="Persisted Newznab configuration. Secrets remain environment references."><KeyValue label="Endpoint" value={<span className="break-all font-mono">{sourceEndpoint(item)}</span>} /><KeyValue label="Secret reference" value={<span className="font-mono">{item.secret_ref ?? "—"}</span>} /><KeyValue label="Default" value={item.is_default ? "Yes" : "No"} /><KeyValue label="Commissioned mode" value={item.config.mode_verified === true ? String(item.config.mode ?? "search") : "Not verified"} /></Panel><Panel title="Pacing state" action={<RefreshButton onClick={refresh} />}><KeyValue label="Next allowed" value={item.next_allowed_at ? new Date(item.next_allowed_at).toLocaleString() : "No persisted delay"} /><KeyValue label="Cooldown" value={item.cooldown_until ? new Date(item.cooldown_until).toLocaleString() : "None"} /><KeyValue label="Consecutive failures" value={String(item.consecutive_failure)} /><InlineNotice tone="info" title="No automatic test">Opening this screen never contacts the indexer. Commissioning remains the only owner-triggered diagnostic path.</InlineNotice></Panel></div></> : null}</div>
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

function SystemInfoContent({ data, mode, refresh }: { data: APISystemInfo; mode: "media-management" | "security" | "api"; refresh: () => void }) {
  if (mode === "media-management") return <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Source policy" value={pretty(data.altmount_source_policy)} note="AltMount content is never renamed or deleted" tone="good" icon={HardDrive} /><MetricCard label="Publication" value={pretty(data.publication_type)} note="Plex-safe managed link generations" tone="info" icon={FileCheck2} /><MetricCard label="Recycle retention" value={`${data.recycle_retention_days} days`} note="Managed links stay recoverable" icon={RefreshCw} /></div><Panel title="Enforced lifecycle policy" subtitle="These values report the running server contract; unsupported settings are not presented as editable."><KeyValue label="Upgrade replacement gate" value={pretty(data.upgrade_replacement_gate)} /><KeyValue label="AltMount source" value={pretty(data.altmount_source_policy)} /><KeyValue label="Publication type" value={pretty(data.publication_type)} /><KeyValue label="Recycle retention" value={`${data.recycle_retention_days} days`} /><InlineNotice tone="info" title="Safe replacement">An old current link is displaced only after the replacement is verified and Plex confirms exact path and provider identity.</InlineNotice></Panel></>
  if (mode === "security") return <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Administration" value={data.owner_only ? "Owner only" : "Not owner-only"} note="No multi-user administration" tone={data.owner_only ? "good" : "warn"} icon={ShieldCheck} /><MetricCard label="Browser boundary" value={pretty(data.browser_auth)} note={`Public edge: ${data.public_edge_owner}`} tone="info" icon={Globe2} /><MetricCard label="Webhook" value={data.seerr_webhook_enabled ? "Enabled" : "Disabled"} note="Seerr uses its separate authenticated route" icon={SearchCheck} /></div><Panel title="Running access boundaries" subtitle="Secret values are never returned by this endpoint." action={<RefreshButton onClick={refresh} />}><KeyValue label="Owner API authentication" value={pretty(data.api_auth)} /><KeyValue label="Browser authentication" value={pretty(data.browser_auth)} /><KeyValue label="Public edge owner" value={data.public_edge_owner} /><KeyValue label="Machine API keys" value={data.machine_api_keys ? "Enabled" : "Not implemented"} /><InlineNotice tone="info" title="Secret handling">Integration records expose environment variable names only. Credential values remain outside browser responses and durable audit detail.</InlineNotice></Panel></>
  return <div className="grid gap-4 xl:grid-cols-[1fr_1.2fr]"><Panel title="Owner API contract" subtitle="The live API is versioned and protected by the owner boundary." action={<RefreshButton onClick={refresh} />}><KeyValue label="Version" value={data.version || "Unversioned development build"} /><KeyValue label="Prefix" value={<span className="font-mono">{data.api_prefix}</span>} /><KeyValue label="Authentication" value={pretty(data.api_auth)} /><KeyValue label="Machine API keys" value={data.machine_api_keys ? "Enabled" : "Not implemented"} /></Panel><Panel title="Supported access" subtitle="Browser requests use the gateway-injected owner bearer. External scoped keys are not claimed."><pre className="overflow-x-auto rounded-xl border border-white/[0.06] bg-[#090c10] p-4 text-[9.5px] leading-5 text-[#aeb6c1]">{`GET ${data.api_prefix}/dashboard\nGET ${data.api_prefix}/media\nGET ${data.api_prefix}/workflows\nGET ${data.api_prefix}/wanted\nGET ${data.api_prefix}/files\nGET ${data.api_prefix}/activity\nGET ${data.api_prefix}/system/health\nGET ${data.api_prefix}/system/tasks`}</pre><InlineNotice tone="warn" title="No machine-key lifecycle">Scoped machine keys, issuance, rotation, and revocation are not implemented. Do not reuse the internal owner bearer outside the gateway.</InlineNotice></Panel></div>
}

function HealthContent({ data, refresh }: { data: APISystemHealth; refresh: () => void }) {
  const disabled = data.components.filter((item) => item.configuration_state !== "configured").length
  return <><div className="grid gap-3 sm:grid-cols-3"><MetricCard label="PostgreSQL" value={pretty(data.database_state)} note={`Schema ${data.schema_version}/${data.expected_schema} · ${data.database_latency_ms} ms`} tone="good" icon={Database} /><MetricCard label="Worker events" value={String(data.outbox.pending)} note={`${data.outbox.due} due · ${data.outbox.retrying} retried`} tone={data.outbox.due > 0 && data.outbox.retrying > 0 ? "warn" : "info"} icon={Activity} /><MetricCard label="Configuration attention" value={String(disabled)} note="Disabled or awaiting commission" tone={disabled ? "warn" : "good"} icon={HeartPulse} /></div><Panel className="!p-0" title="Component state" subtitle="Configuration and last observed operations; this endpoint does not manufacture connection-test success." action={<RefreshButton onClick={refresh} />}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Component</TableHead><TableHead>Type</TableHead><TableHead>Configuration</TableHead><TableHead>Latest observed outcome</TableHead><TableHead>Updated</TableHead></TableRow></TableHeader><TableBody><TableRow><TableCell className="font-semibold">PostgreSQL</TableCell><TableCell>database</TableCell><TableCell><StatusChip tone="good">Ready</StatusChip></TableCell><TableCell>Schema and query ready</TableCell><TableCell>{new Date(data.checked_at).toLocaleTimeString()}</TableCell></TableRow>{data.components.map((item) => <TableRow key={item.id}><TableCell className="font-semibold">{item.name}</TableCell><TableCell>{item.kind}</TableCell><TableCell><StatusChip tone={item.configuration_state === "configured" ? "info" : "warn"}>{pretty(item.configuration_state)}</StatusChip></TableCell><TableCell>{item.last_outcome ? pretty(item.last_outcome) : "No live operation observed"}</TableCell><TableCell>{item.budget_updated_at ? timeAgo(item.budget_updated_at) : timeAgo(item.updated_at)}</TableCell></TableRow>)}</TableBody></Table></div></Panel></>
}
