"use client"

import * as React from "react"
import { LIVE_REFRESH_MS, useAutoRefresh } from "@/hooks/use-auto-refresh"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Activity, ArchiveRestore, Copy, FolderTree, Library, LoaderCircle, Plus, RefreshCw, Search, Settings2, Trash2, Upload } from "lucide-react"
import { toast } from "sonner"

import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import {
  approveProwlarrImport,
  approveAdoptionBatch,
  cancelWorkflow,
  commissionIntegration,
  confirmRealFilePurge,
  createExistingWorkflow,
  createAdoptionBatch,
  createCollection,
  createIntegration,
  createLibraryRoot,
  createMedia,
  executeAdoptionBatch,
  createQualityProfile,
  updateQualityProfile,
  loadIntegrations,
  loadAdoptionBatch,
  loadAdoptionBatches,
  loadCatalogMedia,
  loadCollections,
  loadLibraryRoots,
  loadMediaDetail,
  loadDiscoveryMetadata,
  loadQualityProfiles,
  loadSubtitleVerifications,
  loadRecycle,
  loadSeerrRouting,
  loadWorkflows,
  openAcquisitionRound,
  reconcileAltMount,
  recycleMedia,
  deleteCollection,
  deleteSeerrRoute,
  restoreRecycle,
  restoreAdoptedFiles,
  reviewAdoptionItem,
  previewCollection,
  refreshMediaMetadata,
  reverifyRetainedOutput,
  saveSeerrRoute,
  searchMetadata,
  searchWorkflow,
  uploadProwlarrBackup,
  type APIIntegration,
  type APIAdoptionBatch,
  type APIAdoptionItem,
  type APICollection,
  type APIDiscoveryDetail,
  type APIDiscoverySearchResult,
  type APILibraryRoot,
  type APIMedia,
  type APIMediaDetail,
  type APIQualityProfile,
  type APISubtitleVerification,
  type APIRecycleEntry,
  type APISeerrObservedFolder,
  type APISeerrRoute,
  type APISeerrRouting,
  type APIProwlarrImport,
  type APIWorkflow,
} from "@/lib/media-manager-api"
import { describeStatus, InlineNotice, KeyValue, Panel, StateBadge, StatusChip } from "./ui"
import { AttentionCard, CoverageBar, CoverageLegend, DownloadProgress, MiniPoster, StageStepper, titleHue } from "./console"
import { isActivelyAcquiring, isCompleted } from "@/lib/workflow-states"
import { cn } from "@/lib/utils"
import { retryPlan, workflowStep, workflowSteps } from "@/lib/workflow-presentation"
import { MetadataArtwork, MetadataSourceLink } from "./metadata-artwork"
import { episodeStatus, formatSeasonVariant, parseSeriesVariant, rollupSeriesSeasons, seasonAddCheck } from "@/lib/series-variants"

function prettyState(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase())
}

function timeAgo(value: string) {
  const seconds = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 1000))
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
  return `${Math.floor(seconds / 86400)}d ago`
}

function compactBytes(value = 0) {
  if (value < 1024) return `${value} B`
  const units = ["KiB", "MiB", "GiB", "TiB"]
  let amount = value / 1024
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit += 1 }
  return `${amount.toFixed(amount >= 10 ? 0 : 1)} ${units[unit]}`
}

function ErrorNotice({ error }: { error: string }) {
  return <InlineNotice tone="bad" title="Could not complete this operation">{error}</InlineNotice>
}

function EmptyPanel({ title, copy, href, action }: { title: string; copy: string; href?: string; action?: string }) {
  return <Panel><div className="grid min-h-44 place-items-center text-center"><div><Library className="mx-auto size-7 text-fg-subtle" /><h2 className="mt-3 text-sm font-semibold text-fg">{title}</h2><p className="mt-2 max-w-md text-[12px] leading-5 text-fg-subtle">{copy}</p>{href && action ? <Button asChild className="mt-4" size="sm"><Link href={href}><Plus /> {action}</Link></Button> : null}</div></div></Panel>
}

export function LiveWorkflowScreen() {
  const [workflows, setWorkflows] = React.useState<APIWorkflow[]>([])
  const [error, setError] = React.useState("")
  const [busy, setBusy] = React.useState("")
  const [replacementConfirmations, setReplacementConfirmations] = React.useState<Record<string, string>>({})
  const [filter, setFilter] = React.useState<"all" | WorkflowGroupKey>("all")
  // Artwork is decoration; it loads once and never blocks the table.
  const [posters, setPosters] = React.useState<Map<string, string | undefined>>(() => new Map())
  React.useEffect(() => { const controller = new AbortController(); loadCatalogMedia(controller.signal).then((items) => setPosters(new Map(items.map((item) => [item.id, item.metadata?.poster_url])))).catch(() => undefined); return () => controller.abort() }, [])
  const refresh = React.useCallback(async () => {
    try { setWorkflows(await loadWorkflows(false)); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load workflows") }
  }, [])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  useAutoRefresh(() => void refresh(), 8000)
  async function act(workflow: APIWorkflow, action: "search" | "round" | "reconcile" | "cancel") {
    setBusy(workflow.id)
    try {
      if (action === "search") await searchWorkflow(workflow.id, replacementConfirmations[workflow.id] ?? "")
      else if (action === "round") await openAcquisitionRound(workflow.id)
      else if (action === "reconcile") await reconcileAltMount(workflow.id)
      else await cancelWorkflow(workflow.id)
      toast.success(action === "search" ? "Search queued" : action === "round" ? "New two-slot round started" : action === "reconcile" ? "Same AltMount job queued for reconciliation" : "Workflow cancelled")
      setReplacementConfirmations((current) => ({ ...current, [workflow.id]: "" }))
      await refresh()
    } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Operation failed") } finally { setBusy("") }
  }
  const groups = workflowGroups(workflows)
  const visible = filter === "all" ? groups : groups.filter((group) => group.key === filter)
  function row(workflow: APIWorkflow) {
    const replacementPhrase = replacementConfirmations[workflow.id] ?? ""
    const defaultSearchAllowed = ["monitored", "waiting", "needs_attention", "held"].includes(workflow.state)
    const searchAllowed = workflow.search_allowed ?? defaultSearchAllowed
    const infrastructureRecovery = ["altmount_identity_unresolved", "altmount_identity_ambiguous", "altmount_output_unavailable", "altmount_job_attention", "season_expectation_missing", "output_inspection_failed", "infrastructure_attention"].includes(workflow.attention_code ?? "")
    const exhaustedDiscovery = workflow.retry_discovery_state === "waiting" || workflow.retry_discovery_state === "ready"
    const roundLabel = workflow.round_ordinal ? `Round ${workflow.round_ordinal} · ${workflow.round_slots_used ?? 0}/${workflow.round_attempt_limit ?? 2} slots` : "No submissions yet"
    return <TableRow key={workflow.id}><TableCell><div className="flex w-[200px] items-center gap-2.5"><MiniPoster src={posters.get(workflow.media_id)} title={workflow.title} /><div className="min-w-0"><Link className="block truncate text-[12.5px] font-semibold text-fg hover:underline" href={workflow.media_kind === "movie" ? `/media/movies/${workflow.media_id}` : `/media/series/${workflow.media_id}`}>{workflow.title}</Link><div className="text-[10.5px] text-fg-subtle">{workflow.year ?? "—"} · <span className="font-mono">{workflow.variant}</span></div></div></div></TableCell><TableCell>{workflow.download_percent !== undefined ? <DownloadProgress workflow={workflow} className="w-[140px]" /> : <StageStepper state={workflow.state} className="w-[140px]" />}</TableCell><TableCell className="w-[220px] min-w-[220px] whitespace-normal [overflow-wrap:anywhere]"><StateBadge state={workflow.state} />{workflow.attention_code ? <div className="mt-1 text-[12px] font-semibold uppercase tracking-wide text-warn">{prettyState(workflow.attention_code)}</div> : null}{workflow.attention_detail ? <div className="mt-1 text-[12px] leading-4 text-warn">{workflow.attention_detail}</div> : null}</TableCell><TableCell className="w-[200px] min-w-[200px] whitespace-normal [overflow-wrap:anywhere]"><div className="text-[12px] text-fg-secondary">{roundLabel} · {workflow.acquisition_attempts ?? 0} lifetime</div><div className="mt-1 text-[12px] text-fg-subtle">{workflow.retained_output_count ?? 0} retained ({compactBytes(workflow.retained_output_bytes)}) · {workflow.excluded_release_count ?? 0} exact exclusions</div>{workflow.retry_discovery_state === "waiting" && workflow.next_rescan_at ? <div className="mt-1 text-[12px] text-fg-muted">Next paced discovery {new Date(workflow.next_rescan_at).toLocaleString()}</div> : null}{workflow.retry_discovery_state === "ready" ? <div className="mt-1 text-[12px] font-semibold text-brand-text">New eligible exact identity found; owner approval required.{workflow.ready_release_title ? <span className="mt-1 block font-normal text-fg-secondary">{workflow.ready_release_title}{workflow.ready_indexer_name ? ` · ${workflow.ready_indexer_name}` : ""}{workflow.ready_first_observed_at ? ` · first seen ${timeAgo(workflow.ready_first_observed_at)}` : ""}</span> : null}</div> : null}{workflow.last_discovery_at ? <div className="mt-1 text-[12px] text-fg-subtle">Last discovery {workflow.last_discovery_complete ? "complete" : "partial"} · {timeAgo(workflow.last_discovery_at)}</div> : null}{workflow.search_blocked_reason ? <div className="mt-1 max-w-56 text-[12px] leading-4 text-warn">{workflow.search_blocked_reason}</div> : null}</TableCell><TableCell className="whitespace-nowrap text-[12px] text-fg-subtle">{timeAgo(workflow.updated_at)}</TableCell><TableCell><div className="flex w-[180px] flex-col gap-1">{workflow.adopted_replacement_confirmation_required ? <Input className="mm-input font-mono text-[12px]" value={replacementPhrase} onChange={(event) => setReplacementConfirmations((current) => ({ ...current, [workflow.id]: event.target.value }))} placeholder="REPLACE ADOPTED FILE AFTER PLEX VERIFICATION" autoComplete="off" /> : null}<div className="flex flex-wrap gap-1">{infrastructureRecovery ? <Button size="sm" variant="outline" disabled={busy === workflow.id} onClick={() => void act(workflow, "reconcile")}><RefreshCw /> Reconcile same job</Button> : exhaustedDiscovery ? <Button size="sm" variant="outline" disabled={busy === workflow.id || !workflow.open_next_round_allowed} onClick={() => void act(workflow, "round")}><Search /> Start next round</Button> : <Button size="sm" variant="outline" disabled={busy === workflow.id || !searchAllowed || workflow.adopted_replacement_confirmation_required && replacementPhrase !== "REPLACE ADOPTED FILE AFTER PLEX VERIFICATION"} onClick={() => void act(workflow, "search")}><Search /> {workflow.state === "needs_attention" ? "Retry" : "Search"}</Button>}<Button size="sm" variant="ghost" disabled={busy === workflow.id || !["monitored", "waiting", "searching", "held", "blocked", "needs_attention"].includes(workflow.state)} onClick={() => void act(workflow, "cancel")}>Cancel</Button></div></div></TableCell></TableRow>
  }
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="flex flex-wrap items-center gap-2" aria-label="Workflow filters">
      {[{ key: "all" as const, label: "All", count: workflows.length }, ...groups.map((group) => ({ key: group.key, label: group.label, count: group.items.length }))].map((option) => <button key={option.key} type="button" aria-pressed={filter === option.key} onClick={() => setFilter(option.key)} className={cn("h-[30px] rounded-lg border px-3 text-[12px] font-semibold outline-none transition focus-visible:ring-2 focus-visible:ring-ring", filter === option.key ? "border-brand-line bg-brand-soft text-fg" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg")}>{option.label}{" "}<em className={cn("ml-1 font-medium not-italic tabular-nums", filter === option.key ? "text-brand-text" : option.key === "attention" && option.count ? "text-warn" : "text-fg-subtle")}>{option.count}</em></button>)}
      <Button variant="outline" size="sm" className="ml-auto" onClick={() => void refresh()}><RefreshCw /> Refresh</Button>
    </div>
    <Panel flush title="Workflow activity" subtitle="Durable pipelines survive restarts and retry recoverable failures on their own; pending work is never shown as success.">
      <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Item</TableHead><TableHead>Pipeline</TableHead><TableHead>State</TableHead><TableHead>Recovery</TableHead><TableHead>Updated</TableHead><TableHead>Actions</TableHead></TableRow></TableHeader><TableBody>{visible.filter((group) => group.items.length).map((group) => <React.Fragment key={group.key}><TableRow className="hover:bg-transparent"><TableCell colSpan={6} className="bg-surface py-[7px] text-[10.5px] font-bold uppercase tracking-[0.1em] text-fg-subtle">{group.label} · {group.hint}</TableCell></TableRow>{group.items.map(row)}</React.Fragment>)}</TableBody></Table></div>
      {workflows.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No workflows yet.</div> : null}
      <div className="border-t border-line px-4 py-2.5 text-[11.5px] text-fg-subtle">Failures Media Manager can retry on its own resolve here without asking; only Needs attention asks you for anything.</div>
    </Panel>
  </div>
}

type WorkflowGroupKey = "attention" | "active" | "waiting" | "completed"

/** Groups workflows by what they need: you, time, or nothing at all. */
function workflowGroups(workflows: APIWorkflow[]): Array<{ key: WorkflowGroupKey; label: string; hint: string; items: APIWorkflow[] }> {
  const attention = workflows.filter((workflow) => workflow.owner_action_required || ["needs_attention", "blocked", "quarantined", "transport_unknown"].includes(workflow.state))
  const rest = workflows.filter((workflow) => !attention.includes(workflow))
  const active = rest.filter((workflow) => isActivelyAcquiring(workflow.state))
  const completed = rest.filter((workflow) => isCompleted(workflow.state) || workflow.state === "cancelled")
  const waiting = rest.filter((workflow) => !active.includes(workflow) && !completed.includes(workflow))
  return [
    { key: "attention", label: "Needs attention", hint: "waiting on your decision", items: attention },
    { key: "active", label: "Active", hint: "moving toward Plex", items: active },
    { key: "waiting", label: "Scheduled & waiting", hint: "waiting is normal", items: waiting },
    { key: "completed", label: "Completed", hint: "confirmed or closed", items: completed },
  ]
}

function WorkflowTable({ workflows, empty }: { workflows: APIWorkflow[]; empty: string }) {
  return <Panel flush title="Recent active workflows" subtitle="Current state from PostgreSQL."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Media</TableHead><TableHead>Scope</TableHead><TableHead>State</TableHead><TableHead>Updated</TableHead></TableRow></TableHeader><TableBody>{workflows.map((workflow) => <TableRow key={workflow.id}><TableCell className="font-semibold text-fg">{workflow.title}</TableCell><TableCell className="font-mono text-[12px]">{workflow.variant}</TableCell><TableCell><StateBadge state={workflow.state} /></TableCell><TableCell>{timeAgo(workflow.updated_at)}</TableCell></TableRow>)}</TableBody></Table></div>{workflows.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">{empty}</div> : null}</Panel>
}

function parseSeasonSpec(value: string) {
  const seen = new Set<number>()
  return value.split(",").map((part) => {
    const match = /^\s*(\d{1,3})\s*:\s*(\d{1,3})\s*$/.exec(part)
    if (!match) throw new Error("Use season:episode-count, for example 1:10, 2:8")
    const season = Number(match[1]); const count = Number(match[2])
    if (season > 999 || count < 1 || count > 999 || seen.has(season)) throw new Error("Season snapshots must be unique and within 0–999")
    seen.add(season)
    return { number: season, monitored: true, search_immediately: true, prefer_season_pack: true, episodes: Array.from({ length: count }, (_, index) => ({ number: index + 1 })) }
  })
}

function parseSeasonNumbers(value: string) {
  const numbers = value.split(",").map((part) => Number(part.trim()))
  if (numbers.length === 0 || numbers.some((number) => !Number.isInteger(number) || number < 0 || number > 999) || new Set(numbers).size !== numbers.length) throw new Error("Use unique season numbers, for example 1, 2")
  return numbers
}

export function LiveDiscoverScreen() {
  const router = useRouter()
  const [kind, setKind] = React.useState<"movie" | "series">("movie")
  const [title, setTitle] = React.useState("")
  const [year, setYear] = React.useState("")
  const [provider, setProvider] = React.useState("tmdb")
  const [providerID, setProviderID] = React.useState("")
  const [seasonSpec, setSeasonSpec] = React.useState("1:10")
  const [metadataQuery, setMetadataQuery] = React.useState("")
  const [metadataResults, setMetadataResults] = React.useState<APIDiscoverySearchResult[]>([])
  const [metadataDetail, setMetadataDetail] = React.useState<APIDiscoveryDetail | null>(null)
  const [metadataBusy, setMetadataBusy] = React.useState(false)
  const [metadataError, setMetadataError] = React.useState("")
  const [searchNow, setSearchNow] = React.useState(true)
  const [roots, setRoots] = React.useState<APILibraryRoot[]>([])
  const [profiles, setProfiles] = React.useState<APIQualityProfile[]>([])
  const [clients, setClients] = React.useState<APIIntegration[]>([])
  const [indexers, setIndexers] = React.useState<APIIntegration[]>([])
  const [rootID, setRootID] = React.useState("default")
  const [profileID, setProfileID] = React.useState("default")
  const [clientID, setClientID] = React.useState("default")
  const [loading, setLoading] = React.useState(true)
  const [error, setError] = React.useState("")
  const [submitting, setSubmitting] = React.useState(false)
  React.useEffect(() => {
    Promise.all([loadLibraryRoots(), loadQualityProfiles(), loadIntegrations()]).then(([nextRoots, nextProfiles, integrations]) => { setRoots(nextRoots); setProfiles(nextProfiles); setClients(integrations.filter((item) => item.kind === "altmount" && item.enabled)); setIndexers(integrations.filter((item) => item.kind === "newznab" && item.enabled)) }).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : "Could not load configuration")).finally(() => setLoading(false))
  }, [])
  function changeKind(value: "movie" | "series") { setKind(value); setProvider(value === "movie" ? "tmdb" : "tvdb"); setRootID("default"); setProfileID("default"); setMetadataDetail(null); setSeasonSpec(value === "series" ? "1:10" : "") }
  const compatibleRoots = roots.filter((root) => root.media_kind === kind && root.enabled)
  const compatibleProfiles = profiles.filter((profile) => profile.media_kind === kind && profile.enabled)
  const configured = compatibleRoots.length > 0 && compatibleProfiles.length > 0 && clients.length > 0 && (!searchNow || indexers.length > 0)
  async function findMetadata(event: React.FormEvent) {
    event.preventDefault(); setMetadataBusy(true); setMetadataError(""); setMetadataDetail(null)
    try { const page = await searchMetadata(metadataQuery.trim()); setMetadataResults(page.results); if (page.results.length === 0) setMetadataError("Seerr returned no movie or TV matches.") }
    catch (reason) { setMetadataResults([]); setMetadataError(reason instanceof Error ? reason.message : "Could not search Seerr metadata") }
    finally { setMetadataBusy(false) }
  }
  async function selectMetadata(result: APIDiscoverySearchResult) {
    setMetadataBusy(true); setMetadataError("")
    try {
      const detail = await loadDiscoveryMetadata(result.mediaType, result.id)
      setMetadataDetail(detail); setKind(detail.kind); setTitle(detail.title); setYear(detail.year ? String(detail.year) : ""); setProvider("tmdb"); setProviderID(String(detail.tmdb_id)); setRootID("default"); setProfileID("default"); setSeasonSpec(detail.kind === "series" ? "1" : "")
    } catch (reason) { setMetadataError(reason instanceof Error ? reason.message : "Could not verify metadata") }
    finally { setMetadataBusy(false) }
  }
  function clearVerifiedSelection() { setMetadataDetail(null) }
  async function submit(event: React.FormEvent) {
    event.preventDefault(); setError(""); setSubmitting(true)
    try {
      const parsedYear = Number(year)
      if (!title.trim() || parsedYear < 1800 || parsedYear > 9999 || !providerID.trim()) throw new Error("Title, valid year, and provider ID are required")
      let seasons
      if (kind === "series" && metadataDetail) {
        seasons = []
        for (const number of parseSeasonNumbers(seasonSpec)) {
          const snapshot = await loadDiscoveryMetadata("tv", metadataDetail.tmdb_id, number)
          if (!snapshot.season || snapshot.season.episodes.length === 0) throw new Error(`Seerr returned no exact episode snapshot for season ${number}`)
          seasons.push({ number, monitored: true, search_immediately: searchNow, prefer_season_pack: true, episodes: snapshot.season.episodes.map((episode) => ({ number: episode.number, provider_episode_id: episode.provider_id, title: episode.title, air_date: episode.air_date })) })
        }
      } else if (kind === "series") seasons = parseSeasonSpec(seasonSpec).map((season) => ({ ...season, search_immediately: searchNow }))
      const identities = metadataDetail ? metadataDetail.identities.map((identity) => ({ ...identity, state: "verified" as const, source: identity.source || "seerr_metadata" })) : [{ namespace: provider, value: providerID.trim(), state: "verified" as const, source: "manual" as const }]
      await createMedia({ kind, title: title.trim(), year: parsedYear, identities, ...(metadataDetail?.metadata ? { metadata: metadataDetail.metadata } : {}), search_immediately: searchNow, ...(rootID !== "default" ? { root_id: rootID } : {}), ...(profileID !== "default" ? { quality_profile_id: profileID } : {}), ...(clientID !== "default" ? { download_client_id: clientID } : {}), ...(seasons ? { seasons } : {}) })
      toast.success(kind === "movie" ? "Movie workflow created" : "Season-pack workflows created")
      router.push("/activity/queue")
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not add media") } finally { setSubmitting(false) }
  }
  return <div className="space-y-4">
    <Panel title="Search verified metadata through Seerr" subtitle="Search is owner-triggered. Selecting a result performs a detail lookup before its identities can be submitted.">
      <form className="flex flex-col gap-2 sm:flex-row" onSubmit={findMetadata}><Input className="mm-input flex-1" value={metadataQuery} onChange={(event) => setMetadataQuery(event.target.value)} minLength={2} maxLength={200} placeholder="Search movies and TV series…" required /><Button type="submit" disabled={metadataBusy}>{metadataBusy ? <LoaderCircle className="animate-spin" /> : <Search />} Search metadata</Button></form>
      {metadataError ? <div className="mt-3"><InlineNotice tone="warn" title="Metadata search unavailable">{metadataError}</InlineNotice></div> : null}
      {metadataResults.length > 0 ? <div className="mt-4 grid gap-2 md:grid-cols-2 xl:grid-cols-3">{metadataResults.slice(0, 24).map((result) => { const resultTitle = result.mediaType === "movie" ? result.title : result.name; const resultDate = result.mediaType === "movie" ? result.releaseDate : result.firstAirDate; return <button type="button" key={`${result.mediaType}:${result.id}`} disabled={metadataBusy} onClick={() => void selectMetadata(result)} className="rounded-xl border border-line bg-surface-2 p-3 text-left transition hover:border-line-strong disabled:opacity-50"><div className="text-[12px] font-semibold text-fg">{resultTitle || "Untitled result"}</div><div className="mt-1 text-[12px] text-fg-subtle">{result.mediaType === "movie" ? "Movie" : "TV series"} · {resultDate?.slice(0, 4) || "Year unavailable"} · TMDB {result.id}</div>{result.overview ? <div className="mt-2 line-clamp-2 text-[12px] leading-4 text-fg-subtle">{result.overview}</div> : null}</button> })}</div> : null}
    </Panel>
    <div className="grid gap-4 xl:grid-cols-[1fr_340px]">
    <Panel title="Add directly to Media Manager" subtitle={metadataDetail ? "Verified Seerr metadata is selected. Series season numbers resolve to immutable episode snapshots before creation." : "Manual identity fallback. Series snapshots become one preferred pack job per season."}>
      {error ? <div className="mb-4"><ErrorNotice error={error} /></div> : null}
      {metadataDetail ? <div className="mb-4 grid grid-cols-[54px_1fr] gap-3 rounded-lg border border-line-strong bg-surface-3 p-3"><MetadataArtwork src={metadataDetail.metadata?.poster_url} fallback={metadataDetail.kind === "movie" ? "/media/movie-placeholder.svg" : "/media/series-placeholder.svg"} alt="" width={108} height={162} className="aspect-[2/3] w-[54px] rounded border border-line-strong object-cover" /><div className="min-w-0"><div className="text-[12px] font-semibold text-fg">Verified metadata selected</div><div className="mt-1 text-[12px] text-fg-muted">{metadataDetail.identities.map((identity) => `${identity.namespace.toUpperCase()} ${identity.value}`).join(" · ")}</div>{metadataDetail.metadata?.overview ? <p className="mt-2 line-clamp-3 text-[12px] leading-4 text-fg-subtle">{metadataDetail.metadata.overview}</p> : null}<div className="mt-2 text-[12px] text-brand-text">{Object.entries(metadataDetail.provider_states).filter(([, state]) => state === "direct" || state === "via_simkl").map(([provider]) => provider.toUpperCase()).join(" · ") || "TMDB through Seerr"}</div></div></div> : null}
      {!loading && !configured ? <div className="mb-4"><InlineNotice tone="warn" title="Finish setup first">You need an enabled Plex-backed {kind} root, {kind} quality profile, AltMount client{searchNow ? ", and at least one Newznab indexer" : ""}. <Link className="underline" href="/settings/connections">Open setup</Link>.</InlineNotice></div> : null}
      <form className="grid gap-4" onSubmit={submit}>
        <div className="grid gap-3 sm:grid-cols-2"><Field label="Media type"><Select value={kind} onValueChange={(value) => changeKind(value as "movie" | "series")}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="movie">Movie</SelectItem><SelectItem value="series">TV series</SelectItem></SelectContent></Select></Field><Field label="Release year"><Input className="mm-input" inputMode="numeric" value={year} onChange={(event) => setYear(event.target.value)} placeholder="2026" required /></Field></div>
        <Field label="Title"><Input className="mm-input" value={title} onChange={(event) => setTitle(event.target.value)} placeholder={kind === "movie" ? "Movie title" : "Series title"} required /></Field>
        <div className="grid gap-3 sm:grid-cols-2"><Field label="Verified provider"><Select value={provider} onValueChange={(value) => { clearVerifiedSelection(); setProvider(value) }}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent>{kind === "movie" ? <><SelectItem value="tmdb">TMDB</SelectItem><SelectItem value="imdb">IMDb</SelectItem></> : <><SelectItem value="tvdb">TVDB</SelectItem><SelectItem value="tmdb">TMDB</SelectItem></>}</SelectContent></Select></Field><Field label={`${provider.toUpperCase()} ID`}><Input className="mm-input" value={providerID} onChange={(event) => { clearVerifiedSelection(); setProviderID(event.target.value) }} placeholder={provider === "imdb" ? "tt1234567" : "12345"} required /></Field></div>
        {kind === "series" ? <Field label={metadataDetail ? "Season numbers" : "Exact seasons and episode counts"} hint={metadataDetail ? "Comma-separated seasons. Seerr supplies each exact episode snapshot, for example 1, 2." : "Format season:count. Example: 1:10, 2:8 creates two immutable episode snapshots."}><Input className="mm-input" value={seasonSpec} onChange={(event) => setSeasonSpec(event.target.value)} required /></Field> : null}
        <div className="grid gap-3 sm:grid-cols-3"><ConfigSelect label="Library root" value={rootID} onChange={setRootID} items={compatibleRoots.map((item) => ({ id: item.id, label: `${item.name}${item.is_default ? " · default" : ""}` }))} /><ConfigSelect label="Quality profile" value={profileID} onChange={setProfileID} items={compatibleProfiles.map((item) => ({ id: item.id, label: `${item.name}${item.is_default ? " · default" : ""}` }))} /><ConfigSelect label="Download client" value={clientID} onChange={setClientID} items={clients.map((item) => ({ id: item.id, label: `${item.name}${item.is_default ? " · default" : ""}` }))} /></div>
        <div className="flex items-center justify-between rounded-xl border border-line bg-surface-2 p-3"><div><div className="text-[12px] font-semibold text-fg">Search immediately</div><div className="mt-1 text-[12px] text-fg-subtle">Turn off to leave a monitored workflow you can search later.</div></div><Switch checked={searchNow} onCheckedChange={setSearchNow} aria-label="Search immediately" /></div>
        <Button disabled={loading || !configured || submitting} type="submit">{submitting ? <LoaderCircle className="animate-spin" /> : <Plus />} {searchNow ? "Add and search" : "Add without search"}</Button>
      </form>
    </Panel>
    <Panel title="Identity and season safety" subtitle="No single provider ID is required for every media type." className="h-fit">
      <KeyValue label="Movies" value="TMDB or IMDb" /><KeyValue label="Series" value="TVDB or TMDB" /><KeyValue label="Season preference" value="Complete pack" /><KeyValue label="AltMount jobs" value="One per season" /><KeyValue label="Publication" value="Exact coverage + Plex" />
      <InlineNotice tone="info" title="Best input">Seerr discovery supplies canonical metadata and exact episode snapshots. Manual input remains available when Seerr is not configured or you already know the verified identity and season shape.</InlineNotice>
    </Panel>
    </div>
  </div>
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) { return <label className="grid gap-1.5 text-[12.5px] font-medium text-fg-secondary">{label}{children}{hint ? <span className="text-[12px] font-normal leading-[18px] text-fg-muted">{hint}</span> : null}</label> }

function ConfigSelect({ label, value, onChange, items }: { label: string; value: string; onChange: (value: string) => void; items: Array<{ id: string; label: string }> }) { return <Field label={label}><Select value={value} onValueChange={onChange}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="default">Use default</SelectItem>{items.map((item) => <SelectItem key={item.id} value={item.id}>{item.label}</SelectItem>)}</SelectContent></Select></Field> }

export function LiveMediaDetailScreen({ mediaId }: { mediaId: string }) {
  const router = useRouter()
  const [detail, setDetail] = React.useState<APIMediaDetail | null>(null)
  const [error, setError] = React.useState("")
  // Empty until the owner types a season: a prefilled value made one click
  // add a blank season 1 that searched immediately.
  const [seasonSpec, setSeasonSpec] = React.useState("")
  const [seasonAddConfirmed, setSeasonAddConfirmed] = React.useState(false)
  const [replacementConfirmation, setReplacementConfirmation] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const refresh = React.useCallback(async () => { try { setDetail(await loadMediaDetail(mediaId)); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load media") } }, [mediaId])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  useAutoRefresh(() => void refresh(), LIVE_REFRESH_MS, !busy)
  const media = detail?.media ?? null
  const seasonRollups = React.useMemo(() => new Map(rollupSeriesSeasons(detail?.workflows ?? [], detail?.files ?? []).map((season) => [season.number, season])), [detail?.workflows, detail?.files])
  async function addWorkflow() {
    if (!media) return
    setBusy(true)
    try {
      if (media.kind === "movie") await createExistingWorkflow(media.id, { search_immediately: true, adopted_replacement_confirmation: replacementConfirmation })
      else {
        const seasons = parseSeasonSpec(seasonSpec)
        if (seasons.length !== 1) throw new Error("Add one season at a time from this screen")
        await createExistingWorkflow(media.id, { search_immediately: true, season: seasons[0], adopted_replacement_confirmation: replacementConfirmation })
      }
      toast.success(media.kind === "movie" ? "Upgrade workflow created" : "Season-pack workflow created"); setSeasonSpec(""); setSeasonAddConfirmed(false); await refresh()
    } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not create workflow") } finally { setBusy(false) }
  }
  async function refreshMetadata() {
    setBusy(true)
    try { const result = await refreshMediaMetadata(mediaId); const direct = Object.entries(result.provider_states).filter(([, state]) => state === "direct" || state === "via_simkl").map(([provider]) => provider.toUpperCase()); toast.success(direct.length ? `${direct.join(", ")} metadata refreshed` : "TMDB metadata refreshed through Seerr"); await refresh() }
    catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not refresh metadata") }
    finally { setBusy(false) }
  }
  async function remove() {
    setBusy(true)
    try { await recycleMedia(mediaId); toast.success("Media moved to the 30-day recycle workflow"); router.push("/media") } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not recycle media") } finally { setBusy(false) }
  }
  if (error) return <ErrorNotice error={error} />
  if (!media) return <Panel><div className="flex items-center gap-2 text-[12px] text-fg-subtle"><LoaderCircle className="size-4 animate-spin" /> Loading media…</div></Panel>
  const hasAdoptedProtected = detail?.files.some((file) => file.origin === "adoption" && ["current", "recycle_planned", "retained", "restore_confirming", "restore_attention"].includes(file.state)) ?? false
  const selectedSeason = media.kind === "series" ? Number.parseInt(seasonSpec.split(":", 1)[0] ?? "", 10) : undefined
  const selectedVariant = media.kind === "movie" ? "default" : selectedSeason !== undefined && Number.isInteger(selectedSeason) ? formatSeasonVariant(selectedSeason) : ""
  const hasAdoptedForVariant = detail?.files.some((file) => file.origin === "adoption" && file.state === "current" && file.variant === selectedVariant) ?? false
  const seasonSpecError = media.kind === "series" && seasonSpec.trim() ? (() => { try { return parseSeasonSpec(seasonSpec).length === 1 ? "" : "Add one season at a time from this screen" } catch (reason) { return reason instanceof Error ? reason.message : "Use season:episode-count, for example 5:8" } })() : ""
  const seasonCheck = media.kind === "series" && seasonSpec.trim() && !seasonSpecError && selectedSeason !== undefined ? seasonAddCheck(selectedSeason, detail?.workflows ?? [], seasonRollups.get(selectedSeason), detail?.seasons.find((season) => season.number === selectedSeason)?.expected_episodes) : undefined
  const seasonAddAllowed = media.kind !== "series" || (seasonCheck !== undefined && (seasonCheck.status === "ready" || (seasonCheck.status === "confirm" && seasonAddConfirmed)))
  const replacementConfirmed = !hasAdoptedForVariant || replacementConfirmation === "REPLACE ADOPTED FILE AFTER PLEX VERIFICATION"
  const currentFile = detail?.files.find((file) => file.state === "current" && file.plex_confirmed) ?? detail?.files.find((file) => file.state === "current")
  const attentionWorkflow = detail?.workflows.find((workflow) => workflow.owner_action_required || ["needs_attention", "blocked", "quarantined", "transport_unknown"].includes(workflow.state))
  const availabilityState = currentFile ? "plex_confirmed" : attentionWorkflow ? attentionWorkflow.state : media.kind === "series" ? (detail?.seasons.some((season) => season.monitored) ? "monitored" : "missing") : "missing"
  const hue = titleHue(media.title)
  const kindLabel = media.kind === "movie" ? "Movie" : "TV series"
  const monitoredSeasons = (detail?.seasons ?? []).filter((season) => season.monitored).length
  // A file's profile is what it was verified against; before any file exists,
  // the profile the title's workflows acquire under is the truthful answer.
  const profile = currentFile?.quality_profile ?? detail?.files.find((file) => file.quality_profile)?.quality_profile ?? detail?.workflows.find((workflow) => !workflow.terminal_at && workflow.quality_profile)?.quality_profile
  const totalBytes = (detail?.files ?? []).filter((file) => file.state === "current").reduce((sum, file) => sum + (file.source_size_bytes ?? 0), 0)
  const heldFiles = (detail?.files ?? []).filter((file) => file.state !== "current" && !file.removed_at && !file.displaced_at).length
  const seasonCoverage = (detail?.seasons ?? []).map((season) => {
    const rollup = seasonRollups.get(season.number)
    const total = season.expected_episodes
    const packDone = rollup?.packWorkflow?.state === "plex_confirmed" || (rollup?.packFiles ?? []).some((file) => file.state === "current" && file.plex_confirmed)
    const packActive = rollup?.packWorkflow ? ["searching", "candidate_selected", "submitting", "queued", "downloading", "streaming_ready", "import_complete", "verifying", "publishing", "plex_scanning"].includes(rollup.packWorkflow.state) : false
    const acquired = packDone ? total : Math.min(total, rollup?.acquiredEpisodes.length ?? 0)
    const attention = packDone ? 0 : Math.min(Math.max(0, total - acquired), (rollup?.attentionEpisodes.length ?? 0) + (rollup?.packWorkflow && ["needs_attention", "blocked", "quarantined"].includes(rollup.packWorkflow.state) ? Math.max(0, total - acquired) : 0))
    const acquiring = packDone ? 0 : packActive ? Math.max(0, total - acquired - attention) : Math.min(Math.max(0, total - acquired - attention), rollup?.acquiringEpisodes.length ?? 0)
    // Only dated future episodes are unaired; an episode without an air date is
    // counted separately so a finished season never looks like it is airing.
    const unaired = Math.min(total, season.future_episodes ?? 0)
    const undated = Math.min(total - unaired, season.undated_episodes ?? 0)
    return { season, rollup, total, acquired, acquiring, attention, released: total - unaired, undated }
  })
  const totals = seasonCoverage.reduce((sum, entry) => ({ total: sum.total + entry.total, acquired: sum.acquired + entry.acquired, acquiring: sum.acquiring + entry.acquiring, attention: sum.attention + entry.attention, unaired: sum.unaired + Math.max(0, entry.total - entry.released) }), { total: 0, acquired: 0, acquiring: 0, attention: 0, unaired: 0 })
  const nextSearch = seasonCoverage.map((entry) => entry.rollup?.nextSearchAt).filter((value): value is string => Boolean(value)).sort()[0]
  const latestWorkflow = [...(detail?.workflows ?? [])].sort((left, right) => new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime())[0]
  const pipeline = latestWorkflow ? workflowStep(latestWorkflow.state) : { current: -1, complete: false }
  return <div className="space-y-4">
    <nav aria-label="Breadcrumb" className="flex items-center gap-2 text-[12.5px] text-fg-subtle"><Link href={media.kind === "movie" ? "/media" : "/media/series"} className="text-brand-text hover:underline">{media.kind === "movie" ? "Movies" : "TV Shows"}</Link><span aria-hidden>/</span><span className="truncate text-fg">{media.title}</span></nav>
    <section className="relative overflow-hidden rounded-[14px] border border-line" aria-labelledby="media-detail-title">
      <div className="pointer-events-none absolute inset-0" aria-hidden style={{ background: `radial-gradient(120% 160% at 85% -10%, hsl(${hue} 55% 45% / .45), transparent 55%), radial-gradient(110% 130% at 0% 110%, hsl(${(hue + 150) % 360} 45% 35% / .35), transparent 55%), linear-gradient(120deg, var(--surface-2), var(--canvas))` }}>
        {media.metadata?.backdrop_url ? <MetadataArtwork src={media.metadata.backdrop_url} fallback={media.kind === "movie" ? "/media/movie-placeholder.svg" : "/media/series-placeholder.svg"} alt="" width={1600} height={900} className="h-full w-full object-cover opacity-25" /> : null}
        <div className="absolute inset-0 bg-gradient-to-r from-canvas/90 via-canvas/55 to-transparent" />
      </div>
      <div className="relative flex flex-col gap-6 p-5 sm:flex-row sm:p-7">
        <div className="relative flex aspect-[2/3] w-32 shrink-0 items-end overflow-hidden rounded-[10px] shadow-[0_16px_40px_rgba(0,0,0,.55)] sm:w-[172px]" style={{ background: `linear-gradient(160deg, hsl(${hue} 50% 36%), hsl(${hue} 55% 12%))` }}>
          {media.metadata?.poster_url ? <MetadataArtwork src={media.metadata.poster_url} fallback={media.kind === "movie" ? "/media/movie-placeholder.svg" : "/media/series-placeholder.svg"} alt={`${media.title} poster`} width={344} height={516} className="absolute inset-0 h-full w-full object-cover" /> : <b className="relative p-3 text-[15px] font-bold uppercase leading-tight text-white [text-shadow:0_1px_6px_rgba(0,0,0,.8)]">{media.title}</b>}
        </div>
        <div className="min-w-0 flex-1 pt-1">
          <div className="mb-2 flex flex-wrap items-center gap-2 text-[11px] font-bold uppercase tracking-[0.14em] text-brand-text"><span>{kindLabel}{media.kind === "series" ? ` · ${monitoredSeasons ? "Monitored" : "Not monitored"}` : ""}</span><StateBadge state={availabilityState} className="normal-case tracking-normal" /></div>
          <h1 id="media-detail-title" className="text-[30px] font-extrabold leading-[1.1] tracking-[-0.03em] text-fg">{media.title}</h1>
          <div className="mb-3 mt-2 text-[13px] text-fg-muted">{[media.year ? String(media.year) : "Year unavailable", media.metadata?.runtime_minutes ? `${Math.floor(media.metadata.runtime_minutes / 60)}h ${media.metadata.runtime_minutes % 60}m` : "", media.metadata?.networks[0]?.name ?? "", (media.metadata?.genres ?? []).slice(0, 3).join(", "), profile ? `Profile ${profile}` : ""].filter(Boolean).join(" · ")}</div>
          {media.metadata?.overview ? <p className="mb-3 line-clamp-3 max-w-3xl text-[13px] leading-[21px] text-fg-secondary">{media.metadata.overview}</p> : null}
          <div className="mb-4 flex flex-wrap gap-2">
            {media.kind === "series" && totals.total ? <StatusChip tone={totals.acquired === totals.total ? "good" : "neutral"}>{totals.acquired} of {totals.total} episodes</StatusChip> : null}
            {latestWorkflow ? <StatusChip tone={latestWorkflow.owner_action_required ? "warn" : pipeline.current >= 0 && !pipeline.complete ? "info" : pipeline.complete ? "good" : "neutral"}>{latestWorkflow.variant} · {describeStatus(latestWorkflow.state).label}</StatusChip> : null}
            {attentionWorkflow ? <StatusChip tone="warn">{retryPlan(attentionWorkflow)}</StatusChip> : null}
            {(media.identities ?? []).map((identity) => <StatusChip key={`${identity.namespace}-${identity.value}`} dot={false}><span className="font-mono">{identity.namespace.toUpperCase()} {identity.value}</span></StatusChip>)}
          </div>
          <div className="flex flex-wrap items-center gap-2">{media.kind === "series" ? <Input className="mm-input h-8 w-28" value={seasonSpec} placeholder="5:8" onChange={(event) => { setSeasonSpec(event.target.value); setSeasonAddConfirmed(false); setReplacementConfirmation("") }} aria-label="Season and episode count" aria-describedby={seasonSpecError || seasonCheck?.status === "blocked" || seasonCheck?.status === "confirm" ? "season-add-note" : undefined} /> : null}<Button size="sm" className="h-8" disabled={busy || !replacementConfirmed || !seasonAddAllowed} onClick={() => void addWorkflow()}><Plus /> {media.kind === "movie" ? "Search upgrade" : "Add season"}</Button><Button size="sm" variant="outline" className="h-8" disabled={busy || !media.identities.some((identity) => identity.namespace === "tmdb" && identity.state === "verified")} onClick={() => void refreshMetadata()}><RefreshCw /> Refresh metadata</Button><AlertDialog><AlertDialogTrigger asChild><Button size="sm" variant="ghost" className="h-8" disabled={busy || hasAdoptedProtected}><Trash2 /> Recycle</Button></AlertDialogTrigger><AlertDialogContent className="border-line-strong bg-surface-2 text-fg"><AlertDialogHeader><AlertDialogTitle>Recycle {media.title}?</AlertDialogTitle><AlertDialogDescription>Managed links move into 30-day retention. AltMount source files stay untouched. Active workflows and adopted real-file retention must be resolved first.</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>Keep media</AlertDialogCancel><AlertDialogAction onClick={() => void remove()}>Recycle links</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog></div>
          {media.kind === "series" && (seasonSpecError || seasonCheck?.status === "blocked" || seasonCheck?.status === "confirm") ? <div id="season-add-note" role="status" className="mt-2 max-w-xl rounded-lg border border-warn-line bg-warn-soft px-3 py-2 text-[12px] leading-[18px] text-warn">
            {seasonSpecError || (seasonCheck && seasonCheck.status !== "ready" ? seasonCheck.message : "")}
            {seasonCheck?.status === "confirm" ? <label className="mt-1.5 flex items-center gap-2 font-semibold text-fg"><input type="checkbox" className="size-3.5 accent-[var(--brand)]" checked={seasonAddConfirmed} onChange={(event) => setSeasonAddConfirmed(event.target.checked)} /> Search again anyway</label> : null}
          </div> : null}
        </div>
      </div>
    </section>
    {attentionWorkflow ? <Panel flush className="border-warn-line"><AttentionCard item={{ title: media.title, year: media.year, variant: attentionWorkflow.variant, detail: attentionWorkflow.attention_detail?.trim() || `This ${media.kind === "movie" ? "movie" : "season"} is waiting for a decision only you can make.`, mediaKind: media.kind, mediaId: media.id }} workflow={attentionWorkflow} poster={media.metadata?.poster_url} /></Panel> : null}
    {hasAdoptedForVariant ? <Panel title="Confirm adopted-file replacement" subtitle={`The selected ${selectedVariant} currently contains a reviewed real file.`}><InlineNotice tone="warn" title="New media is verified first">The existing file is moved into 30-day retention only after the replacement is published and Plex confirms it. Physical deletion is never automatic and requires a second owner confirmation after retention.</InlineNotice><Field label="Exact confirmation phrase" hint="Type REPLACE ADOPTED FILE AFTER PLEX VERIFICATION"><Input className="mm-input mt-3" value={replacementConfirmation} onChange={(event) => setReplacementConfirmation(event.target.value)} autoComplete="off" /></Field></Panel> : hasAdoptedProtected ? <InlineNotice tone="info" title="Adopted real-file lifecycle remains protected">The selected scope has no current adopted file. Adopted files in another scope, displacement, or retained-file state still block whole-item recycle.</InlineNotice> : null}
    <div className="grid items-start gap-4 xl:grid-cols-[1.9fr_1fr]">
      <div className="min-w-0 space-y-4">
        {media.kind === "series" ? <Panel title="Seasons" subtitle={`${detail?.seasons.length ?? 0} season${detail?.seasons.length === 1 ? "" : "s"} · ${totals.total} episodes expected`}>
          {seasonCoverage.length === 0 ? <div className="py-4 text-center text-[12.5px] text-fg-subtle">No seasons are monitored.</div> : <div className="space-y-2.5">{seasonCoverage.map(({ season, rollup, total, acquired, acquiring, attention, released, undated }) => <details key={season.number} className="overflow-hidden rounded-[11px] border border-line bg-surface" open={attention > 0 || acquiring > 0 || released < total}>
            <summary className="flex cursor-pointer list-none items-center gap-3.5 px-4 py-3.5 transition hover:bg-surface-2 [&::-webkit-details-marker]:hidden">
              <span className="w-11 shrink-0 text-[15px] font-extrabold text-fg">S{season.number}</span>
              <div className="min-w-0 flex-1"><b className="text-[13px] font-bold text-fg">Season {season.number} · {total || "?"} episodes{released < total ? " · currently airing" : ""}</b><small className="mt-0.5 block text-[11.5px] text-fg-subtle">{[acquired ? `${acquired} acquired` : "", acquiring ? `${acquiring} acquiring` : "", attention ? `${attention} need attention` : "", released < total ? `${total - released} unaired` : "", undated ? `${undated} without air date` : "", season.prefer_season_pack ? "season pack preferred" : "episodes individually"].filter(Boolean).join(" · ")}</small></div>
              <CoverageBar total={total} acquired={acquired} acquiring={acquiring} attention={attention} released={released} className="hidden w-[200px] shrink-0 md:flex" />
              {attention ? <StatusChip tone="warn">Needs attention</StatusChip> : acquired === total && total > 0 ? <StatusChip tone="good">Complete</StatusChip> : acquiring ? <StatusChip tone="info">In progress</StatusChip> : <StateBadge state={season.monitored ? (season.workflow_state ?? season.state) : "unmonitored"} />}
            </summary>
            <div className="border-t border-line px-4 py-3">
              <KeyValue label="Monitoring" value={season.monitored ? "Monitored" : "Not monitored"} />
              <KeyValue label="Strategy" value={season.prefer_season_pack ? "Season pack when complete; single episodes while airing" : "Individual episodes"} />
              <KeyValue label="Released / future" value={typeof season.released_episodes === "number" || typeof season.future_episodes === "number" ? `${season.released_episodes ?? 0} released · ${season.future_episodes ?? 0} future${season.undated_episodes ? ` · ${season.undated_episodes} air date unknown` : ""}` : "Not snapshotted"} />
              <KeyValue label="Search" value={season.last_search_at || season.next_search_at ? `${season.last_search_at ? `Last ${new Date(season.last_search_at).toLocaleString()}` : "Not searched"}${season.next_search_at ? ` · next ${new Date(season.next_search_at).toLocaleString()}` : ""}` : "Not searched"} />
              {rollup && rollup.attentionWorkflows[0]?.attention_detail ? <div className="mt-2 rounded-lg border border-warn-line bg-warn-soft px-3 py-2 text-[12px] leading-[18px] text-warn">{rollup.attentionWorkflows[0].attention_detail}</div> : season.waiting_reason ? <div className="mt-2 rounded-lg border border-line bg-surface-2 px-3 py-2 text-[12px] leading-[18px] text-fg-subtle">{season.waiting_reason}</div> : null}
            </div>
            {season.episodes?.length ? <ol className="border-t border-line py-1" aria-label={`Season ${season.number} episodes`}>{season.episodes.map((episode) => { const { status, workflow } = episodeStatus(episode, rollup); return <li key={episode.number} className={cn("grid grid-cols-[48px_minmax(0,1fr)_auto] items-center gap-3 border-t border-line px-4 py-2 text-[12.5px] first:border-t-0 sm:grid-cols-[52px_minmax(0,1.2fr)_110px_130px_minmax(0,1.4fr)]", status === "attention" && "bg-warn-soft/60")}>
              <span className="font-extrabold text-fg-subtle">E{String(episode.number).padStart(2, "0")}</span>
              <span className="truncate font-semibold text-fg">{episode.title || `Episode ${episode.number}`}</span>
              <span className="hidden text-[11.5px] text-fg-subtle sm:block">{episode.air_date ? new Date(`${episode.air_date.slice(0, 10)}T00:00:00`).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" }) : "Air date unknown"}</span>
              <span>{status === "acquired" ? <StatusChip tone="good">Acquired</StatusChip> : status === "attention" ? <StatusChip tone="warn">Needs attention</StatusChip> : status === "acquiring" ? <StatusChip tone="info">{workflow ? describeStatus(workflow.state).label : "Acquiring"}</StatusChip> : status === "unaired" ? <StatusChip>Unaired</StatusChip> : <StatusChip>Not yet acquired</StatusChip>}</span>
              <span className={cn("hidden truncate text-[11.5px] sm:block", status === "attention" ? "text-warn" : status === "acquired" ? "text-good" : "text-fg-subtle")}>{status === "attention" ? workflow?.attention_detail || "Waiting for your decision" : status === "acquiring" && workflow ? (workflow.download_percent !== undefined ? `AltMount ${workflow.download_percent}%` : describeStatus(workflow.state).label) : status === "unaired" ? "Acquired automatically after it airs" : status === "acquired" ? "Verified and confirmed in Plex" : season.prefer_season_pack ? "Covered by the season search" : "Searched within indexer pacing"}</span>
            </li> })}</ol> : null}
          </details>)}</div>}
        </Panel> : <Panel title="Acquisition pipeline" subtitle={latestWorkflow ? `${describeStatus(latestWorkflow.state).label} · updated ${new Date(latestWorkflow.updated_at).toLocaleString()}` : "No acquisition has run for this movie yet"}>
          <ol className="space-y-1.5">{workflowSteps.map((step, index) => { const done = pipeline.complete || index < pipeline.current; const current = !pipeline.complete && index === pipeline.current; return <li key={step} className="flex items-center gap-2.5 text-[12px] text-fg-subtle"><b className={cn("w-16 shrink-0 text-right text-[11px] font-semibold", done ? "text-good" : current ? "text-brand-text" : "text-fg-subtle")}>{step}</b><span className={cn("h-1 flex-1 rounded-full", done ? "bg-good-solid/60" : current ? "bg-brand" : "bg-surface-3")} /><span className={cn("w-24 shrink-0 text-[11px]", current && "text-fg")}>{done ? "Done" : current ? "In progress" : "—"}</span></li> })}</ol>
          <div className="mt-3 rounded-lg border border-line bg-surface px-3 py-2 text-[11.5px] leading-[17px] text-fg-subtle">{pipeline.complete ? "Plex confirmed the exact managed path and provider identity." : "Required properties count as met only after the real output is inspected and Plex confirms it."}</div>
        </Panel>}
        <WorkflowTable workflows={detail?.workflows ?? []} empty="No workflows for this media." />
        <Panel flush title="Managed files" subtitle="Plex-safe AltMount symlinks and reviewed adopted real files with persisted provenance."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Destination</TableHead><TableHead>Origin</TableHead><TableHead>Scope</TableHead><TableHead>Quality evidence</TableHead><TableHead>Plex</TableHead></TableRow></TableHeader><TableBody>{(detail?.files ?? []).map((file) => <TableRow key={file.id}><TableCell><Link href={`/files/${file.id}`} className="font-mono text-[11.5px] text-fg hover:text-info">{file.destination_relative}</Link></TableCell><TableCell>{file.origin === "adoption" ? "Adopted real file" : `AltMount generation ${file.generation}`}</TableCell><TableCell>{file.variant}</TableCell><TableCell><StateBadge state={file.quality_evidence_state} /></TableCell><TableCell>{file.plex_confirmed ? <StatusChip tone="good">Confirmed</StatusChip> : <StatusChip>Not confirmed</StatusChip>}</TableCell></TableRow>)}</TableBody></Table></div>{detail?.files.length === 0 ? <div className="p-6 text-center text-[12px] text-fg-subtle">No managed files exist for this item.</div> : null}</Panel>
      </div>
      <div className="min-w-0 space-y-4">
        {media.kind === "series" ? <Panel title="Coverage">
          <CoverageBar total={totals.total} acquired={totals.acquired} acquiring={totals.acquiring} attention={totals.attention} released={totals.total - totals.unaired} className="mb-2.5 h-2" />
          <CoverageLegend items={[{ kind: "a", label: `${totals.acquired} acquired` }, { kind: "b", label: `${totals.acquiring} acquiring` }, { kind: "c", label: `${totals.attention} attention` }, { kind: "u", label: `${totals.unaired} unaired` }]} />
          <div className="mt-3"><KeyValue label="Last publication" value={currentFile?.verified_at ? new Date(currentFile.verified_at).toLocaleDateString() : "None yet"} /><KeyValue label="Next search" value={nextSearch ? new Date(nextSearch).toLocaleString() : "Not scheduled"} /></div>
        </Panel> : <Panel title="Availability"><KeyValue label="State" value={describeStatus(availabilityState).label} /><KeyValue label="Current file" value={currentFile ? currentFile.variant : "None"} /><KeyValue label="Plex" value={currentFile?.plex_confirmed ? "Confirmed" : "Not confirmed"} /><KeyValue label="Verified" value={currentFile?.verified_at ? new Date(currentFile.verified_at).toLocaleString() : "Not yet"} /></Panel>}
        <Panel title="Profile" action={<Button asChild variant="ghost" size="sm"><Link href="/settings/profiles">Change →</Link></Button>}><KeyValue label="Quality profile" value={profile ?? "Not assigned"} /><KeyValue label="Quality evidence" value={currentFile ? describeStatus(currentFile.quality_evidence_state).label : "No current file"} /><KeyValue label="Origin" value={currentFile ? currentFile.origin === "adoption" ? "Adopted real file" : "AltMount symlink" : "—"} /></Panel>
        <Panel title="Files" action={<Button asChild variant="ghost" size="sm"><Link href="/files">Open in Files →</Link></Button>}><KeyValue label="Managed files" value={`${(detail?.files ?? []).filter((file) => file.state === "current").length} current`} /><KeyValue label="Held or retained" value={heldFiles ? `${heldFiles} file${heldFiles === 1 ? "" : "s"}` : "None"} valueClassName={heldFiles ? "text-warn" : undefined} /><KeyValue label="Total size" value={totalBytes ? `${(totalBytes / 1e9).toFixed(1)} GB` : "—"} /></Panel>
        {media.metadata ? <Panel title="Details"><KeyValue label="Original title" value={media.metadata.original_title || "Not supplied"} /><KeyValue label="Original language" value={media.metadata.original_language || "Not supplied"} /><KeyValue label="Status" value={media.metadata.status || "Not supplied"} /><KeyValue label="Release date" value={media.metadata.release_date || "Not supplied"} /><KeyValue label="Certification" value={media.metadata.certification || "Not supplied"} /><KeyValue label="Countries" value={media.metadata.countries.join(", ") || "Not supplied"} /><KeyValue label="Cast and crew" value={(media.metadata.credits ?? []).slice(0, 6).map((credit) => `${credit.name} (${credit.role})`).join(", ") || "Not supplied"} />{media.metadata.ratings.map((rating) => <KeyValue key={rating.source} label={`${rating.source.toUpperCase()} rating`} value={`${rating.value.toFixed(1)}${rating.votes ? ` · ${rating.votes.toLocaleString()} votes` : ""}`} />)}<div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 border-t border-line pt-2 text-[12px]">{media.metadata.sources.map((source) => <MetadataSourceLink key={`${source.provider}:${source.provider_id}:${source.role}`} source={source} />)}{media.metadata.sources.length === 0 ? <span className="text-fg-subtle">No provider sources</span> : null}</div></Panel> : <Panel title="Details"><p className="text-[12.5px] text-fg-muted">No descriptive metadata is stored for this item yet. Refresh metadata to enrich it from the configured providers.</p></Panel>}
      </div>
    </div>
  </div>
}

export function LiveRecycleScreen() {
  const [entries, setEntries] = React.useState<APIRecycleEntry[]>([])
  const [error, setError] = React.useState("")
  const [busy, setBusy] = React.useState("")
  const [purgeConfirmations, setPurgeConfirmations] = React.useState<Record<string, string>>({})
  const [restoreConfirmations, setRestoreConfirmations] = React.useState<Record<string, string>>({})
  const refresh = React.useCallback(async () => { try { setEntries(await loadRecycle()); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load recycle entries") } }, [])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  const groups = React.useMemo(() => {
    const grouped = new Map<string, APIRecycleEntry[]>()
    for (const entry of entries) grouped.set(entry.cycle_id, [...(grouped.get(entry.cycle_id) ?? []), entry])
    return Array.from(grouped.entries())
  }, [entries])
  async function restore(entry: APIRecycleEntry) { setBusy(entry.cycle_id); try { await restoreRecycle(entry.id); toast.success("Complete link restore queued"); await refresh() } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not restore media") } finally { setBusy("") } }
  async function restoreAdopted(entry: APIRecycleEntry) { setBusy(entry.cycle_id); try { await restoreAdoptedFiles(entry.id, restoreConfirmations[entry.cycle_id] ?? ""); toast.success("Adopted-file restore and Plex verification queued"); await refresh() } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not restore adopted files") } finally { setBusy("") } }
  async function purgeRealFile(entry: APIRecycleEntry) { setBusy(entry.id); try { await confirmRealFilePurge(entry.id, purgeConfirmations[entry.id] ?? ""); toast.success("Owner-confirmed real-file purge queued"); await refresh() } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not confirm real-file purge") } finally { setBusy("") } }
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{groups.length === 0 ? <EmptyPanel title="Recycle bin is empty" copy="Managed links and retained adopted upgrade files will appear here." /> : groups.map(([cycleId, mediaEntries]) => { const restorable = mediaEntries.find((entry) => entry.entry_kind === "link" && entry.purpose === "media_delete" && entry.state === "retained"); const adoptedRestorable = mediaEntries.find((entry) => entry.entry_kind === "real_file" && entry.purpose === "upgrade_displacement" && entry.state === "retained"); const hasRealFile = mediaEntries.some((entry) => entry.entry_kind === "real_file"); const purgeAt = mediaEntries.reduce((latest, entry) => entry.purge_after > latest ? entry.purge_after : latest, ""); const restorePhrase = restoreConfirmations[cycleId] ?? ""; return <Panel key={cycleId} title={mediaEntries[0].media_title} subtitle={hasRealFile ? "Adopted upgrade displacement · retained real file" : `${mediaEntries.length} managed link${mediaEntries.length === 1 ? "" : "s"} · AltMount source retained`} action={restorable ? <Button size="sm" disabled={busy === cycleId} onClick={() => void restore(restorable)}><ArchiveRestore /> Restore all links</Button> : undefined}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Original path</TableHead><TableHead>Kind</TableHead><TableHead>State</TableHead><TableHead>Retention</TableHead><TableHead>Owner action</TableHead></TableRow></TableHeader><TableBody>{mediaEntries.map((entry) => { const purgeDue = entry.entry_kind === "real_file" && entry.state === "retained" && Date.now() >= new Date(entry.purge_after).getTime(); const phrase = purgeConfirmations[entry.id] ?? ""; return <TableRow key={entry.id}><TableCell className="max-w-xl break-all font-mono text-[12px]">{entry.original_relative}</TableCell><TableCell>{entry.entry_kind === "real_file" ? "Adopted real file" : "Managed link"}</TableCell><TableCell><StateBadge state={entry.state} /></TableCell><TableCell>{new Date(entry.purge_after).toLocaleDateString()}</TableCell><TableCell>{purgeDue ? <div className="flex min-w-72 flex-col gap-2"><Input className="mm-input" value={phrase} onChange={(event) => setPurgeConfirmations((current) => ({ ...current, [entry.id]: event.target.value }))} placeholder="PURGE RETAINED ADOPTED FILE" autoComplete="off" /><Button size="sm" variant="destructive" disabled={busy === entry.id || phrase !== "PURGE RETAINED ADOPTED FILE"} onClick={() => void purgeRealFile(entry)}><Trash2 /> Confirm physical purge</Button></div> : entry.entry_kind === "real_file" && entry.state === "retained" ? "Purge available after retention" : entry.state === "failed" ? "Owner recovery required" : "—"}</TableCell></TableRow> })}</TableBody></Table></div>{adoptedRestorable ? <div className="mt-4 grid gap-2 rounded-lg border border-line-strong bg-surface-3 p-3"><div className="text-[12px] font-semibold text-fg">Restore the complete adopted-file cycle</div><div className="text-[12px] leading-4 text-fg-muted">The retained files move back first. The replacement links remain current until Plex confirms every restored path; only then are those links displaced.</div><Input className="mm-input" value={restorePhrase} onChange={(event) => setRestoreConfirmations((current) => ({ ...current, [cycleId]: event.target.value }))} placeholder="RESTORE RETAINED ADOPTED FILE AFTER PLEX VERIFICATION" autoComplete="off" /><Button size="sm" disabled={busy === cycleId || restorePhrase !== "RESTORE RETAINED ADOPTED FILE AFTER PLEX VERIFICATION"} onClick={() => void restoreAdopted(adoptedRestorable)}><ArchiveRestore /> Restore and verify with Plex</Button></div> : null}<div className="mt-3 text-[12px] text-fg-subtle">Retention ends: {purgeAt ? new Date(purgeAt).toLocaleString() : "—"}. Link cycles purge automatically after 30 days. Adopted real files require an exact owner confirmation after retention and are never auto-deleted.</div></Panel> })}</div>
}

export function LiveAdoptionScreen() {
  const [batches, setBatches] = React.useState<APIAdoptionBatch[]>([])
  const [detail, setDetail] = React.useState<APIAdoptionBatch | null>(null)
  const [roots, setRoots] = React.useState<APILibraryRoot[]>([])
  const [profiles, setProfiles] = React.useState<APIQualityProfile[]>([])
  const [media, setMedia] = React.useState<APIMedia[]>([])
  const [rootID, setRootID] = React.useState("")
  const [profileID, setProfileID] = React.useState("")
  const [sourceRoot, setSourceRoot] = React.useState("")
  const [monitoring, setMonitoring] = React.useState(false)
  const [searchNow, setSearchNow] = React.useState(false)
  const [busy, setBusy] = React.useState("")
  const [error, setError] = React.useState("")
  const [executionConfirmation, setExecutionConfirmation] = React.useState("")
  const [adoptionReplacementConfirmation, setAdoptionReplacementConfirmation] = React.useState("")
  const refresh = React.useCallback(async () => {
    try {
      const [nextBatches, nextRoots, nextProfiles, nextMedia] = await Promise.all([loadAdoptionBatches(), loadLibraryRoots(), loadQualityProfiles(), loadCatalogMedia()])
      setBatches(nextBatches); setRoots(nextRoots.filter((item) => item.enabled)); setProfiles(nextProfiles.filter((item) => item.enabled)); setMedia(nextMedia); setError("")
      if (!rootID && nextRoots.length) { const root = nextRoots.find((item) => item.enabled); if (root) { setRootID(root.id); setSourceRoot(root.destination_root); const profile = nextProfiles.find((item) => item.enabled && item.media_kind === root.media_kind); if (profile) setProfileID(profile.id) } }
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load adoption review") }
  }, [rootID])
  React.useEffect(() => { const timer = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(timer) }, [refresh])
  async function openBatch(id: string) { setBusy(id); try { setDetail(await loadAdoptionBatch(id)); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load adoption batch") } finally { setBusy("") } }
  function changeRoot(value: string) { setRootID(value); const root = roots.find((item) => item.id === value); if (root) { setSourceRoot(root.destination_root); const profile = profiles.find((item) => item.media_kind === root.media_kind); setProfileID(profile?.id ?? "") } }
  async function scan(event: React.FormEvent) {
    event.preventDefault(); setBusy("scan"); setError("")
    try { const batch = await createAdoptionBatch({ source_root: sourceRoot, root_id: rootID, quality_profile_id: profileID, monitoring_enabled: monitoring, search_immediately: searchNow }); setDetail(batch); toast.success(`Read-only discovery persisted ${batch.items?.length ?? 0} review item(s)`); await refresh() }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not scan library") }
    finally { setBusy("") }
  }
  async function reloadBatch(batchID: string) { setDetail(await loadAdoptionBatch(batchID)); setBatches(await loadAdoptionBatches()) }
  async function approvePlan() {
    if (!detail) return
    setBusy("approve")
    try { const next = await approveAdoptionBatch(detail.id); setDetail(next); toast.success("Review plan approved; no file operation was queued"); await refresh() }
    catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not approve plan") }
    finally { setBusy("") }
  }
  async function executePlan() {
    if (!detail) return
    setBusy("execute")
    try { const next = await executeAdoptionBatch(detail.id, executionConfirmation, adoptionReplacementConfirmation); setDetail(next); setExecutionConfirmation(""); setAdoptionReplacementConfirmation(""); toast.success("Journaled adoption execution queued"); await refresh() }
    catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not queue adoption execution") }
    finally { setBusy("") }
  }
  const selectedRoot = roots.find((item) => item.id === rootID)
  const compatibleProfiles = profiles.filter((item) => item.media_kind === selectedRoot?.media_kind)
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="grid gap-4 xl:grid-cols-[1fr_390px]"><Panel flush title="Adoption review batches" subtitle="Discovery and plan approval never rename, move, publish, monitor, or search."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Source</TableHead><TableHead>Kind</TableHead><TableHead>Review</TableHead><TableHead>State</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{batches.map((batch) => <TableRow key={batch.id}><TableCell className="max-w-lg break-all font-mono text-[12px]">{batch.source_root}</TableCell><TableCell>{batch.media_kind}</TableCell><TableCell>{batch.approved_items} approved · {batch.rejected_items} rejected · {batch.attention_items + batch.pending_items} unresolved</TableCell><TableCell><StateBadge state={batch.state} /></TableCell><TableCell><Button size="sm" variant="outline" disabled={busy === batch.id} onClick={() => void openBatch(batch.id)}>Review</Button></TableCell></TableRow>)}</TableBody></Table></div>{batches.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No discovery plans exist.</div> : null}</Panel><Panel title="Start read-only discovery" subtitle="The source must be the selected configured library root or a subdirectory."><form className="grid gap-3" onSubmit={scan}><Field label="Library root"><Select value={rootID} onValueChange={changeRoot}><SelectTrigger className="mm-input w-full"><SelectValue placeholder="Select root" /></SelectTrigger><SelectContent>{roots.map((root) => <SelectItem key={root.id} value={root.id}>{root.name} · {root.media_kind}</SelectItem>)}</SelectContent></Select></Field><Field label="Source directory"><Input className="mm-input font-mono" value={sourceRoot} onChange={(event) => setSourceRoot(event.target.value)} required /></Field><Field label="Quality profile"><Select value={profileID} onValueChange={setProfileID}><SelectTrigger className="mm-input w-full" aria-label="Quality profile"><SelectValue placeholder="Select profile" /></SelectTrigger><SelectContent>{compatibleProfiles.map((profile) => <SelectItem key={profile.id} value={profile.id}>{profile.name}</SelectItem>)}</SelectContent></Select></Field><ToggleRow label="Monitoring after future execution" checked={monitoring} onChange={(value) => { setMonitoring(value); if (!value) setSearchNow(false) }} /><ToggleRow label="Search after future execution" checked={searchNow} onChange={(value) => { setSearchNow(value); if (value) setMonitoring(true) }} /><Button type="submit" disabled={!rootID || !profileID || busy === "scan"}><Search /> Read-only scan and review</Button></form><InlineNotice tone="info" title="Bounded discovery">The scanner reads directory entries only, never follows symlinks, and stops above 50,000 files.</InlineNotice></Panel></div>
    {detail ? <Panel title={`Review plan · ${detail.media_kind}`} subtitle={`${detail.source_root} · ${detail.state === "review" ? "approval records intent only" : "filesystem, Plex, and reviewed monitoring outcomes are durably journaled"}.`} action={detail.state === "review" ? <Button disabled={busy === "approve" || detail.approved_items === 0 || detail.attention_items + detail.pending_items > 0} onClick={() => void approvePlan()}>Approve reviewed plan · no execution</Button> : undefined}><div className="space-y-3">{(detail.items ?? []).map((item) => <AdoptionItemReview key={item.id} batch={detail} item={item} media={media.filter((entry) => entry.kind === detail.media_kind)} roots={roots.filter((entry) => entry.media_kind === detail.media_kind)} profiles={profiles.filter((entry) => entry.media_kind === detail.media_kind)} disabled={detail.state !== "review"} onSaved={() => reloadBatch(detail.id)} />)}{detail.items?.length === 0 ? <div className="py-8 text-center text-[12px] text-fg-subtle">The read-only scan found no supported video files.</div> : null}{detail.state === "approved" ? <div className="rounded-xl border border-warn-line/30 bg-warn-soft p-4"><div className="text-[12px] font-semibold text-warn">Irreversible live-action gate</div><div className="mt-2 text-[12px] leading-5 text-warn">Execution moves up to 25 reviewed files and recognized companions to their selected roots. Cross-root files are copied to an atomic temporary path, content-verified, and only then removed from the source. Unknown companions remain untouched. Every operation is journaled and rolled back on filesystem, bounded media-info, or Plex verification failure. Reviewed monitoring is applied only after Plex confirmation; an immediate search creates one paced workflow per exact scope.</div><Input className="mm-input mt-3 font-mono" value={executionConfirmation} onChange={(event) => setExecutionConfirmation(event.target.value)} placeholder="EXECUTE REVIEWED ADOPTION" />{detail.search_immediately ? <Input className="mm-input mt-2 font-mono" value={adoptionReplacementConfirmation} onChange={(event) => setAdoptionReplacementConfirmation(event.target.value)} placeholder="REPLACE ADOPTED FILE AFTER PLEX VERIFICATION" autoComplete="off" /> : null}<Button className="mt-3" variant="destructive" disabled={busy === "execute" || executionConfirmation !== "EXECUTE REVIEWED ADOPTION" || detail.search_immediately && adoptionReplacementConfirmation !== "REPLACE ADOPTED FILE AFTER PLEX VERIFICATION" || detail.approved_items > 25} onClick={() => void executePlan()}>Execute reviewed adoption</Button></div> : null}{detail.state === "executing" ? <InlineNotice tone="warn" title="Execution in progress">Filesystem operations and bounded media-info evidence are journaled. Completion requires Plex to confirm each exact destination path and verified provider identity before monitoring or search is applied.</InlineNotice> : null}{detail.state === "complete" ? <InlineNotice tone="info" title="Adoption verified">Every approved item has durable current-file path, fingerprint, exact scope, target profile, media-info tracks/facts, and Plex identity provenance. {detail.monitoring_enabled ? detail.search_immediately ? "Reviewed monitoring was applied and its fingerprint-authorized search was queued." : "Reviewed monitoring was applied without starting a search." : "Monitoring was intentionally left unchanged."}</InlineNotice> : null}{detail.state === "failed" ? <InlineNotice tone="bad" title="Adoption failed">The journal records the scoped failure and rollback outcome. Monitoring and search are not applied before successful Plex confirmation.</InlineNotice> : null}</div></Panel> : null}
  </div>
}

function AdoptionItemReview({ batch, item, media, roots, profiles, disabled, onSaved }: { batch: APIAdoptionBatch; item: APIAdoptionItem; media: APIMedia[]; roots: APILibraryRoot[]; profiles: APIQualityProfile[]; disabled: boolean; onSaved: () => Promise<void> }) {
  const [mediaID, setMediaID] = React.useState(item.media_id ?? "")
  const [rootID, setRootID] = React.useState(item.root_id ?? batch.default_root_id)
  const [profileID, setProfileID] = React.useState(item.quality_profile_id ?? batch.default_quality_profile_id)
  const [proposed, setProposed] = React.useState(item.proposed_relative ?? item.original_relative)
  const [seasonNumber, setSeasonNumber] = React.useState(() => { const parsed = parseSeriesVariant(item.variant); return parsed.kind === "season" || parsed.kind === "episode" ? String(parsed.season) : "1" })
  const [busy, setBusy] = React.useState(false)
  const season = Number(seasonNumber)
  const validSeason = batch.media_kind === "movie" || Number.isInteger(season) && season >= 0 && season <= 99
  const variant = batch.media_kind === "movie" ? "default" : formatSeasonVariant(season)
  async function save(state: "approved" | "rejected") { setBusy(true); try { await reviewAdoptionItem(batch.id, item.id, state === "rejected" ? { review_state: state } : { review_state: state, media_id: mediaID, root_id: rootID, quality_profile_id: profileID, proposed_relative: proposed, variant, monitoring_enabled: batch.monitoring_enabled, search_immediately: batch.search_immediately }); toast.success(`Adoption item ${state}`); await onSaved() } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not save review") } finally { setBusy(false) } }
  return <div className="rounded-xl border border-line bg-surface-2 p-3"><div className="flex flex-col gap-2 lg:flex-row lg:items-start lg:justify-between"><div className="min-w-0"><div className="break-all font-mono text-[12px] text-fg">{item.original_relative}</div><div className="mt-1 text-[12px] text-fg-subtle">{item.recognized_companions.length} recognized companion(s) · {item.unknown_companions.length} unknown companion(s){item.variant ? ` · ${item.variant}` : ""}</div></div><div className="flex gap-1"><StateBadge state={item.review_state} />{item.execution_state !== "pending" ? <StateBadge state={item.execution_state} /> : null}</div></div>{!disabled ? <div className="mt-3 grid gap-2 lg:grid-cols-[1fr_1fr_1fr_1.5fr_auto_auto]"><select className="mm-input h-10 rounded-md px-2 text-[12px]" value={mediaID} onChange={(event) => setMediaID(event.target.value)}><option value="">Match catalog item</option>{media.map((entry) => <option key={entry.id} value={entry.id}>{entry.title} {entry.year ? `(${entry.year})` : ""}</option>)}</select><select className="mm-input h-10 rounded-md px-2 text-[12px]" value={rootID} onChange={(event) => setRootID(event.target.value)}>{roots.map((entry) => <option key={entry.id} value={entry.id}>{entry.name}</option>)}</select><select className="mm-input h-10 rounded-md px-2 text-[12px]" value={profileID} onChange={(event) => setProfileID(event.target.value)}>{profiles.map((entry) => <option key={entry.id} value={entry.id}>{entry.name}</option>)}</select><Input className="mm-input font-mono text-[12px]" value={proposed} onChange={(event) => setProposed(event.target.value)} aria-label="Proposed relative path" />{batch.media_kind === "series" ? <Input className="mm-input w-20" type="number" min={0} max={99} value={seasonNumber} onChange={(event) => setSeasonNumber(event.target.value)} aria-label="Exact season number" /> : <div className="self-center text-[12px] text-fg-subtle">Movie</div>}<div className="flex gap-1"><Button size="sm" disabled={busy || !mediaID || !proposed || !validSeason} onClick={() => void save("approved")}>Approve</Button><Button size="sm" variant="ghost" disabled={busy} onClick={() => void save("rejected")}>Reject</Button></div></div> : null}</div>
}

export function LiveCollectionsScreen() {
  const [collections, setCollections] = React.useState<APICollection[]>([])
  const [media, setMedia] = React.useState<APIMedia[]>([])
  const [selected, setSelected] = React.useState<string[]>([])
  const [preview, setPreview] = React.useState<APIMedia[] | null>(null)
  const [name, setName] = React.useState("")
  const [description, setDescription] = React.useState("")
  const [busy, setBusy] = React.useState("")
  const [error, setError] = React.useState("")
  const refresh = React.useCallback(async () => { try { const [nextCollections, nextMedia] = await Promise.all([loadCollections(), loadCatalogMedia()]); setCollections(nextCollections); setMedia(nextMedia); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load collections") } }, [])
  React.useEffect(() => { const timer = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(timer) }, [refresh])
  function toggle(id: string, checked: boolean) { setSelected((current) => checked ? [...current, id] : current.filter((item) => item !== id)); setPreview(null) }
  async function runPreview() { setBusy("preview"); try { setPreview(await previewCollection(selected)); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not preview collection") } finally { setBusy("") } }
  async function save(event: React.FormEvent) { event.preventDefault(); if (!preview) return; setBusy("save"); try { await createCollection({ name: name.trim(), description: description.trim(), media_ids: preview.map((item) => item.id) }); toast.success("Manual collection persisted"); setName(""); setDescription(""); setSelected([]); setPreview(null); await refresh() } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not create collection") } finally { setBusy("") } }
  async function remove(id: string) { if (!window.confirm("Delete this grouping? Media records and files are not changed.")) return; setBusy(id); try { await deleteCollection(id); toast.success("Collection deleted"); await refresh() } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not delete collection") } finally { setBusy("") } }
  return <div className="grid gap-4 xl:grid-cols-[1fr_390px]">{error ? <div className="xl:col-span-2"><ErrorNotice error={error} /></div> : null}<Panel title="Manual collections" subtitle="Persisted groupings only; no unsupported smart-rule matches are claimed.">{collections.length === 0 ? <div className="py-10 text-center text-[12px] text-fg-subtle">No collections exist.</div> : <div className="grid gap-3 md:grid-cols-2">{collections.map((collection) => <div key={collection.id} className="rounded-xl border border-line bg-surface-2 p-3"><div className="flex items-start justify-between gap-3"><div><div className="text-[12px] font-semibold text-fg">{collection.name}</div><div className="mt-1 text-[12px] text-fg-subtle">{collection.members.length} member(s)</div></div><Button size="sm" variant="ghost" disabled={busy === collection.id} onClick={() => void remove(collection.id)}><Trash2 /> Delete</Button></div>{collection.description ? <div className="mt-2 text-[12px] text-fg-muted">{collection.description}</div> : null}<div className="mt-3 flex flex-wrap gap-1">{collection.members.map((item) => <StatusChip key={item.id}>{item.title}{item.year ? ` (${item.year})` : ""}</StatusChip>)}</div></div>)}</div>}</Panel><Panel title="Create with preview" subtitle="Select persisted catalog records, preview the exact server result, then save."><form className="grid gap-3" onSubmit={save}><Field label="Name"><Input className="mm-input" maxLength={200} value={name} onChange={(event) => setName(event.target.value)} required /></Field><Field label="Description"><Textarea className="mm-input min-h-20" maxLength={2000} value={description} onChange={(event) => setDescription(event.target.value)} /></Field><div className="max-h-64 space-y-1 overflow-y-auto rounded-xl border border-line bg-surface-2 p-2">{media.map((item) => <label key={item.id} className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-[12px] text-fg-secondary hover:bg-fg/[0.04]"><Checkbox checked={selected.includes(item.id)} onCheckedChange={(value) => toggle(item.id, value === true)} />{item.title} {item.year ? `(${item.year})` : ""}<span className="ml-auto text-fg-subtle">{item.kind}</span></label>)}{media.length === 0 ? <div className="p-4 text-center text-[12px] text-fg-subtle">Add catalog media before creating a collection.</div> : null}</div><Button type="button" variant="outline" disabled={selected.length === 0 || busy === "preview"} onClick={() => void runPreview()}><Search /> Preview {selected.length} member(s)</Button>{preview ? <InlineNotice tone="info" title="Server preview">{preview.map((item) => item.title).join(" · ")}</InlineNotice> : null}<Button type="submit" disabled={!preview || !name.trim() || busy === "save"}><Plus /> Save reviewed collection</Button></form></Panel></div>
}

function SeerrRoutingPanel({ instance, roots, profiles }: { instance: APIIntegration; roots: APILibraryRoot[]; profiles: APIQualityProfile[] }) {
  const [routing, setRouting] = React.useState<APISeerrRouting>({ routes: [], observed_folders: [] })
  const [error, setError] = React.useState("")
  const [busy, setBusy] = React.useState("")
  const [newKind, setNewKind] = React.useState<"movie" | "series">("movie")
  const [newFolder, setNewFolder] = React.useState("")
  const [newRoot, setNewRoot] = React.useState("")
  const [newProfile, setNewProfile] = React.useState("auto")
  const refresh = React.useCallback(async () => {
    try { setRouting(await loadSeerrRouting(instance.id)); setError("") }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load Seerr routing") }
  }, [instance.id])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  const rows = React.useMemo(() => {
    const observed = routing.observed_folders.map((folder) => ({ ...folder, route: routing.routes.find((route) => route.id === folder.route_id) }))
    const observedKeys = new Set(observed.map((folder) => `${folder.media_kind}\u0000${folder.root_folder}`))
    const configured = routing.routes.filter((route) => !observedKeys.has(`${route.media_kind}\u0000${route.root_folder}`)).map((route) => ({ media_kind: route.media_kind, root_folder: route.root_folder, request_count: 0, latest_at: route.updated_at, route_id: route.id, library_root_id: route.library_root_id, quality_profile_id: route.quality_profile_id, routed: route.enabled, route }))
    return [...observed, ...configured].sort((left, right) => left.media_kind.localeCompare(right.media_kind) || left.root_folder.localeCompare(right.root_folder))
  }, [routing])
  async function save(kind: "movie" | "series", folder: string, rootID: string, profileID: string) {
    if (!rootID) { setError("Choose a Media Manager library root for this folder first."); return }
    setBusy(`${kind}:${folder}`); setError("")
    try { await saveSeerrRoute(instance.id, { media_kind: kind, root_folder: folder, library_root_id: rootID, quality_profile_id: profileID === "auto" ? undefined : profileID }); toast.success(`${folder} mapped`); await refresh() }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not save routing") }
    finally { setBusy("") }
  }
  async function remove(routeID: string) {
    setBusy(routeID); setError("")
    try { await deleteSeerrRoute(instance.id, routeID); toast.success("Route removed"); await refresh() }
    catch (reason) { setError(reason instanceof Error ? reason.message : "Could not remove route") }
    finally { setBusy("") }
  }
  const newRoots = roots.filter((root) => root.media_kind === newKind && root.enabled)
  const newProfiles = profiles.filter((profile) => profile.media_kind === newKind && profile.enabled)
  return <Panel className="xl:col-span-2" title="Seerr request routing" subtitle="Map each root folder a requester chose in Seerr to a Media Manager library root. A request with an unmapped folder pauses for configuration instead of publishing into the wrong library.">
    {error ? <div className="mb-3"><ErrorNotice error={error} /></div> : null}
    <div className="overflow-x-auto">
      <Table>
        <TableHeader><TableRow><TableHead>Kind</TableHead><TableHead>Seerr root folder</TableHead><TableHead>Requests</TableHead><TableHead>Media Manager root</TableHead><TableHead>Quality profile</TableHead><TableHead>Action</TableHead></TableRow></TableHeader>
        <TableBody>{rows.map((row) => <SeerrRouteRow key={`${row.media_kind}:${row.root_folder}:${row.route?.id ?? "unrouted"}`} row={row} roots={roots} profiles={profiles} busy={busy} onSave={save} onRemove={remove} />)}</TableBody>
      </Table>
    </div>
    {rows.length === 0 ? <div className="py-6 text-center text-[12px] text-fg-subtle">No Seerr root folders observed yet. Add a mapping below; observed folders appear as requests arrive.</div> : null}
    <div className="mt-4 grid gap-2 border-t border-line pt-4 lg:grid-cols-[120px_1.2fr_1fr_1fr_auto] lg:items-end">
      <Field label="Kind"><Select value={newKind} onValueChange={(value) => { setNewKind(value as "movie" | "series"); setNewRoot(""); setNewProfile("auto") }}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="movie">Movie</SelectItem><SelectItem value="series">TV series</SelectItem></SelectContent></Select></Field>
      <Field label="Seerr root folder" hint="Exactly as returned by Seerr, e.g. /media/Comedy"><Input className="mm-input font-mono" value={newFolder} onChange={(event) => setNewFolder(event.target.value)} placeholder="/media/Comedy" /></Field>
      <Field label="Media Manager root"><Select value={newRoot || "unset"} onValueChange={setNewRoot}><SelectTrigger className="mm-input w-full"><SelectValue placeholder="Choose root" /></SelectTrigger><SelectContent><SelectItem value="unset" disabled>Choose root</SelectItem>{newRoots.map((root) => <SelectItem key={root.id} value={root.id}>{root.name}</SelectItem>)}</SelectContent></Select></Field>
      <Field label="Quality profile"><Select value={newProfile} onValueChange={setNewProfile}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="auto">Automatic (language)</SelectItem>{newProfiles.map((profile) => <SelectItem key={profile.id} value={profile.id}>{profile.name}</SelectItem>)}</SelectContent></Select></Field>
      <Button disabled={!newFolder.trim() || !newRoot || busy === `${newKind}:${newFolder.trim()}`} onClick={() => void save(newKind, newFolder.trim(), newRoot, newProfile)}><Plus /> Add route</Button>
    </div>
  </Panel>
}

type SeerrRouteRowData = APISeerrObservedFolder & { route?: APISeerrRoute }

function SeerrRouteRow({ row, roots, profiles, busy, onSave, onRemove }: { row: SeerrRouteRowData; roots: APILibraryRoot[]; profiles: APIQualityProfile[]; busy: string; onSave: (kind: "movie" | "series", folder: string, rootID: string, profileID: string) => Promise<void>; onRemove: (routeID: string) => Promise<void> }) {
  const [rootID, setRootID] = React.useState(row.route?.library_root_id ?? row.library_root_id ?? "")
  const [profileID, setProfileID] = React.useState(row.route?.quality_profile_id ?? row.quality_profile_id ?? "auto")
  const kindRoots = roots.filter((root) => root.media_kind === row.media_kind && root.enabled)
  const kindProfiles = profiles.filter((profile) => profile.media_kind === row.media_kind && profile.enabled)
  const pending = busy === `${row.media_kind}:${row.root_folder}` || busy === row.route_id
  return <TableRow>
    <TableCell>{row.media_kind === "movie" ? "Movie" : "TV"}</TableCell>
    <TableCell><div className="font-mono text-[12px] text-fg">{row.root_folder}</div>{row.routed ? null : <div className="mt-1 text-[11px] font-semibold uppercase tracking-wide text-warn">Needs mapping</div>}</TableCell>
    <TableCell>{row.request_count ? row.request_count : "—"}</TableCell>
    <TableCell><Select value={rootID || "unset"} onValueChange={setRootID}><SelectTrigger className="mm-input w-full" aria-label={`Library root for ${row.root_folder}`}><SelectValue placeholder="Choose root" /></SelectTrigger><SelectContent><SelectItem value="unset" disabled>Choose root</SelectItem>{kindRoots.map((root) => <SelectItem key={root.id} value={root.id}>{root.name}</SelectItem>)}</SelectContent></Select></TableCell>
    <TableCell><Select value={profileID} onValueChange={setProfileID}><SelectTrigger className="mm-input w-full" aria-label={`Quality profile for ${row.root_folder}`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="auto">Automatic (language)</SelectItem>{kindProfiles.map((profile) => <SelectItem key={profile.id} value={profile.id}>{profile.name}</SelectItem>)}</SelectContent></Select></TableCell>
    <TableCell><div className="flex items-center gap-1"><Button size="sm" variant="outline" disabled={pending} onClick={() => void onSave(row.media_kind, row.root_folder, rootID === "unset" ? "" : rootID, profileID)}>{pending ? <LoaderCircle className="animate-spin" /> : <Settings2 />} {row.route_id ? "Update" : "Map"}</Button>{row.route_id ? <Button size="sm" variant="ghost" disabled={pending} onClick={() => void onRemove(row.route_id as string)}><Trash2 /> Remove</Button> : null}</div></TableCell>
  </TableRow>
}

export function LiveConnectionsScreen() {
  const [items, setItems] = React.useState<APIIntegration[]>([])
  const [roots, setRoots] = React.useState<APILibraryRoot[]>([])
  const [profiles, setProfiles] = React.useState<APIQualityProfile[]>([])
  const seerr = items.find((item) => item.kind === "seerr" && item.enabled) ?? items.find((item) => item.kind === "seerr")
  const [kind, setKind] = React.useState("plex")
  const [name, setName] = React.useState("Plex")
  const [baseURL, setBaseURL] = React.useState("")
  const [secretRef, setSecretRef] = React.useState("PLEX_TOKEN")
  const [allowHTTP, setAllowHTTP] = React.useState(false)
  const [isDefault, setIsDefault] = React.useState(true)
  const [localRoot, setLocalRoot] = React.useState("/mnt/altmount")
  const [reportedRoot, setReportedRoot] = React.useState("/complete")
  const [error, setError] = React.useState("")
  const [busy, setBusy] = React.useState(false)
	const [commissioning, setCommissioning] = React.useState("")
  const refresh = React.useCallback(async () => {
    try {
      const [nextItems, nextRoots, nextProfiles] = await Promise.all([loadIntegrations(), loadLibraryRoots(), loadQualityProfiles()])
      setItems(nextItems); setRoots(nextRoots); setProfiles(nextProfiles); setError("")
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load integrations") }
  }, [])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  function changeConnectionKind(value: string) { const labels: Record<string, [string, string]> = { plex: ["Plex", "PLEX_TOKEN"], altmount: ["AltMount", "ALTMOUNT_SECRET"], newznab: ["Newznab indexer", "NEWZNAB_PRIMARY_API_KEY"], seerr: ["Seerr", "SEERR_API_KEY"] }; setKind(value); setName(labels[value][0]); setSecretRef(labels[value][1]) }
  async function submit(event: React.FormEvent) {
    event.preventDefault(); setBusy(true); setError("")
    try {
      const config = kind === "altmount" ? { base_url: baseURL, allow_http: allowHTTP, reported_root: reportedRoot, local_read_only_root: localRoot, category: "media-manager", request_timeout_ms: 30000 } : kind === "newznab" ? { base_url: baseURL, allow_http: allowHTTP, minimum_interval_ms: 3000, mode: "search", mode_verified: false, categories: [], extended: true } : { base_url: baseURL, allow_http: allowHTTP, request_timeout_ms: 20000 }
		const stagedNewznab = kind === "newznab"
		const created = await createIntegration({ kind, name, enabled: !stagedNewznab, is_default: stagedNewznab ? false : isDefault, config, secret_ref: secretRef })
      toast.success(`${created.name} saved`); await refresh()
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not save integration") } finally { setBusy(false) }
  }
	async function commission(item: APIIntegration) {
		setCommissioning(item.id); setError("")
		try { await commissionIntegration(item.id); toast.success(`${item.name} validated and enabled`); await refresh() }
		catch (reason) { setError(reason instanceof Error ? reason.message : "Could not validate indexer") }
		finally { setCommissioning("") }
	}
  return <div className="grid gap-4 xl:grid-cols-[1fr_380px]">
    <Panel flush title="Connected services" subtitle="Secrets remain environment references; values never return to the browser."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Type</TableHead><TableHead>Default</TableHead><TableHead>Secret reference</TableHead><TableHead>State</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => { const awaiting = item.kind === "newznab" && !item.enabled && item.config.mode_verified !== true; const canValidate = item.kind === "newznab" && !item.enabled; return <TableRow key={item.id}><TableCell className="font-semibold text-fg">{item.name}<div className="mt-1 font-mono text-[12px] text-fg-subtle">{item.id}</div></TableCell><TableCell>{item.kind}</TableCell><TableCell>{item.is_default ? "Yes" : "No"}</TableCell><TableCell className="font-mono text-[12px]">{item.secret_ref ?? "—"}</TableCell><TableCell><StateBadge state={item.enabled ? "enabled" : awaiting ? "awaiting_validation" : "disabled"} /></TableCell><TableCell>{canValidate ? <Button size="sm" variant="outline" disabled={commissioning === item.id} onClick={() => void commission(item)}>{commissioning === item.id ? <LoaderCircle className="animate-spin" /> : <Activity />} Validate & enable</Button> : "—"}</TableCell></TableRow> })}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No integrations configured.</div> : null}</Panel>
    <Panel title="Add connection" subtitle="Create the database record after its secret exists in .env.integrations.">{error ? <div className="mb-4"><ErrorNotice error={error} /></div> : null}<form className="grid gap-3" onSubmit={submit}><Field label="Connection type"><Select value={kind} onValueChange={changeConnectionKind}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="plex">Plex</SelectItem><SelectItem value="altmount">AltMount</SelectItem><SelectItem value="newznab">Newznab indexer</SelectItem><SelectItem value="seerr">Seerr record</SelectItem></SelectContent></Select></Field><Field label="Name"><Input className="mm-input" value={name} onChange={(event) => setName(event.target.value)} required /></Field><Field label="Base URL"><Input className="mm-input" value={baseURL} onChange={(event) => setBaseURL(event.target.value)} placeholder="https://service.example" required /></Field><Field label="Secret environment name"><Input className="mm-input font-mono" value={secretRef} onChange={(event) => setSecretRef(event.target.value)} required /></Field>{kind === "altmount" ? <div className="grid grid-cols-2 gap-2"><Field label="Reported root"><Input className="mm-input" value={reportedRoot} onChange={(event) => setReportedRoot(event.target.value)} /></Field><Field label="Read-only root"><Input className="mm-input" value={localRoot} onChange={(event) => setLocalRoot(event.target.value)} /></Field></div> : null}<ToggleRow label="Allow plain HTTP" checked={allowHTTP} onChange={setAllowHTTP} /><ToggleRow label="Make default" checked={isDefault} onChange={setIsDefault} /><Button disabled={busy} type="submit"><Plus /> Save connection</Button></form><div className="mt-3 text-[12px] leading-5 text-fg-subtle">For Seerr: save the record, use its UUID as <span className="font-mono">SEERR_INSTANCE_ID</span>, then restart with all four Seerr variables set.</div></Panel>
    {seerr ? <SeerrRoutingPanel instance={seerr} roots={roots} profiles={profiles} /> : null}
  </div>
}

export function LiveRootsScreen() {
  const [roots, setRoots] = React.useState<APILibraryRoot[]>([])
  const [plex, setPlex] = React.useState<APIIntegration[]>([])
  const [kind, setKind] = React.useState<"movie" | "series">("movie")
  const [name, setName] = React.useState("Movies Primary")
  const [destination, setDestination] = React.useState("/libraries/movies")
  const [plexRoot, setPlexRoot] = React.useState("/libraries/movies")
  const [sourceRoot, setSourceRoot] = React.useState("/mnt/altmount")
  const [plexID, setPlexID] = React.useState("")
  const [libraryID, setLibraryID] = React.useState("")
  const [isDefault, setIsDefault] = React.useState(true)
  const [error, setError] = React.useState("")
  const refresh = React.useCallback(async () => { try { const [nextRoots, integrations] = await Promise.all([loadLibraryRoots(), loadIntegrations()]); const nextPlex = integrations.filter((item) => item.kind === "plex" && item.enabled); setRoots(nextRoots); setPlex(nextPlex); setPlexID((current) => current || nextPlex.find((item) => item.is_default)?.id || nextPlex[0]?.id || ""); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load roots") } }, [])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  function changeRootKind(value: "movie" | "series") { const path = value === "movie" ? "/libraries/movies" : "/libraries/tv"; setKind(value); setName(value === "movie" ? "Movies Primary" : "TV Primary"); setDestination(path); setPlexRoot(path) }
  async function submit(event: React.FormEvent) { event.preventDefault(); try { await createLibraryRoot({ media_kind: kind, name, destination_root: destination, plex_visible_root: plexRoot, source_visible_root: sourceRoot, plex_instance_id: plexID, plex_library_id: libraryID, is_default: isDefault, enabled: true }); toast.success("Library root saved"); await refresh() } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not save root") } }
  return <div className="grid gap-4 xl:grid-cols-[1fr_380px]"><Panel flush title="Library roots" subtitle="Multiple roots with a default and per-item override."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Kind</TableHead><TableHead>Destination</TableHead><TableHead>Plex section</TableHead><TableHead>Default</TableHead></TableRow></TableHeader><TableBody>{roots.map((root) => <TableRow key={root.id}><TableCell className="font-semibold text-fg">{root.name}</TableCell><TableCell>{root.media_kind}</TableCell><TableCell className="font-mono text-[12px]">{root.destination_root}</TableCell><TableCell>{root.plex_library_id}</TableCell><TableCell>{root.is_default ? "Yes" : "No"}</TableCell></TableRow>)}</TableBody></Table></div></Panel><Panel title="Add Plex-backed root" subtitle="Media Manager needs write access; Plex receives this library read-only.">{error ? <div className="mb-4"><ErrorNotice error={error} /></div> : null}{plex.length === 0 ? <div className="mb-4"><InlineNotice tone="warn" title="Plex first">Create an enabled Plex connection before a root.</InlineNotice></div> : null}<form className="grid gap-3" onSubmit={submit}><Field label="Media kind"><Select value={kind} onValueChange={(value) => changeRootKind(value as "movie" | "series")}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="movie">Movie</SelectItem><SelectItem value="series">TV series</SelectItem></SelectContent></Select></Field><Field label="Name"><Input className="mm-input" value={name} onChange={(event) => setName(event.target.value)} /></Field><Field label="Media Manager destination"><Input className="mm-input font-mono" value={destination} onChange={(event) => setDestination(event.target.value)} /></Field><Field label="Path visible to Plex"><Input className="mm-input font-mono" value={plexRoot} onChange={(event) => setPlexRoot(event.target.value)} /></Field><Field label="AltMount path visible to Plex"><Input className="mm-input font-mono" value={sourceRoot} onChange={(event) => setSourceRoot(event.target.value)} /></Field><Field label="Plex connection"><Select value={plexID} onValueChange={setPlexID}><SelectTrigger className="mm-input w-full"><SelectValue placeholder="Choose Plex" /></SelectTrigger><SelectContent>{plex.map((item) => <SelectItem key={item.id} value={item.id}>{item.name}</SelectItem>)}</SelectContent></Select></Field><Field label="Plex library section ID"><Input className="mm-input" inputMode="numeric" value={libraryID} onChange={(event) => setLibraryID(event.target.value)} placeholder="1" /></Field><ToggleRow label="Make default" checked={isDefault} onChange={setIsDefault} /><Button disabled={!plexID || !libraryID} type="submit"><FolderTree /> Save root</Button></form></Panel></div>
}

export function LiveProfilesScreen() {
  const [profiles, setProfiles] = React.useState<APIQualityProfile[]>([])
  const [verifications, setVerifications] = React.useState<APISubtitleVerification[]>([])
  const [editingID, setEditingID] = React.useState("")
  const [kind, setKind] = React.useState<"movie" | "series">("movie")
  const [name, setName] = React.useState("UHD")
  const [resolution, setResolution] = React.useState("best_available")
  const [audio, setAudio] = React.useState("best_available")
  const [source, setSource] = React.useState("webdl")
  const [requiredSubtitles, setRequiredSubtitles] = React.useState("da")
  const [preferredSubtitles, setPreferredSubtitles] = React.useState("")
  const [originalLanguageRouting, setOriginalLanguageRouting] = React.useState("")
  const [upgrades, setUpgrades] = React.useState(true)
  const [isDefault, setIsDefault] = React.useState(true)
  const [enabled, setEnabled] = React.useState(true)
  const [busy, setBusy] = React.useState(false)
  const [reverifying, setReverifying] = React.useState("")
  const [error, setError] = React.useState("")
  const refresh = React.useCallback(async () => { try { const [nextProfiles, nextVerifications] = await Promise.all([loadQualityProfiles(), loadSubtitleVerifications()]); setProfiles(nextProfiles); setVerifications(nextVerifications); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load profiles") } }, [])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  function beginEdit(profile: APIQualityProfile) {
    const rules = Array.isArray(profile.policy.rules) ? profile.policy.rules as Array<Record<string, unknown>> : []
    const sourceRule = rules.find((rule) => rule.fact === "source" && rule.kind === "preferred")
    setEditingID(profile.id); setKind(profile.media_kind); setName(profile.name)
    setResolution(profileResolutionStrategy(rules)); setAudio(profileAudioStrategy(rules)); setSource(ruleFirstValue(sourceRule) || "none")
    setRequiredSubtitles(policyLanguages(profile, "required_subtitle_languages").join(", "))
    setPreferredSubtitles(policyLanguages(profile, "preferred_subtitle_languages").join(", "))
    setOriginalLanguageRouting(policyOriginalLanguages(profile).join(", "))
    setUpgrades(profile.upgrades_enabled); setIsDefault(profile.is_default); setEnabled(profile.enabled)
  }
  function resetForm() { setEditingID(""); setKind("movie"); setName("UHD"); setResolution("best_available"); setAudio("best_available"); setSource("webdl"); setRequiredSubtitles("da"); setPreferredSubtitles(""); setOriginalLanguageRouting(""); setUpgrades(true); setIsDefault(true); setEnabled(true) }
  async function submit(event: React.FormEvent) {
    event.preventDefault(); setBusy(true); setError("")
    try {
      const current = profiles.find((profile) => profile.id === editingID)
      const existingRules = current && Array.isArray(current.policy.rules) ? current.policy.rules as Array<Record<string, unknown>> : []
      const rules = mergeProfileRules(existingRules, resolution, audio, source)
      const policy = { ...(current?.policy ?? {}), rules, required_subtitle_languages: languageInput(requiredSubtitles), preferred_subtitle_languages: languageInput(preferredSubtitles), original_language_routing: languageInput(originalLanguageRouting) }
      const input = { media_kind: kind, name, policy_version: current?.policy_version ?? 1, policy, upgrades_enabled: upgrades, is_default: isDefault, enabled }
      if (editingID) await updateQualityProfile(editingID, input); else await createQualityProfile(input)
      toast.success(editingID ? "Quality profile updated" : "Quality profile saved"); resetForm(); await refresh()
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not save profile") } finally { setBusy(false) }
  }
  async function reverify(item: APISubtitleVerification) {
    if (!window.confirm("Reverify this exact retained AltMount output? No download is started. If every required subtitle is verified, the existing publication and Plex verification lifecycle will continue.")) return
    setReverifying(item.id); setError("")
    try {
      await reverifyRetainedOutput(item.workflow_id, item.altmount_job_id)
      toast.success("Retained-output reverification queued")
      await refresh()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Could not queue retained-output reverification")
    } finally {
      setReverifying("")
    }
  }
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="grid gap-4 xl:grid-cols-[1fr_400px]">
      <Panel flush title="Quality profiles" subtitle="Best available ranks quality without rejecting a safe fallback. Required subtitles are still verified before publication."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Kind</TableHead><TableHead>Quality</TableHead><TableHead>Audio</TableHead><TableHead>Original language</TableHead><TableHead>Required subtitles</TableHead><TableHead>Upgrades</TableHead><TableHead>State</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{profiles.map((profile) => { const rules = profileRules(profile); return <TableRow key={profile.id}><TableCell className="font-semibold text-fg">{profile.name}{profile.is_default ? <div className="mt-1 text-[12px] text-fg-subtle">Default fallback</div> : null}</TableCell><TableCell>{profile.media_kind}</TableCell><TableCell>{profileResolutionLabel(rules)}</TableCell><TableCell>{profileAudioLabel(rules)}</TableCell><TableCell className="font-mono text-[12px]">{policyOriginalLanguages(profile).join(", ") || "Fallback"}</TableCell><TableCell className="font-mono text-[12px]">{policyLanguages(profile, "required_subtitle_languages").join(", ") || "None"}</TableCell><TableCell>{profile.upgrades_enabled ? "On" : "Off"}</TableCell><TableCell><StateBadge state={profile.enabled ? "enabled" : "disabled"} /></TableCell><TableCell><Button size="sm" variant="outline" onClick={() => beginEdit(profile)}>Edit</Button></TableCell></TableRow> })}</TableBody></Table></div>{profiles.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No quality profiles exist.</div> : null}</Panel>
      <Panel title={editingID ? "Edit profile" : "Create profile"} subtitle="Best available prefers higher resolution and audio from positive release evidence. Unknown evidence stays neutral."><form className="grid gap-3" onSubmit={submit}><Field label="Media kind"><Select value={kind} disabled={Boolean(editingID)} onValueChange={(value) => setKind(value as "movie" | "series")}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="movie">Movie</SelectItem><SelectItem value="series">TV series</SelectItem></SelectContent></Select></Field><Field label="Name"><Input className="mm-input" value={name} onChange={(event) => setName(event.target.value)} required /></Field><Field label="Resolution policy"><Select value={resolution} onValueChange={setResolution}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="best_available">Best available</SelectItem><SelectItem value="2160p">Require 2160p</SelectItem><SelectItem value="1080p">Require 1080p</SelectItem><SelectItem value="720p">Require 720p</SelectItem><SelectItem value="none">Keep custom / none</SelectItem></SelectContent></Select><div className="mt-1 text-[12px] text-fg-subtle">Best available ranks 2160p, 1080p, 720p, 576p, then 480p without imposing a minimum.</div></Field><Field label="Audio policy"><Select value={audio} onValueChange={setAudio}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="best_available">Best available</SelectItem><SelectItem value="none">Keep custom / none</SelectItem></SelectContent></Select><div className="mt-1 text-[12px] text-fg-subtle">Positive release-name evidence ranks lossless and object-based codecs first. Unknown audio remains eligible.</div></Field><Field label="Preferred source"><Select value={source} onValueChange={setSource}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="none">Keep custom / none</SelectItem><SelectItem value="remux">Remux</SelectItem><SelectItem value="bluray">Blu-ray</SelectItem><SelectItem value="webdl">WEB-DL</SelectItem><SelectItem value="webrip">WEBRip</SelectItem></SelectContent></Select></Field><Field label="Original-language routing"><Input className="mm-input font-mono" value={originalLanguageRouting} onChange={(event) => { setOriginalLanguageRouting(event.target.value); if (event.target.value.trim()) setIsDefault(false) }} placeholder="da" /><div className="mt-1 text-[12px] text-fg-subtle">Routes Seerr requests from reliable provider metadata. Leave blank for the default fallback profile.</div></Field><Field label="Required subtitle languages"><Input className="mm-input font-mono" value={requiredSubtitles} onChange={(event) => setRequiredSubtitles(event.target.value)} placeholder="da" /><div className="mt-1 text-[12px] text-fg-subtle">Comma-separated reliable tags. Every selected video must satisfy every required language.</div></Field><Field label="Preferred subtitle languages"><Input className="mm-input font-mono" value={preferredSubtitles} onChange={(event) => setPreferredSubtitles(event.target.value)} placeholder="da, en" /></Field><ToggleRow label="Allow upgrades" checked={upgrades} onChange={setUpgrades} /><ToggleRow label="Make default" checked={isDefault} onChange={setIsDefault} /><ToggleRow label="Enabled" checked={enabled} onChange={setEnabled} /><div className="flex gap-2"><Button type="submit" disabled={busy}><Settings2 /> {editingID ? "Update profile" : "Save profile"}</Button>{editingID ? <Button type="button" variant="outline" onClick={resetForm}>Cancel</Button> : null}</div></form></Panel>
    </div>
    <Panel flush title="AltMount subtitle verification" subtitle="Persisted owner-review evidence. Unverified output is retained, never made current, and never automatically deleted."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Media / release</TableHead><TableHead>Lifetime attempt</TableHead><TableHead>Required</TableHead><TableHead>Observed</TableHead><TableHead>Result</TableHead><TableHead>Retry</TableHead><TableHead>Evidence</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{verifications.map((item) => <TableRow key={item.id}><TableCell className="max-w-sm"><div className="font-semibold text-fg">{item.media_title}</div><div className="mt-1 break-all font-mono text-[12px] text-fg-subtle">{item.release_title}</div></TableCell><TableCell>#{item.attempt_generation}</TableCell><TableCell className="font-mono text-[12px]">{item.required_languages.join(", ") || "None"}</TableCell><TableCell className="font-mono text-[12px]">{item.observed_languages.join(", ") || "Unknown"}</TableCell><TableCell><StateBadge state={item.status} /></TableCell><TableCell><StateBadge state={item.retry_state} /></TableCell><TableCell className="max-w-md text-[12px] text-fg-secondary">{item.failure_reasons.join(" · ") || `Verified ${timeAgo(item.created_at)}`}</TableCell><TableCell>{item.can_reverify ? <Button size="sm" variant="outline" disabled={reverifying === item.id} onClick={() => void reverify(item)}>{reverifying === item.id ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}Reverify output</Button> : "—"}</TableCell></TableRow>)}</TableBody></Table></div>{verifications.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No completed AltMount output has been subtitle-verified yet.</div> : null}</Panel>
  </div>
}

function policyLanguages(profile: APIQualityProfile, key: "required_subtitle_languages" | "preferred_subtitle_languages") {
  const value = profile.policy[key]
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : []
}

function policyOriginalLanguages(profile: APIQualityProfile) {
  const value = profile.policy.original_language_routing
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : []
}

function profileRules(profile: APIQualityProfile) {
  return Array.isArray(profile.policy.rules) ? profile.policy.rules as Array<Record<string, unknown>> : []
}

function ruleFirstValue(rule: Record<string, unknown> | undefined) {
  const values = rule?.allowed_values
  return Array.isArray(values) && typeof values[0] === "string" ? values[0] : ""
}

function languageInput(value: string) { return value.split(",").map((item) => item.trim()).filter(Boolean) }

const bestAvailableResolutionRules = [
  { label: "2160p", value: "2160p", weight: 50000 },
  { label: "1080p", value: "1080p", weight: 40000 },
  { label: "720p", value: "720p", weight: 30000 },
  { label: "576p", value: "576p", weight: 20000 },
  { label: "480p", value: "480p", weight: 10000 },
]

const bestAvailableAudioRules = [
  { label: "TrueHD Atmos", value: "truehd_atmos", weight: 5300 },
  { label: "DTS:X", value: "dts_x", weight: 5100 },
  { label: "TrueHD", value: "truehd", weight: 5000 },
  { label: "DTS-HD MA", value: "dts_hd_ma", weight: 4800 },
  { label: "DTS-HD", value: "dts_hd", weight: 4700 },
  { label: "FLAC", value: "flac", weight: 4600 },
  { label: "LPCM", value: "lpcm", weight: 4400 },
  { label: "E-AC-3 Atmos", value: "eac3_atmos", weight: 3300 },
  { label: "E-AC-3", value: "eac3", weight: 3000 },
  { label: "DTS", value: "dts", weight: 2800 },
  { label: "AC-3", value: "ac3", weight: 2000 },
  { label: "Opus", value: "opus", weight: 1800 },
  { label: "AAC", value: "aac", weight: 1500 },
  { label: "MP3", value: "mp3", weight: 1000 },
]

function bestAvailableRules(fact: "resolution" | "audio_codec", values: typeof bestAvailableResolutionRules) {
  const prefix = fact === "resolution" ? "Best available resolution" : "Best available audio"
  return values.map((item) => ({ name: `${prefix}: ${item.label}`, fact, allowed_values: [item.value], kind: "preferred", weight: item.weight, unknown: "allow" }))
}

function hasBestAvailableRules(rules: Array<Record<string, unknown>>, fact: "resolution" | "audio_codec", values: typeof bestAvailableResolutionRules) {
  return values.every((item) => rules.some((rule) => rule.fact === fact && rule.kind === "preferred" && rule.weight === item.weight && ruleFirstValue(rule) === item.value))
}

function profileResolutionStrategy(rules: Array<Record<string, unknown>>) {
  if (hasBestAvailableRules(rules, "resolution", bestAvailableResolutionRules)) return "best_available"
  const required = rules.find((rule) => rule.fact === "resolution" && rule.kind === "required")
  const value = ruleFirstValue(required)
  return ["2160p", "1080p", "720p"].includes(value) ? value : "none"
}

function profileAudioStrategy(rules: Array<Record<string, unknown>>) {
  return hasBestAvailableRules(rules, "audio_codec", bestAvailableAudioRules) ? "best_available" : "none"
}

function profileResolutionLabel(rules: Array<Record<string, unknown>>) {
  const strategy = profileResolutionStrategy(rules)
  if (strategy === "best_available") return "Best available"
  if (strategy !== "none") return `Require ${strategy}`
  return "Custom / none"
}

function profileAudioLabel(rules: Array<Record<string, unknown>>) {
  return profileAudioStrategy(rules) === "best_available" ? "Best available" : "Custom / none"
}

function mergeProfileRules(existing: Array<Record<string, unknown>>, resolution: string, audio: string, source: string) {
  let rules = existing.map((rule) => ({ ...rule }))
  if (resolution !== "none") {
    rules = rules.filter((rule) => rule.fact !== "resolution")
    if (resolution === "best_available") rules.push(...bestAvailableRules("resolution", bestAvailableResolutionRules))
    else rules.push({ name: "Required resolution", fact: "resolution", kind: "required", allowed_values: [resolution], weight: 0, unknown: "hold" })
  }
  if (audio !== "none") {
    rules = rules.filter((rule) => rule.fact !== "audio_codec")
    if (audio === "best_available") rules.push(...bestAvailableRules("audio_codec", bestAvailableAudioRules))
  }
  function replace(fact: string, kind: string, value: string, fallback: Record<string, unknown>) {
    if (value === "none") return
    const index = rules.findIndex((rule) => rule.fact === fact && rule.kind === kind)
    const next = { ...(index >= 0 ? rules[index] : fallback), allowed_values: [value] }
    if (index >= 0) rules[index] = next; else rules.push(next)
  }
  replace("source", "preferred", source, { name: "Preferred source", fact: "source", kind: "preferred", weight: 100, unknown: "allow" })
  return rules
}

export function LiveProwlarrImportScreen() {
  const [result, setResult] = React.useState<APIProwlarrImport | null>(null)
  const [selected, setSelected] = React.useState<Set<string>>(new Set())
  const [secretLines, setSecretLines] = React.useState("")
  const [secretsSaved, setSecretsSaved] = React.useState(false)
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState("")

  async function upload(file: File | undefined) {
    if (!file) return
    setBusy(true)
    setError("")
    try {
      const imported = await uploadProwlarrBackup(file)
      const usable = imported.candidates.filter((candidate) => candidate.review_state === "pending" && candidate.api_key && candidate.secret_ref)
      setResult(imported)
      setSelected(new Set(usable.map((candidate) => candidate.id)))
      setSecretLines(usable.map((candidate) => `${candidate.secret_ref}=${candidate.api_key}`).join("\n"))
      setSecretsSaved(false)
      toast.success(`Prowlarr backup parsed: ${usable.length} Newznab candidate${usable.length === 1 ? "" : "s"}`)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Could not parse the Prowlarr backup")
    } finally {
      setBusy(false)
    }
  }

  function toggle(id: string, checked: boolean) {
    setSelected((current) => {
      const next = new Set(current)
      if (checked) next.add(id); else next.delete(id)
      return next
    })
    setSecretsSaved(false)
  }

  async function approve() {
    if (!result || !secretsSaved) return
    setBusy(true)
    setError("")
    try {
      const candidates = result.candidates.filter((candidate) => selected.has(candidate.id) && candidate.secret_ref).map((candidate) => ({ id: candidate.id, secret_ref: candidate.secret_ref! }))
      const completed = await approveProwlarrImport(result.id, candidates)
      setResult(completed)
      toast.success(`${candidates.length} Newznab indexer${candidates.length === 1 ? "" : "s"} added for safe commissioning`)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Could not approve the Prowlarr import")
    } finally {
      setBusy(false)
    }
  }

  const pending = result?.candidates.filter((candidate) => candidate.review_state === "pending") ?? []
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <Panel title="Import a Prowlarr backup" subtitle="Prowlarr is used only as a one-time source of indexer settings. Media Manager never depends on its runtime.">
      <div className="grid gap-3">
        <Field label="Prowlarr ZIP backup" hint="The upload is capped at 128 MiB, integrity-checked, and only prowlarr.db is read."><Input type="file" accept=".zip,application/zip" disabled={busy} onChange={(event) => void upload(event.target.files?.[0])} /></Field>
        {busy ? <div className="flex items-center gap-2 text-[12px] text-fg-subtle"><LoaderCircle className="size-4 animate-spin" /> Reading backup…</div> : <div className="flex items-center gap-2 text-[12px] text-fg-subtle"><Upload className="size-4" /> Choose a backup above to begin a read-only review.</div>}
      </div>
    </Panel>
    {result ? <>
      <Panel flush title="Indexer review" subtitle="Only detected Usenet/Newznab records can be approved; torrent and incomplete records remain held.">
        <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead className="w-12">Use</TableHead><TableHead>Name</TableHead><TableHead>Implementation</TableHead><TableHead>Endpoint</TableHead><TableHead>State</TableHead></TableRow></TableHeader><TableBody>{result.candidates.map((candidate) => <TableRow key={candidate.id}><TableCell><Checkbox checked={selected.has(candidate.id)} disabled={candidate.review_state !== "pending" || result.state === "complete"} onCheckedChange={(checked) => toggle(candidate.id, checked === true)} aria-label={`Import ${candidate.name}`} /></TableCell><TableCell className="font-semibold text-fg">{candidate.name}</TableCell><TableCell>{candidate.implementation}</TableCell><TableCell className="max-w-md break-all font-mono text-[12px]">{candidate.base_url || "—"}</TableCell><TableCell><StateBadge state={candidate.review_state} />{candidate.attention_detail ? <div className="mt-1 max-w-sm text-[12px] text-warn">{candidate.attention_detail}</div> : null}</TableCell></TableRow>)}</TableBody></Table></div>
      </Panel>
      {result.state !== "complete" && pending.length > 0 ? <Panel title="Save the imported API keys" subtitle="Secrets are deliberately not stored in the review database or returned after this page.">
        <InlineNotice tone="warn" title="Required before approval">Copy these lines into <span className="font-mono">.env.integrations</span>, then keep that file private. Approval creates Media Manager indexers that reference these environment names.</InlineNotice>
        <div className="mt-3 grid gap-2"><Textarea className="min-h-32 font-mono text-[12px]" readOnly spellCheck={false} value={secretLines || "No usable API keys were found."} /><Button variant="outline" disabled={!secretLines} onClick={() => void navigator.clipboard.writeText(secretLines).then(() => toast.success("Secret lines copied")).catch(() => toast.error("Clipboard access was unavailable"))}><Copy /> Copy secret lines</Button></div>
        <label className="mt-4 flex items-start gap-2 rounded-lg border border-line bg-surface-2 p-3 text-[12px] leading-5 text-fg-muted"><Checkbox checked={secretsSaved} onCheckedChange={(checked) => setSecretsSaved(checked === true)} />I saved the selected secret lines in <span className="font-mono">.env.integrations</span>.</label>
        <Button className="mt-4" disabled={busy || selected.size === 0 || !secretsSaved} onClick={() => void approve()}><Plus /> Approve {selected.size} indexer{selected.size === 1 ? "" : "s"}</Button>
      </Panel> : null}
      {result.state === "complete" ? <InlineNotice tone="info" title="Import review complete">Approved indexers are staged disabled in Connections. Run <span className="font-mono">docker compose up -d --force-recreate api</span> after changing <span className="font-mono">.env.integrations</span>, then validate each indexer. Validation uses only paced CAPS and one negative search.</InlineNotice> : null}
    </> : null}
  </div>
}

function ToggleRow({ label, checked, onChange }: { label: string; checked: boolean; onChange: (value: boolean) => void }) { return <div className="flex items-center justify-between rounded-lg border border-line bg-surface-2 px-3 py-2.5"><span className="text-[13px] font-medium text-fg-secondary">{label}</span><Switch checked={checked} onCheckedChange={onChange} aria-label={label} /></div> }

export function LiveUnavailableScreen() { return <EmptyPanel title="This surface is not connected yet" copy="The live owner build exposes catalog, add, workflows, recycle, roots, profiles, connections, Prowlarr backup review, and health. Development-only concept data is never shown here." href="/" action="Return to overview" /> }
