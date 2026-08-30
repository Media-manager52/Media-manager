"use client"

import * as React from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Activity, ArchiveRestore, CircleAlert, Copy, Database, FolderTree, HeartPulse, Library, LoaderCircle, Plus, RefreshCw, Search, ServerCog, Settings2, Trash2, Upload } from "lucide-react"
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
  loadIntegrations,
  loadAdoptionBatch,
  loadAdoptionBatches,
  loadCatalogMedia,
  loadCollections,
  loadLibraryRoots,
  loadMedia,
  loadMediaDetail,
  loadDiscoveryMetadata,
  loadQualityProfiles,
  loadRecycle,
  loadWorkflows,
  recycleMedia,
  deleteCollection,
  restoreRecycle,
  restoreAdoptedFiles,
  reviewAdoptionItem,
  previewCollection,
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
  type APIRecycleEntry,
  type APIProwlarrImport,
  type APIWorkflow,
} from "@/lib/media-manager-api"
import { InlineNotice, KeyValue, MetricCard, Panel, StatusChip } from "./ui"

type Tone = "good" | "info" | "warn" | "bad" | "neutral"

function workflowTone(state: string): Tone {
  if (state === "plex_confirmed") return "good"
  if (["needs_attention", "held", "blocked", "quarantined"].includes(state)) return "warn"
  if (["cancelled"].includes(state)) return "bad"
  if (["searching", "submitting", "queued", "streaming_ready", "verifying", "publishing", "plex_scanning"].includes(state)) return "info"
  return "neutral"
}

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

function ErrorNotice({ error }: { error: string }) {
  return <InlineNotice tone="bad" title="Could not complete this operation">{error}</InlineNotice>
}

function EmptyPanel({ title, copy, href, action }: { title: string; copy: string; href?: string; action?: string }) {
  return <Panel><div className="grid min-h-44 place-items-center text-center"><div><Library className="mx-auto size-7 text-[#65748c]" /><h2 className="mt-3 text-sm font-bold text-white">{title}</h2><p className="mt-2 max-w-md text-[11px] leading-5 text-[#7f8a9a]">{copy}</p>{href && action ? <Button asChild className="mt-4" size="sm"><Link href={href}><Plus /> {action}</Link></Button> : null}</div></div></Panel>
}

export function LiveDashboardScreen() {
  const [media, setMedia] = React.useState<Awaited<ReturnType<typeof loadMedia>>>([])
  const [workflows, setWorkflows] = React.useState<APIWorkflow[]>([])
  const [recycle, setRecycle] = React.useState<APIRecycleEntry[]>([])
  const [error, setError] = React.useState("")
  React.useEffect(() => {
    const controller = new AbortController()
    Promise.all([loadMedia(controller.signal), loadWorkflows(true, controller.signal), loadRecycle(controller.signal)])
      .then(([nextMedia, nextWorkflows, nextRecycle]) => { setMedia(nextMedia); setWorkflows(nextWorkflows); setRecycle(nextRecycle) })
      .catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Could not load the overview") })
    return () => controller.abort()
  }, [])
  const attention = workflows.filter((workflow) => ["needs_attention", "held", "blocked", "quarantined"].includes(workflow.state)).length
  return <div className="space-y-4">
    {error ? <ErrorNotice error={error} /> : null}
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <MetricCard label="Catalog" value={String(media.length)} note="Movies and series" icon={Library} />
      <MetricCard label="Active workflows" value={String(workflows.length)} note="Durable acquisition state" tone="info" icon={Activity} />
      <MetricCard label="Needs attention" value={String(attention)} note="No hidden global failure" tone={attention ? "warn" : "good"} icon={CircleAlert} />
      <MetricCard label="Recycle entries" value={String(recycle.filter((entry) => entry.state !== "purged" && entry.state !== "restored").length)} note="30-day retention controls" icon={ArchiveRestore} />
    </div>
    <div className="grid gap-4 xl:grid-cols-[1fr_330px]">
      <WorkflowTable workflows={workflows.slice(0, 8)} empty="No active workflows." />
      <Panel title="Start here" subtitle="The shortest path from request to Plex.">
        <div className="grid gap-2"><Button asChild><Link href="/discover"><Plus /> Add movie or season</Link></Button><Button asChild variant="outline"><Link href="/settings/connections"><ServerCog /> Configure services</Link></Button><Button asChild variant="outline"><Link href="/activity/queue"><Activity /> Open workflow queue</Link></Button></div>
        <div className="mt-4 border-t border-white/[0.06] pt-3 text-[10px] leading-5 text-[#7f8a9a]">Season packs stay one AltMount job. Publication waits for exact episode coverage and Plex path confirmation.</div>
      </Panel>
    </div>
  </div>
}

export function LiveWorkflowScreen() {
  const [workflows, setWorkflows] = React.useState<APIWorkflow[]>([])
  const [error, setError] = React.useState("")
  const [busy, setBusy] = React.useState("")
  const [replacementConfirmations, setReplacementConfirmations] = React.useState<Record<string, string>>({})
  const refresh = React.useCallback(async () => {
    try { setWorkflows(await loadWorkflows(false)); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load workflows") }
  }, [])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); const timer = window.setInterval(() => void refresh(), 8000); return () => { window.clearTimeout(initial); window.clearInterval(timer) } }, [refresh])
  async function act(workflow: APIWorkflow, action: "search" | "cancel") {
    setBusy(workflow.id)
    try { if (action === "search") await searchWorkflow(workflow.id, replacementConfirmations[workflow.id] ?? ""); else await cancelWorkflow(workflow.id); toast.success(action === "search" ? "Search queued" : "Workflow cancelled"); setReplacementConfirmations((current) => ({ ...current, [workflow.id]: "" })); await refresh() } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Operation failed") } finally { setBusy("") }
  }
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}<Panel className="!p-0" title="Workflow activity" subtitle="Real persisted state; no invented percentages." action={<Button variant="outline" size="sm" onClick={() => void refresh()}><RefreshCw /> Refresh</Button>}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Media</TableHead><TableHead>Scope</TableHead><TableHead>State</TableHead><TableHead>Updated</TableHead><TableHead>Actions</TableHead></TableRow></TableHeader><TableBody>{workflows.map((workflow) => { const replacementPhrase = replacementConfirmations[workflow.id] ?? ""; return <TableRow key={workflow.id}><TableCell><Link className="font-semibold text-[#e5eaf0] hover:text-[#aebdff]" href={workflow.media_kind === "movie" ? `/media/movies/${workflow.media_id}` : `/media/series/${workflow.media_id}`}>{workflow.title}</Link><div className="mt-1 text-[9px] text-[#727e8e]">{workflow.year ?? "—"}</div></TableCell><TableCell className="font-mono text-[9px]">{workflow.variant}</TableCell><TableCell><StatusChip tone={workflowTone(workflow.state)}>{prettyState(workflow.state)}</StatusChip>{workflow.attention_detail ? <div className="mt-1 max-w-sm text-[9px] text-[#d6b36d]">{workflow.attention_detail}</div> : null}</TableCell><TableCell>{timeAgo(workflow.updated_at)}</TableCell><TableCell><div className="flex min-w-64 flex-col gap-1">{workflow.adopted_replacement_confirmation_required ? <Input className="mm-input font-mono text-[9px]" value={replacementPhrase} onChange={(event) => setReplacementConfirmations((current) => ({ ...current, [workflow.id]: event.target.value }))} placeholder="REPLACE ADOPTED FILE AFTER PLEX VERIFICATION" autoComplete="off" /> : null}<div className="flex gap-1"><Button size="sm" variant="outline" disabled={busy === workflow.id || !["monitored", "waiting", "needs_attention"].includes(workflow.state) || workflow.adopted_replacement_confirmation_required && replacementPhrase !== "REPLACE ADOPTED FILE AFTER PLEX VERIFICATION"} onClick={() => void act(workflow, "search")}><Search /> Search</Button><Button size="sm" variant="ghost" disabled={busy === workflow.id || !["monitored", "waiting", "searching", "held", "blocked", "needs_attention"].includes(workflow.state)} onClick={() => void act(workflow, "cancel")}>Cancel</Button></div></div></TableCell></TableRow> })}</TableBody></Table></div>{workflows.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No workflows yet.</div> : null}</Panel></div>
}

function WorkflowTable({ workflows, empty }: { workflows: APIWorkflow[]; empty: string }) {
  return <Panel className="!p-0" title="Recent active workflows" subtitle="Current state from PostgreSQL."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Media</TableHead><TableHead>Scope</TableHead><TableHead>State</TableHead><TableHead>Updated</TableHead></TableRow></TableHeader><TableBody>{workflows.map((workflow) => <TableRow key={workflow.id}><TableCell className="font-semibold text-[#e5eaf0]">{workflow.title}</TableCell><TableCell className="font-mono text-[9px]">{workflow.variant}</TableCell><TableCell><StatusChip tone={workflowTone(workflow.state)}>{prettyState(workflow.state)}</StatusChip></TableCell><TableCell>{timeAgo(workflow.updated_at)}</TableCell></TableRow>)}</TableBody></Table></div>{workflows.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">{empty}</div> : null}</Panel>
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
      const identities = metadataDetail ? metadataDetail.identities.map((identity) => ({ ...identity, state: "verified" as const, source: "seerr_metadata" as const })) : [{ namespace: provider, value: providerID.trim(), state: "verified" as const, source: "manual" as const }]
      await createMedia({ kind, title: title.trim(), year: parsedYear, identities, search_immediately: searchNow, ...(rootID !== "default" ? { root_id: rootID } : {}), ...(profileID !== "default" ? { quality_profile_id: profileID } : {}), ...(clientID !== "default" ? { download_client_id: clientID } : {}), ...(seasons ? { seasons } : {}) })
      toast.success(kind === "movie" ? "Movie workflow created" : "Season-pack workflows created")
      router.push("/activity/queue")
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not add media") } finally { setSubmitting(false) }
  }
  return <div className="space-y-4">
    <Panel title="Search verified metadata through Seerr" subtitle="Search is owner-triggered. Selecting a result performs a detail lookup before its identities can be submitted.">
      <form className="flex flex-col gap-2 sm:flex-row" onSubmit={findMetadata}><Input className="mm-input flex-1" value={metadataQuery} onChange={(event) => setMetadataQuery(event.target.value)} minLength={2} maxLength={200} placeholder="Search movies and TV series…" required /><Button type="submit" disabled={metadataBusy}>{metadataBusy ? <LoaderCircle className="animate-spin" /> : <Search />} Search metadata</Button></form>
      {metadataError ? <div className="mt-3"><InlineNotice tone="warn" title="Metadata search unavailable">{metadataError}</InlineNotice></div> : null}
      {metadataResults.length > 0 ? <div className="mt-4 grid gap-2 md:grid-cols-2 xl:grid-cols-3">{metadataResults.slice(0, 24).map((result) => { const resultTitle = result.mediaType === "movie" ? result.title : result.name; const resultDate = result.mediaType === "movie" ? result.releaseDate : result.firstAirDate; return <button type="button" key={`${result.mediaType}:${result.id}`} disabled={metadataBusy} onClick={() => void selectMetadata(result)} className="rounded-xl border border-white/[0.07] bg-[#0c1015] p-3 text-left transition hover:border-[#46536a] disabled:opacity-50"><div className="text-[10.5px] font-bold text-[#e4e9ef]">{resultTitle || "Untitled result"}</div><div className="mt-1 text-[9px] text-[#788493]">{result.mediaType === "movie" ? "Movie" : "TV series"} · {resultDate?.slice(0, 4) || "Year unavailable"} · TMDB {result.id}</div>{result.overview ? <div className="mt-2 line-clamp-2 text-[9px] leading-4 text-[#707c8b]">{result.overview}</div> : null}</button> })}</div> : null}
    </Panel>
    <div className="grid gap-4 xl:grid-cols-[1fr_340px]">
    <Panel title="Add directly to Media Manager" subtitle={metadataDetail ? "Verified Seerr metadata is selected. Series season numbers resolve to immutable episode snapshots before creation." : "Manual identity fallback. Series snapshots become one preferred pack job per season."}>
      {error ? <div className="mb-4"><ErrorNotice error={error} /></div> : null}
      {metadataDetail ? <div className="mb-4"><InlineNotice tone="info" title="Verified metadata selected">{metadataDetail.identities.map((identity) => `${identity.namespace.toUpperCase()} ${identity.value}`).join(" · ")}. Editing identity fields switches this submission back to manual provenance.</InlineNotice></div> : null}
      {!loading && !configured ? <div className="mb-4"><InlineNotice tone="warn" title="Finish setup first">You need an enabled Plex-backed {kind} root, {kind} quality profile, AltMount client{searchNow ? ", and at least one Newznab indexer" : ""}. <Link className="underline" href="/settings/connections">Open setup</Link>.</InlineNotice></div> : null}
      <form className="grid gap-4" onSubmit={submit}>
        <div className="grid gap-3 sm:grid-cols-2"><Field label="Media type"><Select value={kind} onValueChange={(value) => changeKind(value as "movie" | "series")}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="movie">Movie</SelectItem><SelectItem value="series">TV series</SelectItem></SelectContent></Select></Field><Field label="Release year"><Input className="mm-input" inputMode="numeric" value={year} onChange={(event) => setYear(event.target.value)} placeholder="2026" required /></Field></div>
        <Field label="Title"><Input className="mm-input" value={title} onChange={(event) => setTitle(event.target.value)} placeholder={kind === "movie" ? "Movie title" : "Series title"} required /></Field>
        <div className="grid gap-3 sm:grid-cols-2"><Field label="Verified provider"><Select value={provider} onValueChange={(value) => { clearVerifiedSelection(); setProvider(value) }}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent>{kind === "movie" ? <><SelectItem value="tmdb">TMDB</SelectItem><SelectItem value="imdb">IMDb</SelectItem></> : <><SelectItem value="tvdb">TVDB</SelectItem><SelectItem value="tmdb">TMDB</SelectItem></>}</SelectContent></Select></Field><Field label={`${provider.toUpperCase()} ID`}><Input className="mm-input" value={providerID} onChange={(event) => { clearVerifiedSelection(); setProviderID(event.target.value) }} placeholder={provider === "imdb" ? "tt1234567" : "12345"} required /></Field></div>
        {kind === "series" ? <Field label={metadataDetail ? "Season numbers" : "Exact seasons and episode counts"} hint={metadataDetail ? "Comma-separated seasons. Seerr supplies each exact episode snapshot, for example 1, 2." : "Format season:count. Example: 1:10, 2:8 creates two immutable episode snapshots."}><Input className="mm-input" value={seasonSpec} onChange={(event) => setSeasonSpec(event.target.value)} required /></Field> : null}
        <div className="grid gap-3 sm:grid-cols-3"><ConfigSelect label="Library root" value={rootID} onChange={setRootID} items={compatibleRoots.map((item) => ({ id: item.id, label: `${item.name}${item.is_default ? " · default" : ""}` }))} /><ConfigSelect label="Quality profile" value={profileID} onChange={setProfileID} items={compatibleProfiles.map((item) => ({ id: item.id, label: `${item.name}${item.is_default ? " · default" : ""}` }))} /><ConfigSelect label="Download client" value={clientID} onChange={setClientID} items={clients.map((item) => ({ id: item.id, label: `${item.name}${item.is_default ? " · default" : ""}` }))} /></div>
        <div className="flex items-center justify-between rounded-xl border border-white/[0.07] bg-[#0b0f14] p-3"><div><div className="text-[11px] font-bold text-[#dfe5ec]">Search immediately</div><div className="mt-1 text-[9.5px] text-[#768292]">Turn off to leave a monitored workflow you can search later.</div></div><Switch checked={searchNow} onCheckedChange={setSearchNow} aria-label="Search immediately" /></div>
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

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) { return <label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">{label}{children}{hint ? <span className="font-normal leading-4 text-[#697586]">{hint}</span> : null}</label> }

function ConfigSelect({ label, value, onChange, items }: { label: string; value: string; onChange: (value: string) => void; items: Array<{ id: string; label: string }> }) { return <Field label={label}><Select value={value} onValueChange={onChange}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="default">Use default</SelectItem>{items.map((item) => <SelectItem key={item.id} value={item.id}>{item.label}</SelectItem>)}</SelectContent></Select></Field> }

export function LiveMediaDetailScreen({ mediaId }: { mediaId: string }) {
  const router = useRouter()
  const [detail, setDetail] = React.useState<APIMediaDetail | null>(null)
  const [error, setError] = React.useState("")
  const [seasonSpec, setSeasonSpec] = React.useState("1:10")
  const [replacementConfirmation, setReplacementConfirmation] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const refresh = React.useCallback(async () => { try { setDetail(await loadMediaDetail(mediaId)); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load media") } }, [mediaId])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  const media = detail?.media ?? null
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
      toast.success(media.kind === "movie" ? "Upgrade workflow created" : "Season-pack workflow created"); await refresh()
    } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not create workflow") } finally { setBusy(false) }
  }
  async function remove() {
    setBusy(true)
    try { await recycleMedia(mediaId); toast.success("Media moved to the 30-day recycle workflow"); router.push("/media") } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not recycle media") } finally { setBusy(false) }
  }
  if (error) return <ErrorNotice error={error} />
  if (!media) return <Panel><div className="flex items-center gap-2 text-[11px] text-[#8390a1]"><LoaderCircle className="size-4 animate-spin" /> Loading media…</div></Panel>
  const hasAdoptedProtected = detail?.files.some((file) => file.origin === "adoption" && ["current", "recycle_planned", "retained", "restore_confirming", "restore_attention"].includes(file.state)) ?? false
  const selectedSeason = media.kind === "series" ? Number.parseInt(seasonSpec.split(":", 1)[0] ?? "", 10) : undefined
  const selectedVariant = media.kind === "movie" ? "default" : Number.isInteger(selectedSeason) ? `season:${String(selectedSeason).padStart(2, "0")}` : ""
  const hasAdoptedForVariant = detail?.files.some((file) => file.origin === "adoption" && file.state === "current" && file.variant === selectedVariant) ?? false
  const replacementConfirmed = !hasAdoptedForVariant || replacementConfirmation === "REPLACE ADOPTED FILE AFTER PLEX VERIFICATION"
  return <div className="space-y-4">
    <Panel><div className="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between"><div><div className="flex gap-2"><StatusChip tone="info">{media.kind === "movie" ? "Movie" : "Series"}</StatusChip>{(media.identities ?? []).map((identity) => <StatusChip key={`${identity.namespace}-${identity.value}`}>{identity.namespace.toUpperCase()} {identity.value}</StatusChip>)}</div><h2 className="mt-4 text-3xl font-[850] tracking-[-0.04em] text-white">{media.title}</h2><div className="mt-2 text-[11px] text-[#8692a2]">{media.year ?? "Year unavailable"}</div></div><div className="flex flex-wrap gap-2">{media.kind === "series" ? <Input className="mm-input w-36" value={seasonSpec} onChange={(event) => { setSeasonSpec(event.target.value); setReplacementConfirmation("") }} aria-label="Season and episode count" /> : null}<Button disabled={busy || !replacementConfirmed} onClick={() => void addWorkflow()}><Plus /> {media.kind === "movie" ? "Search upgrade" : "Add season"}</Button><AlertDialog><AlertDialogTrigger asChild><Button variant="outline" disabled={busy || hasAdoptedProtected}><Trash2 /> Recycle</Button></AlertDialogTrigger><AlertDialogContent className="border-[#303947] bg-[#10141a] text-white"><AlertDialogHeader><AlertDialogTitle>Recycle {media.title}?</AlertDialogTitle><AlertDialogDescription>Managed links move into 30-day retention. AltMount source files stay untouched. Active workflows and adopted real-file retention must be resolved first.</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>Keep media</AlertDialogCancel><AlertDialogAction onClick={() => void remove()}>Recycle links</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog></div></div></Panel>
    {hasAdoptedForVariant ? <Panel title="Confirm adopted-file replacement" subtitle={`The selected ${selectedVariant} currently contains a reviewed real file.`}><InlineNotice tone="warn" title="New media is verified first">The existing file is moved into 30-day retention only after the replacement is published and Plex confirms it. Physical deletion is never automatic and requires a second owner confirmation after retention.</InlineNotice><Field label="Exact confirmation phrase" hint="Type REPLACE ADOPTED FILE AFTER PLEX VERIFICATION"><Input className="mm-input mt-3" value={replacementConfirmation} onChange={(event) => setReplacementConfirmation(event.target.value)} autoComplete="off" /></Field></Panel> : hasAdoptedProtected ? <InlineNotice tone="info" title="Adopted real-file lifecycle remains protected">The selected scope has no current adopted file. Adopted files in another scope, displacement, or retained-file state still block whole-item recycle.</InlineNotice> : null}
    {media.kind === "series" ? <Panel className="!p-0" title="Season monitoring" subtitle="Persisted exact episode expectations; complete season packs remain one workflow and one AltMount job."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Season</TableHead><TableHead>Monitoring</TableHead><TableHead>Pack preference</TableHead><TableHead>Expected episodes</TableHead><TableHead>Latest workflow</TableHead></TableRow></TableHeader><TableBody>{(detail?.seasons ?? []).map((season) => <TableRow key={season.number}><TableCell className="font-semibold">Season {season.number}</TableCell><TableCell><StatusChip tone={season.monitored ? "good" : "neutral"}>{season.monitored ? "Monitored" : "Not monitored"}</StatusChip></TableCell><TableCell>{season.prefer_season_pack ? "Complete pack" : "No pack preference"}</TableCell><TableCell>{season.expected_episodes || "Not snapshotted"}</TableCell><TableCell><StatusChip tone={workflowTone(season.workflow_state ?? "")}>{season.workflow_state ? prettyState(season.workflow_state) : prettyState(season.state)}</StatusChip></TableCell></TableRow>)}</TableBody></Table></div>{detail?.seasons.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No seasons are monitored.</div> : null}</Panel> : null}
    <WorkflowTable workflows={detail?.workflows ?? []} empty="No workflows for this media." />
    <Panel className="!p-0" title="Managed files" subtitle="Plex-safe AltMount symlinks and reviewed adopted real files with persisted provenance."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Destination</TableHead><TableHead>Origin</TableHead><TableHead>Scope</TableHead><TableHead>Quality evidence</TableHead><TableHead>Plex</TableHead></TableRow></TableHeader><TableBody>{(detail?.files ?? []).map((file) => <TableRow key={file.id}><TableCell><Link href={`/files/${file.id}`} className="font-mono text-[9px] text-[#dfe5ec] hover:text-[#aebdff]">{file.destination_relative}</Link></TableCell><TableCell>{file.origin === "adoption" ? "Adopted real file" : `AltMount generation ${file.generation}`}</TableCell><TableCell>{file.variant}</TableCell><TableCell><StatusChip tone={file.quality_evidence_state === "unassessed" ? "warn" : "info"}>{prettyState(file.quality_evidence_state)}</StatusChip></TableCell><TableCell>{file.plex_confirmed ? "Confirmed" : "Not confirmed"}</TableCell></TableRow>)}</TableBody></Table></div>{detail?.files.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No managed files exist for this item.</div> : null}</Panel>
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
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{groups.length === 0 ? <EmptyPanel title="Recycle bin is empty" copy="Managed links and retained adopted upgrade files will appear here." /> : groups.map(([cycleId, mediaEntries]) => { const restorable = mediaEntries.find((entry) => entry.entry_kind === "link" && entry.purpose === "media_delete" && entry.state === "retained"); const adoptedRestorable = mediaEntries.find((entry) => entry.entry_kind === "real_file" && entry.purpose === "upgrade_displacement" && entry.state === "retained"); const hasRealFile = mediaEntries.some((entry) => entry.entry_kind === "real_file"); const purgeAt = mediaEntries.reduce((latest, entry) => entry.purge_after > latest ? entry.purge_after : latest, ""); const restorePhrase = restoreConfirmations[cycleId] ?? ""; return <Panel key={cycleId} title={mediaEntries[0].media_title} subtitle={hasRealFile ? "Adopted upgrade displacement · retained real file" : `${mediaEntries.length} managed link${mediaEntries.length === 1 ? "" : "s"} · AltMount source retained`} action={restorable ? <Button size="sm" disabled={busy === cycleId} onClick={() => void restore(restorable)}><ArchiveRestore /> Restore all links</Button> : undefined}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Original path</TableHead><TableHead>Kind</TableHead><TableHead>State</TableHead><TableHead>Retention</TableHead><TableHead>Owner action</TableHead></TableRow></TableHeader><TableBody>{mediaEntries.map((entry) => { const purgeDue = entry.entry_kind === "real_file" && entry.state === "retained" && Date.now() >= new Date(entry.purge_after).getTime(); const phrase = purgeConfirmations[entry.id] ?? ""; return <TableRow key={entry.id}><TableCell className="max-w-xl break-all font-mono text-[9px]">{entry.original_relative}</TableCell><TableCell>{entry.entry_kind === "real_file" ? "Adopted real file" : "Managed link"}</TableCell><TableCell><StatusChip tone={entry.state === "restored" ? "good" : entry.state === "purged" ? "neutral" : entry.state === "failed" ? "bad" : "warn"}>{prettyState(entry.state)}</StatusChip></TableCell><TableCell>{new Date(entry.purge_after).toLocaleDateString()}</TableCell><TableCell>{purgeDue ? <div className="flex min-w-72 flex-col gap-2"><Input className="mm-input" value={phrase} onChange={(event) => setPurgeConfirmations((current) => ({ ...current, [entry.id]: event.target.value }))} placeholder="PURGE RETAINED ADOPTED FILE" autoComplete="off" /><Button size="sm" variant="destructive" disabled={busy === entry.id || phrase !== "PURGE RETAINED ADOPTED FILE"} onClick={() => void purgeRealFile(entry)}><Trash2 /> Confirm physical purge</Button></div> : entry.entry_kind === "real_file" && entry.state === "retained" ? "Purge available after retention" : entry.state === "failed" ? "Owner recovery required" : "—"}</TableCell></TableRow> })}</TableBody></Table></div>{adoptedRestorable ? <div className="mt-4 grid gap-2 rounded-lg border border-[#384354] bg-[#151b23] p-3"><div className="text-[10px] font-semibold text-white">Restore the complete adopted-file cycle</div><div className="text-[9.5px] leading-4 text-[#8793a3]">The retained files move back first. The replacement links remain current until Plex confirms every restored path; only then are those links displaced.</div><Input className="mm-input" value={restorePhrase} onChange={(event) => setRestoreConfirmations((current) => ({ ...current, [cycleId]: event.target.value }))} placeholder="RESTORE RETAINED ADOPTED FILE AFTER PLEX VERIFICATION" autoComplete="off" /><Button size="sm" disabled={busy === cycleId || restorePhrase !== "RESTORE RETAINED ADOPTED FILE AFTER PLEX VERIFICATION"} onClick={() => void restoreAdopted(adoptedRestorable)}><ArchiveRestore /> Restore and verify with Plex</Button></div> : null}<div className="mt-3 text-[9.5px] text-[#778392]">Retention ends: {purgeAt ? new Date(purgeAt).toLocaleString() : "—"}. Link cycles purge automatically after 30 days. Adopted real files require an exact owner confirmation after retention and are never auto-deleted.</div></Panel> })}</div>
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
    <div className="grid gap-4 xl:grid-cols-[1fr_390px]"><Panel className="!p-0" title="Adoption review batches" subtitle="Discovery and plan approval never rename, move, publish, monitor, or search."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Source</TableHead><TableHead>Kind</TableHead><TableHead>Review</TableHead><TableHead>State</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{batches.map((batch) => <TableRow key={batch.id}><TableCell className="max-w-lg break-all font-mono text-[9px]">{batch.source_root}</TableCell><TableCell>{batch.media_kind}</TableCell><TableCell>{batch.approved_items} approved · {batch.rejected_items} rejected · {batch.attention_items + batch.pending_items} unresolved</TableCell><TableCell><StatusChip tone={batch.state === "approved" ? "good" : "warn"}>{prettyState(batch.state)}</StatusChip></TableCell><TableCell><Button size="sm" variant="outline" disabled={busy === batch.id} onClick={() => void openBatch(batch.id)}>Review</Button></TableCell></TableRow>)}</TableBody></Table></div>{batches.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No discovery plans exist.</div> : null}</Panel><Panel title="Start read-only discovery" subtitle="The source must be the selected configured library root or a subdirectory."><form className="grid gap-3" onSubmit={scan}><Field label="Library root"><Select value={rootID} onValueChange={changeRoot}><SelectTrigger className="mm-input w-full"><SelectValue placeholder="Select root" /></SelectTrigger><SelectContent>{roots.map((root) => <SelectItem key={root.id} value={root.id}>{root.name} · {root.media_kind}</SelectItem>)}</SelectContent></Select></Field><Field label="Source directory"><Input className="mm-input font-mono" value={sourceRoot} onChange={(event) => setSourceRoot(event.target.value)} required /></Field><Field label="Quality profile"><Select value={profileID} onValueChange={setProfileID}><SelectTrigger className="mm-input w-full"><SelectValue placeholder="Select profile" /></SelectTrigger><SelectContent>{compatibleProfiles.map((profile) => <SelectItem key={profile.id} value={profile.id}>{profile.name}</SelectItem>)}</SelectContent></Select></Field><ToggleRow label="Monitoring after future execution" checked={monitoring} onChange={(value) => { setMonitoring(value); if (!value) setSearchNow(false) }} /><ToggleRow label="Search after future execution" checked={searchNow} onChange={(value) => { setSearchNow(value); if (value) setMonitoring(true) }} /><Button type="submit" disabled={!rootID || !profileID || busy === "scan"}><Search /> Read-only scan and review</Button></form><InlineNotice tone="info" title="Bounded discovery">The scanner reads directory entries only, never follows symlinks, and stops above 50,000 files.</InlineNotice></Panel></div>
    {detail ? <Panel title={`Review plan · ${detail.media_kind}`} subtitle={`${detail.source_root} · ${detail.state === "review" ? "approval records intent only" : "filesystem, Plex, and reviewed monitoring outcomes are durably journaled"}.`} action={detail.state === "review" ? <Button disabled={busy === "approve" || detail.approved_items === 0 || detail.attention_items + detail.pending_items > 0} onClick={() => void approvePlan()}>Approve reviewed plan · no execution</Button> : undefined}><div className="space-y-3">{(detail.items ?? []).map((item) => <AdoptionItemReview key={item.id} batch={detail} item={item} media={media.filter((entry) => entry.kind === detail.media_kind)} roots={roots.filter((entry) => entry.media_kind === detail.media_kind)} profiles={profiles.filter((entry) => entry.media_kind === detail.media_kind)} disabled={detail.state !== "review"} onSaved={() => reloadBatch(detail.id)} />)}{detail.items?.length === 0 ? <div className="py-8 text-center text-[11px] text-[#7f8a9a]">The read-only scan found no supported video files.</div> : null}{detail.state === "approved" ? <div className="rounded-xl border border-[#9d7938]/30 bg-[#1a150c] p-4"><div className="text-[11px] font-bold text-[#e4c17a]">Irreversible live-action gate</div><div className="mt-2 text-[9.5px] leading-5 text-[#b7a27c]">Execution moves up to 25 reviewed files and recognized companions to their selected roots. Cross-root files are copied to an atomic temporary path, content-verified, and only then removed from the source. Unknown companions remain untouched. Every operation is journaled and rolled back on filesystem, bounded media-info, or Plex verification failure. Reviewed monitoring is applied only after Plex confirmation; an immediate search creates one paced workflow per exact scope.</div><Input className="mm-input mt-3 font-mono" value={executionConfirmation} onChange={(event) => setExecutionConfirmation(event.target.value)} placeholder="EXECUTE REVIEWED ADOPTION" />{detail.search_immediately ? <Input className="mm-input mt-2 font-mono" value={adoptionReplacementConfirmation} onChange={(event) => setAdoptionReplacementConfirmation(event.target.value)} placeholder="REPLACE ADOPTED FILE AFTER PLEX VERIFICATION" autoComplete="off" /> : null}<Button className="mt-3" variant="destructive" disabled={busy === "execute" || executionConfirmation !== "EXECUTE REVIEWED ADOPTION" || detail.search_immediately && adoptionReplacementConfirmation !== "REPLACE ADOPTED FILE AFTER PLEX VERIFICATION" || detail.approved_items > 25} onClick={() => void executePlan()}>Execute reviewed adoption</Button></div> : null}{detail.state === "executing" ? <InlineNotice tone="warn" title="Execution in progress">Filesystem operations and bounded media-info evidence are journaled. Completion requires Plex to confirm each exact destination path and verified provider identity before monitoring or search is applied.</InlineNotice> : null}{detail.state === "complete" ? <InlineNotice tone="info" title="Adoption verified">Every approved item has durable current-file path, fingerprint, exact scope, target profile, media-info tracks/facts, and Plex identity provenance. {detail.monitoring_enabled ? detail.search_immediately ? "Reviewed monitoring was applied and its fingerprint-authorized search was queued." : "Reviewed monitoring was applied without starting a search." : "Monitoring was intentionally left unchanged."}</InlineNotice> : null}{detail.state === "failed" ? <InlineNotice tone="bad" title="Adoption failed">The journal records the scoped failure and rollback outcome. Monitoring and search are not applied before successful Plex confirmation.</InlineNotice> : null}</div></Panel> : null}
  </div>
}

function AdoptionItemReview({ batch, item, media, roots, profiles, disabled, onSaved }: { batch: APIAdoptionBatch; item: APIAdoptionItem; media: APIMedia[]; roots: APILibraryRoot[]; profiles: APIQualityProfile[]; disabled: boolean; onSaved: () => Promise<void> }) {
  const [mediaID, setMediaID] = React.useState(item.media_id ?? "")
  const [rootID, setRootID] = React.useState(item.root_id ?? batch.default_root_id)
  const [profileID, setProfileID] = React.useState(item.quality_profile_id ?? batch.default_quality_profile_id)
  const [proposed, setProposed] = React.useState(item.proposed_relative ?? item.original_relative)
  const [seasonNumber, setSeasonNumber] = React.useState(item.variant?.startsWith("season:") ? String(Number(item.variant.slice(7))) : "1")
  const [busy, setBusy] = React.useState(false)
  const season = Number(seasonNumber)
  const validSeason = batch.media_kind === "movie" || Number.isInteger(season) && season >= 0 && season <= 99
  const variant = batch.media_kind === "movie" ? "default" : `season:${String(season).padStart(2, "0")}`
  async function save(state: "approved" | "rejected") { setBusy(true); try { await reviewAdoptionItem(batch.id, item.id, state === "rejected" ? { review_state: state } : { review_state: state, media_id: mediaID, root_id: rootID, quality_profile_id: profileID, proposed_relative: proposed, variant, monitoring_enabled: batch.monitoring_enabled, search_immediately: batch.search_immediately }); toast.success(`Adoption item ${state}`); await onSaved() } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not save review") } finally { setBusy(false) } }
  return <div className="rounded-xl border border-white/[0.07] bg-[#0c1015] p-3"><div className="flex flex-col gap-2 lg:flex-row lg:items-start lg:justify-between"><div className="min-w-0"><div className="break-all font-mono text-[9.5px] text-[#dce2e9]">{item.original_relative}</div><div className="mt-1 text-[9px] text-[#748090]">{item.recognized_companions.length} recognized companion(s) · {item.unknown_companions.length} unknown companion(s){item.variant ? ` · ${item.variant}` : ""}</div></div><div className="flex gap-1"><StatusChip tone={item.review_state === "approved" ? "good" : item.review_state === "rejected" ? "neutral" : "warn"}>{prettyState(item.review_state)}</StatusChip>{item.execution_state !== "pending" ? <StatusChip tone={item.execution_state === "complete" ? "good" : item.execution_state === "failed" ? "bad" : "info"}>{prettyState(item.execution_state)}</StatusChip> : null}</div></div>{!disabled ? <div className="mt-3 grid gap-2 lg:grid-cols-[1fr_1fr_1fr_1.5fr_auto_auto]"><select className="mm-input h-10 rounded-md px-2 text-[9px]" value={mediaID} onChange={(event) => setMediaID(event.target.value)}><option value="">Match catalog item</option>{media.map((entry) => <option key={entry.id} value={entry.id}>{entry.title} {entry.year ? `(${entry.year})` : ""}</option>)}</select><select className="mm-input h-10 rounded-md px-2 text-[9px]" value={rootID} onChange={(event) => setRootID(event.target.value)}>{roots.map((entry) => <option key={entry.id} value={entry.id}>{entry.name}</option>)}</select><select className="mm-input h-10 rounded-md px-2 text-[9px]" value={profileID} onChange={(event) => setProfileID(event.target.value)}>{profiles.map((entry) => <option key={entry.id} value={entry.id}>{entry.name}</option>)}</select><Input className="mm-input font-mono text-[9px]" value={proposed} onChange={(event) => setProposed(event.target.value)} aria-label="Proposed relative path" />{batch.media_kind === "series" ? <Input className="mm-input w-20" type="number" min={0} max={99} value={seasonNumber} onChange={(event) => setSeasonNumber(event.target.value)} aria-label="Exact season number" /> : <div className="self-center text-[9px] text-[#748090]">Movie</div>}<div className="flex gap-1"><Button size="sm" disabled={busy || !mediaID || !proposed || !validSeason} onClick={() => void save("approved")}>Approve</Button><Button size="sm" variant="ghost" disabled={busy} onClick={() => void save("rejected")}>Reject</Button></div></div> : null}</div>
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
  return <div className="grid gap-4 xl:grid-cols-[1fr_390px]">{error ? <div className="xl:col-span-2"><ErrorNotice error={error} /></div> : null}<Panel title="Manual collections" subtitle="Persisted groupings only; no unsupported smart-rule matches are claimed.">{collections.length === 0 ? <div className="py-10 text-center text-[11px] text-[#7f8a9a]">No collections exist.</div> : <div className="grid gap-3 md:grid-cols-2">{collections.map((collection) => <div key={collection.id} className="rounded-xl border border-white/[0.07] bg-[#0c1015] p-3"><div className="flex items-start justify-between gap-3"><div><div className="text-[11px] font-bold text-[#e5eaf0]">{collection.name}</div><div className="mt-1 text-[9px] text-[#748090]">{collection.members.length} member(s)</div></div><Button size="sm" variant="ghost" disabled={busy === collection.id} onClick={() => void remove(collection.id)}><Trash2 /> Delete</Button></div>{collection.description ? <div className="mt-2 text-[9.5px] text-[#8a95a3]">{collection.description}</div> : null}<div className="mt-3 flex flex-wrap gap-1">{collection.members.map((item) => <StatusChip key={item.id}>{item.title}{item.year ? ` (${item.year})` : ""}</StatusChip>)}</div></div>)}</div>}</Panel><Panel title="Create with preview" subtitle="Select persisted catalog records, preview the exact server result, then save."><form className="grid gap-3" onSubmit={save}><Field label="Name"><Input className="mm-input" maxLength={200} value={name} onChange={(event) => setName(event.target.value)} required /></Field><Field label="Description"><Textarea className="mm-input min-h-20" maxLength={2000} value={description} onChange={(event) => setDescription(event.target.value)} /></Field><div className="max-h-64 space-y-1 overflow-y-auto rounded-xl border border-white/[0.07] bg-[#0b0f14] p-2">{media.map((item) => <label key={item.id} className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-[9.5px] text-[#aeb7c3] hover:bg-white/[0.04]"><Checkbox checked={selected.includes(item.id)} onCheckedChange={(value) => toggle(item.id, value === true)} />{item.title} {item.year ? `(${item.year})` : ""}<span className="ml-auto text-[#687586]">{item.kind}</span></label>)}{media.length === 0 ? <div className="p-4 text-center text-[9.5px] text-[#748090]">Add catalog media before creating a collection.</div> : null}</div><Button type="button" variant="outline" disabled={selected.length === 0 || busy === "preview"} onClick={() => void runPreview()}><Search /> Preview {selected.length} member(s)</Button>{preview ? <InlineNotice tone="info" title="Server preview">{preview.map((item) => item.title).join(" · ")}</InlineNotice> : null}<Button type="submit" disabled={!preview || !name.trim() || busy === "save"}><Plus /> Save reviewed collection</Button></form></Panel></div>
}

export function LiveConnectionsScreen() {
  const [items, setItems] = React.useState<APIIntegration[]>([])
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
  const refresh = React.useCallback(async () => { try { setItems(await loadIntegrations()); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load integrations") } }, [])
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
    <Panel className="!p-0" title="Connected services" subtitle="Secrets remain environment references; values never return to the browser."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Type</TableHead><TableHead>Default</TableHead><TableHead>Secret reference</TableHead><TableHead>State</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>{items.map((item) => { const awaiting = item.kind === "newznab" && (!item.enabled || item.config.mode_verified !== true); return <TableRow key={item.id}><TableCell className="font-semibold text-[#e5eaf0]">{item.name}<div className="mt-1 font-mono text-[8px] text-[#667284]">{item.id}</div></TableCell><TableCell>{item.kind}</TableCell><TableCell>{item.is_default ? "Yes" : "No"}</TableCell><TableCell className="font-mono text-[9px]">{item.secret_ref ?? "—"}</TableCell><TableCell><StatusChip tone={item.enabled ? "good" : awaiting ? "warn" : "neutral"}>{item.enabled ? "Enabled" : awaiting ? "Awaiting validation" : "Disabled"}</StatusChip></TableCell><TableCell>{awaiting ? <Button size="sm" variant="outline" disabled={commissioning === item.id} onClick={() => void commission(item)}>{commissioning === item.id ? <LoaderCircle className="animate-spin" /> : <Activity />} Validate & enable</Button> : "—"}</TableCell></TableRow> })}</TableBody></Table></div>{items.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No integrations configured.</div> : null}</Panel>
    <Panel title="Add connection" subtitle="Create the database record after its secret exists in .env.integrations.">{error ? <div className="mb-4"><ErrorNotice error={error} /></div> : null}<form className="grid gap-3" onSubmit={submit}><Field label="Connection type"><Select value={kind} onValueChange={changeConnectionKind}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="plex">Plex</SelectItem><SelectItem value="altmount">AltMount</SelectItem><SelectItem value="newznab">Newznab indexer</SelectItem><SelectItem value="seerr">Seerr record</SelectItem></SelectContent></Select></Field><Field label="Name"><Input className="mm-input" value={name} onChange={(event) => setName(event.target.value)} required /></Field><Field label="Base URL"><Input className="mm-input" value={baseURL} onChange={(event) => setBaseURL(event.target.value)} placeholder="https://service.example" required /></Field><Field label="Secret environment name"><Input className="mm-input font-mono" value={secretRef} onChange={(event) => setSecretRef(event.target.value)} required /></Field>{kind === "altmount" ? <div className="grid grid-cols-2 gap-2"><Field label="Reported root"><Input className="mm-input" value={reportedRoot} onChange={(event) => setReportedRoot(event.target.value)} /></Field><Field label="Read-only root"><Input className="mm-input" value={localRoot} onChange={(event) => setLocalRoot(event.target.value)} /></Field></div> : null}<ToggleRow label="Allow plain HTTP" checked={allowHTTP} onChange={setAllowHTTP} /><ToggleRow label="Make default" checked={isDefault} onChange={setIsDefault} /><Button disabled={busy} type="submit"><Plus /> Save connection</Button></form><div className="mt-3 text-[9.5px] leading-5 text-[#707c8c]">For Seerr: save the record, use its UUID as <span className="font-mono">SEERR_INSTANCE_ID</span>, then restart with all four Seerr variables set.</div></Panel>
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
  return <div className="grid gap-4 xl:grid-cols-[1fr_380px]"><Panel className="!p-0" title="Library roots" subtitle="Multiple roots with a default and per-item override."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Kind</TableHead><TableHead>Destination</TableHead><TableHead>Plex section</TableHead><TableHead>Default</TableHead></TableRow></TableHeader><TableBody>{roots.map((root) => <TableRow key={root.id}><TableCell className="font-semibold text-[#e5eaf0]">{root.name}</TableCell><TableCell>{root.media_kind}</TableCell><TableCell className="font-mono text-[9px]">{root.destination_root}</TableCell><TableCell>{root.plex_library_id}</TableCell><TableCell>{root.is_default ? "Yes" : "No"}</TableCell></TableRow>)}</TableBody></Table></div></Panel><Panel title="Add Plex-backed root" subtitle="Media Manager needs write access; Plex receives this library read-only.">{error ? <div className="mb-4"><ErrorNotice error={error} /></div> : null}{plex.length === 0 ? <div className="mb-4"><InlineNotice tone="warn" title="Plex first">Create an enabled Plex connection before a root.</InlineNotice></div> : null}<form className="grid gap-3" onSubmit={submit}><Field label="Media kind"><Select value={kind} onValueChange={(value) => changeRootKind(value as "movie" | "series")}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="movie">Movie</SelectItem><SelectItem value="series">TV series</SelectItem></SelectContent></Select></Field><Field label="Name"><Input className="mm-input" value={name} onChange={(event) => setName(event.target.value)} /></Field><Field label="Media Manager destination"><Input className="mm-input font-mono" value={destination} onChange={(event) => setDestination(event.target.value)} /></Field><Field label="Path visible to Plex"><Input className="mm-input font-mono" value={plexRoot} onChange={(event) => setPlexRoot(event.target.value)} /></Field><Field label="AltMount path visible to Plex"><Input className="mm-input font-mono" value={sourceRoot} onChange={(event) => setSourceRoot(event.target.value)} /></Field><Field label="Plex connection"><Select value={plexID} onValueChange={setPlexID}><SelectTrigger className="mm-input w-full"><SelectValue placeholder="Choose Plex" /></SelectTrigger><SelectContent>{plex.map((item) => <SelectItem key={item.id} value={item.id}>{item.name}</SelectItem>)}</SelectContent></Select></Field><Field label="Plex library section ID"><Input className="mm-input" inputMode="numeric" value={libraryID} onChange={(event) => setLibraryID(event.target.value)} placeholder="1" /></Field><ToggleRow label="Make default" checked={isDefault} onChange={setIsDefault} /><Button disabled={!plexID || !libraryID} type="submit"><FolderTree /> Save root</Button></form></Panel></div>
}

export function LiveProfilesScreen() {
  const [profiles, setProfiles] = React.useState<APIQualityProfile[]>([])
  const [kind, setKind] = React.useState<"movie" | "series">("movie")
  const [name, setName] = React.useState("UHD")
  const [resolution, setResolution] = React.useState("2160p")
  const [source, setSource] = React.useState("webdl")
  const [upgrades, setUpgrades] = React.useState(true)
  const [isDefault, setIsDefault] = React.useState(true)
  const [error, setError] = React.useState("")
  const refresh = React.useCallback(async () => { try { setProfiles(await loadQualityProfiles()); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load profiles") } }, [])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  async function submit(event: React.FormEvent) { event.preventDefault(); try { await createQualityProfile({ media_kind: kind, name, policy_version: 1, policy: { rules: [{ name: "Required resolution", fact: "resolution", allowed_values: [resolution], kind: "required", weight: 0, unknown: "hold" }, { name: "Preferred source", fact: "source", allowed_values: [source], kind: "preferred", weight: 100, unknown: "allow" }] }, upgrades_enabled: upgrades, is_default: isDefault, enabled: true }); toast.success("Quality profile saved"); await refresh() } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not save profile") } }
  return <div className="grid gap-4 xl:grid-cols-[1fr_380px]"><Panel className="!p-0" title="Quality profiles" subtitle="Hard requirements reject first; preferences rank eligible candidates."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Kind</TableHead><TableHead>Upgrades</TableHead><TableHead>Default</TableHead><TableHead>State</TableHead></TableRow></TableHeader><TableBody>{profiles.map((profile) => <TableRow key={profile.id}><TableCell className="font-semibold text-[#e5eaf0]">{profile.name}</TableCell><TableCell>{profile.media_kind}</TableCell><TableCell>{profile.upgrades_enabled ? "On" : "Off"}</TableCell><TableCell>{profile.is_default ? "Yes" : "No"}</TableCell><TableCell><StatusChip tone={profile.enabled ? "good" : "neutral"}>{profile.enabled ? "Enabled" : "Disabled"}</StatusChip></TableCell></TableRow>)}</TableBody></Table></div></Panel><Panel title="Create profile" subtitle="A safe starter profile with explicit resolution and preferred source.">{error ? <div className="mb-4"><ErrorNotice error={error} /></div> : null}<form className="grid gap-3" onSubmit={submit}><Field label="Media kind"><Select value={kind} onValueChange={(value) => setKind(value as "movie" | "series")}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="movie">Movie</SelectItem><SelectItem value="series">TV series</SelectItem></SelectContent></Select></Field><Field label="Name"><Input className="mm-input" value={name} onChange={(event) => setName(event.target.value)} /></Field><Field label="Required resolution"><Select value={resolution} onValueChange={setResolution}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="2160p">2160p</SelectItem><SelectItem value="1080p">1080p</SelectItem><SelectItem value="720p">720p</SelectItem></SelectContent></Select></Field><Field label="Preferred source"><Select value={source} onValueChange={setSource}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="remux">Remux</SelectItem><SelectItem value="bluray">Blu-ray</SelectItem><SelectItem value="webdl">WEB-DL</SelectItem><SelectItem value="webrip">WEBRip</SelectItem></SelectContent></Select></Field><ToggleRow label="Allow upgrades" checked={upgrades} onChange={setUpgrades} /><ToggleRow label="Make default" checked={isDefault} onChange={setIsDefault} /><Button type="submit"><Settings2 /> Save profile</Button></form></Panel></div>
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
        {busy ? <div className="flex items-center gap-2 text-[10px] text-[#8390a1]"><LoaderCircle className="size-4 animate-spin" /> Reading backup…</div> : <div className="flex items-center gap-2 text-[10px] text-[#748092]"><Upload className="size-4" /> Choose a backup above to begin a read-only review.</div>}
      </div>
    </Panel>
    {result ? <>
      <Panel className="!p-0" title="Indexer review" subtitle="Only detected Usenet/Newznab records can be approved; torrent and incomplete records remain held.">
        <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead className="w-12">Use</TableHead><TableHead>Name</TableHead><TableHead>Implementation</TableHead><TableHead>Endpoint</TableHead><TableHead>State</TableHead></TableRow></TableHeader><TableBody>{result.candidates.map((candidate) => <TableRow key={candidate.id}><TableCell><Checkbox checked={selected.has(candidate.id)} disabled={candidate.review_state !== "pending" || result.state === "complete"} onCheckedChange={(checked) => toggle(candidate.id, checked === true)} aria-label={`Import ${candidate.name}`} /></TableCell><TableCell className="font-semibold text-[#e5eaf0]">{candidate.name}</TableCell><TableCell>{candidate.implementation}</TableCell><TableCell className="max-w-md break-all font-mono text-[9px]">{candidate.base_url || "—"}</TableCell><TableCell><StatusChip tone={candidate.review_state === "approved" ? "good" : candidate.review_state === "pending" ? "info" : "warn"}>{prettyState(candidate.review_state)}</StatusChip>{candidate.attention_detail ? <div className="mt-1 max-w-sm text-[9px] text-[#b6a06f]">{candidate.attention_detail}</div> : null}</TableCell></TableRow>)}</TableBody></Table></div>
      </Panel>
      {result.state !== "complete" && pending.length > 0 ? <Panel title="Save the imported API keys" subtitle="Secrets are deliberately not stored in the review database or returned after this page.">
        <InlineNotice tone="warn" title="Required before approval">Copy these lines into <span className="font-mono">.env.integrations</span>, then keep that file private. Approval creates Media Manager indexers that reference these environment names.</InlineNotice>
        <div className="mt-3 grid gap-2"><Textarea className="min-h-32 font-mono text-[10px]" readOnly spellCheck={false} value={secretLines || "No usable API keys were found."} /><Button variant="outline" disabled={!secretLines} onClick={() => void navigator.clipboard.writeText(secretLines).then(() => toast.success("Secret lines copied")).catch(() => toast.error("Clipboard access was unavailable"))}><Copy /> Copy secret lines</Button></div>
        <label className="mt-4 flex items-start gap-2 rounded-lg border border-white/[0.07] bg-[#0b0f14] p-3 text-[10px] leading-5 text-[#9aa5b3]"><Checkbox checked={secretsSaved} onCheckedChange={(checked) => setSecretsSaved(checked === true)} />I saved the selected secret lines in <span className="font-mono">.env.integrations</span>.</label>
        <Button className="mt-4" disabled={busy || selected.size === 0 || !secretsSaved} onClick={() => void approve()}><Plus /> Approve {selected.size} indexer{selected.size === 1 ? "" : "s"}</Button>
      </Panel> : null}
      {result.state === "complete" ? <InlineNotice tone="info" title="Import review complete">Approved indexers are staged disabled in Connections. Run <span className="font-mono">docker compose up -d --force-recreate api</span> after changing <span className="font-mono">.env.integrations</span>, then validate each indexer. Validation uses only paced CAPS and one negative search.</InlineNotice> : null}
    </> : null}
  </div>
}

function ToggleRow({ label, checked, onChange }: { label: string; checked: boolean; onChange: (value: boolean) => void }) { return <div className="flex items-center justify-between rounded-lg border border-white/[0.07] bg-[#0b0f14] px-3 py-2.5"><span className="text-[10px] font-semibold text-[#a7b0bc]">{label}</span><Switch checked={checked} onCheckedChange={onChange} aria-label={label} /></div> }

export function LiveHealthScreen() {
  const [status, setStatus] = React.useState<"loading" | "ready" | "failed">("loading")
  const [detail, setDetail] = React.useState("")
  React.useEffect(() => { fetch("/health/ready", { credentials: "same-origin", headers: { Accept: "application/json" } }).then(async (response) => { if (!response.ok) throw new Error(`Readiness returned HTTP ${response.status}`); const body = await response.json() as { data: { since?: string } }; setDetail(body.data.since ? `Ready since ${new Date(body.data.since).toLocaleString()}` : "Database and schema are ready"); setStatus("ready") }).catch((reason: unknown) => { setDetail(reason instanceof Error ? reason.message : "Readiness failed"); setStatus("failed") }) }, [])
  return <div className="grid gap-4 sm:grid-cols-3"><MetricCard label="API" value={status === "loading" ? "Checking" : status === "ready" ? "Ready" : "Unavailable"} note={detail || "Checking live readiness"} tone={status === "ready" ? "good" : status === "failed" ? "bad" : "neutral"} icon={HeartPulse} /><MetricCard label="Database" value={status === "ready" ? "Ready" : "—"} note="PostgreSQL schema gate" tone={status === "ready" ? "good" : "neutral"} icon={Database} /><MetricCard label="Owner surface" value="Protected" note="Gateway login + server-side API token" tone="good" icon={ServerCog} /></div>
}

export function LiveUnavailableScreen() { return <EmptyPanel title="This surface is not connected yet" copy="The live owner build exposes catalog, add, workflows, recycle, roots, profiles, connections, Prowlarr backup review, and health. Development-only concept data is never shown here." href="/" action="Return to overview" /> }
