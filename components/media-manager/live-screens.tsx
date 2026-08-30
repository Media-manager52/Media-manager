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
  cancelWorkflow,
	commissionIntegration,
  createExistingWorkflow,
  createIntegration,
  createLibraryRoot,
  createMedia,
  createQualityProfile,
  loadIntegrations,
  loadLibraryRoots,
  loadMedia,
  loadMediaDetail,
  loadQualityProfiles,
  loadRecycle,
  loadWorkflows,
  recycleMedia,
  restoreRecycle,
  searchWorkflow,
  uploadProwlarrBackup,
  type APIIntegration,
  type APILibraryRoot,
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
      <MetricCard label="Recycle entries" value={String(recycle.filter((entry) => entry.state !== "purged" && entry.state !== "restored").length)} note="30-day link retention" icon={ArchiveRestore} />
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
  const refresh = React.useCallback(async () => {
    try { setWorkflows(await loadWorkflows(false)); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load workflows") }
  }, [])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); const timer = window.setInterval(() => void refresh(), 8000); return () => { window.clearTimeout(initial); window.clearInterval(timer) } }, [refresh])
  async function act(id: string, action: "search" | "cancel") {
    setBusy(id)
    try { if (action === "search") await searchWorkflow(id); else await cancelWorkflow(id); toast.success(action === "search" ? "Search queued" : "Workflow cancelled"); await refresh() } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Operation failed") } finally { setBusy("") }
  }
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}<Panel className="!p-0" title="Workflow activity" subtitle="Real persisted state; no invented percentages." action={<Button variant="outline" size="sm" onClick={() => void refresh()}><RefreshCw /> Refresh</Button>}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Media</TableHead><TableHead>Scope</TableHead><TableHead>State</TableHead><TableHead>Updated</TableHead><TableHead>Actions</TableHead></TableRow></TableHeader><TableBody>{workflows.map((workflow) => <TableRow key={workflow.id}><TableCell><Link className="font-semibold text-[#e5eaf0] hover:text-[#aebdff]" href={workflow.media_kind === "movie" ? `/media/movies/${workflow.media_id}` : `/media/series/${workflow.media_id}`}>{workflow.title}</Link><div className="mt-1 text-[9px] text-[#727e8e]">{workflow.year ?? "—"}</div></TableCell><TableCell className="font-mono text-[9px]">{workflow.variant}</TableCell><TableCell><StatusChip tone={workflowTone(workflow.state)}>{prettyState(workflow.state)}</StatusChip>{workflow.attention_detail ? <div className="mt-1 max-w-sm text-[9px] text-[#d6b36d]">{workflow.attention_detail}</div> : null}</TableCell><TableCell>{timeAgo(workflow.updated_at)}</TableCell><TableCell><div className="flex gap-1"><Button size="sm" variant="outline" disabled={busy === workflow.id || !["monitored", "waiting", "needs_attention"].includes(workflow.state)} onClick={() => void act(workflow.id, "search")}><Search /> Search</Button><Button size="sm" variant="ghost" disabled={busy === workflow.id || !["monitored", "waiting", "searching", "held", "blocked", "needs_attention"].includes(workflow.state)} onClick={() => void act(workflow.id, "cancel")}>Cancel</Button></div></TableCell></TableRow>)}</TableBody></Table></div>{workflows.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No workflows yet.</div> : null}</Panel></div>
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

export function LiveDiscoverScreen() {
  const router = useRouter()
  const [kind, setKind] = React.useState<"movie" | "series">("movie")
  const [title, setTitle] = React.useState("")
  const [year, setYear] = React.useState("")
  const [provider, setProvider] = React.useState("tmdb")
  const [providerID, setProviderID] = React.useState("")
  const [seasonSpec, setSeasonSpec] = React.useState("1:10")
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
  function changeKind(value: "movie" | "series") { setKind(value); setProvider(value === "movie" ? "tmdb" : "tvdb"); setRootID("default"); setProfileID("default") }
  const compatibleRoots = roots.filter((root) => root.media_kind === kind && root.enabled)
  const compatibleProfiles = profiles.filter((profile) => profile.media_kind === kind && profile.enabled)
  const configured = compatibleRoots.length > 0 && compatibleProfiles.length > 0 && clients.length > 0 && (!searchNow || indexers.length > 0)
  async function submit(event: React.FormEvent) {
    event.preventDefault(); setError(""); setSubmitting(true)
    try {
      const parsedYear = Number(year)
      if (!title.trim() || parsedYear < 1800 || parsedYear > 9999 || !providerID.trim()) throw new Error("Title, valid year, and provider ID are required")
      const seasons = kind === "series" ? parseSeasonSpec(seasonSpec).map((season) => ({ ...season, search_immediately: searchNow })) : undefined
      await createMedia({ kind, title: title.trim(), year: parsedYear, identities: [{ namespace: provider, value: providerID.trim(), state: "verified", source: "manual" }], search_immediately: searchNow, ...(rootID !== "default" ? { root_id: rootID } : {}), ...(profileID !== "default" ? { quality_profile_id: profileID } : {}), ...(clientID !== "default" ? { download_client_id: clientID } : {}), ...(seasons ? { seasons } : {}) })
      toast.success(kind === "movie" ? "Movie workflow created" : "Season-pack workflows created")
      router.push("/activity/queue")
    } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not add media") } finally { setSubmitting(false) }
  }
  return <div className="grid gap-4 xl:grid-cols-[1fr_340px]">
    <Panel title="Add directly to Media Manager" subtitle="A manual add creates durable workflow intent. Series snapshots become one preferred pack job per season.">
      {error ? <div className="mb-4"><ErrorNotice error={error} /></div> : null}
      {!loading && !configured ? <div className="mb-4"><InlineNotice tone="warn" title="Finish setup first">You need an enabled Plex-backed {kind} root, {kind} quality profile, AltMount client{searchNow ? ", and at least one Newznab indexer" : ""}. <Link className="underline" href="/settings/connections">Open setup</Link>.</InlineNotice></div> : null}
      <form className="grid gap-4" onSubmit={submit}>
        <div className="grid gap-3 sm:grid-cols-2"><Field label="Media type"><Select value={kind} onValueChange={(value) => changeKind(value as "movie" | "series")}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="movie">Movie</SelectItem><SelectItem value="series">TV series</SelectItem></SelectContent></Select></Field><Field label="Release year"><Input className="mm-input" inputMode="numeric" value={year} onChange={(event) => setYear(event.target.value)} placeholder="2026" required /></Field></div>
        <Field label="Title"><Input className="mm-input" value={title} onChange={(event) => setTitle(event.target.value)} placeholder={kind === "movie" ? "Movie title" : "Series title"} required /></Field>
        <div className="grid gap-3 sm:grid-cols-2"><Field label="Verified provider"><Select value={provider} onValueChange={setProvider}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent>{kind === "movie" ? <><SelectItem value="tmdb">TMDB</SelectItem><SelectItem value="imdb">IMDb</SelectItem></> : <><SelectItem value="tvdb">TVDB</SelectItem><SelectItem value="tmdb">TMDB</SelectItem></>}</SelectContent></Select></Field><Field label={`${provider.toUpperCase()} ID`}><Input className="mm-input" value={providerID} onChange={(event) => setProviderID(event.target.value)} placeholder={provider === "imdb" ? "tt1234567" : "12345"} required /></Field></div>
        {kind === "series" ? <Field label="Exact seasons and episode counts" hint="Format season:count. Example: 1:10, 2:8 creates two immutable episode snapshots."><Input className="mm-input" value={seasonSpec} onChange={(event) => setSeasonSpec(event.target.value)} required /></Field> : null}
        <div className="grid gap-3 sm:grid-cols-3"><ConfigSelect label="Library root" value={rootID} onChange={setRootID} items={compatibleRoots.map((item) => ({ id: item.id, label: `${item.name}${item.is_default ? " · default" : ""}` }))} /><ConfigSelect label="Quality profile" value={profileID} onChange={setProfileID} items={compatibleProfiles.map((item) => ({ id: item.id, label: `${item.name}${item.is_default ? " · default" : ""}` }))} /><ConfigSelect label="Download client" value={clientID} onChange={setClientID} items={clients.map((item) => ({ id: item.id, label: `${item.name}${item.is_default ? " · default" : ""}` }))} /></div>
        <div className="flex items-center justify-between rounded-xl border border-white/[0.07] bg-[#0b0f14] p-3"><div><div className="text-[11px] font-bold text-[#dfe5ec]">Search immediately</div><div className="mt-1 text-[9.5px] text-[#768292]">Turn off to leave a monitored workflow you can search later.</div></div><Switch checked={searchNow} onCheckedChange={setSearchNow} aria-label="Search immediately" /></div>
        <Button disabled={loading || !configured || submitting} type="submit">{submitting ? <LoaderCircle className="animate-spin" /> : <Plus />} {searchNow ? "Add and search" : "Add without search"}</Button>
      </form>
    </Panel>
    <Panel title="Identity and season safety" subtitle="No single provider ID is required for every media type." className="h-fit">
      <KeyValue label="Movies" value="TMDB or IMDb" /><KeyValue label="Series" value="TVDB or TMDB" /><KeyValue label="Season preference" value="Complete pack" /><KeyValue label="AltMount jobs" value="One per season" /><KeyValue label="Publication" value="Exact coverage + Plex" />
      <InlineNotice tone="info" title="Best input">Seerr intake supplies canonical metadata and exact episode snapshots automatically. Manual input is available when you already know the identity and season shape.</InlineNotice>
    </Panel>
  </div>
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) { return <label className="grid gap-1.5 text-[10px] font-semibold text-[#929dab]">{label}{children}{hint ? <span className="font-normal leading-4 text-[#697586]">{hint}</span> : null}</label> }

function ConfigSelect({ label, value, onChange, items }: { label: string; value: string; onChange: (value: string) => void; items: Array<{ id: string; label: string }> }) { return <Field label={label}><Select value={value} onValueChange={onChange}><SelectTrigger className="mm-input w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="default">Use default</SelectItem>{items.map((item) => <SelectItem key={item.id} value={item.id}>{item.label}</SelectItem>)}</SelectContent></Select></Field> }

export function LiveMediaDetailScreen({ mediaId }: { mediaId: string }) {
  const router = useRouter()
  const [detail, setDetail] = React.useState<APIMediaDetail | null>(null)
  const [error, setError] = React.useState("")
  const [seasonSpec, setSeasonSpec] = React.useState("1:10")
  const [busy, setBusy] = React.useState(false)
  const refresh = React.useCallback(async () => { try { setDetail(await loadMediaDetail(mediaId)); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load media") } }, [mediaId])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  const media = detail?.media ?? null
  async function addWorkflow() {
    if (!media) return
    setBusy(true)
    try {
      if (media.kind === "movie") await createExistingWorkflow(media.id, { search_immediately: true })
      else {
        const seasons = parseSeasonSpec(seasonSpec)
        if (seasons.length !== 1) throw new Error("Add one season at a time from this screen")
        await createExistingWorkflow(media.id, { search_immediately: true, season: seasons[0] })
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
  return <div className="space-y-4">
    <Panel><div className="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between"><div><div className="flex gap-2"><StatusChip tone="info">{media.kind === "movie" ? "Movie" : "Series"}</StatusChip>{(media.identities ?? []).map((identity) => <StatusChip key={`${identity.namespace}-${identity.value}`}>{identity.namespace.toUpperCase()} {identity.value}</StatusChip>)}</div><h2 className="mt-4 text-3xl font-[850] tracking-[-0.04em] text-white">{media.title}</h2><div className="mt-2 text-[11px] text-[#8692a2]">{media.year ?? "Year unavailable"}</div></div><div className="flex flex-wrap gap-2">{media.kind === "series" ? <Input className="mm-input w-36" value={seasonSpec} onChange={(event) => setSeasonSpec(event.target.value)} aria-label="Season and episode count" /> : null}<Button disabled={busy} onClick={() => void addWorkflow()}><Plus /> {media.kind === "movie" ? "Search upgrade" : "Add season"}</Button><AlertDialog><AlertDialogTrigger asChild><Button variant="outline" disabled={busy}><Trash2 /> Recycle</Button></AlertDialogTrigger><AlertDialogContent className="border-[#303947] bg-[#10141a] text-white"><AlertDialogHeader><AlertDialogTitle>Recycle {media.title}?</AlertDialogTitle><AlertDialogDescription>Managed links move into 30-day retention. AltMount source files stay untouched. Active workflows must be cancelled or completed first.</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>Keep media</AlertDialogCancel><AlertDialogAction onClick={() => void remove()}>Recycle links</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog></div></div></Panel>
    {media.kind === "series" ? <Panel className="!p-0" title="Season monitoring" subtitle="Persisted exact episode expectations; complete season packs remain one workflow and one AltMount job."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Season</TableHead><TableHead>Monitoring</TableHead><TableHead>Pack preference</TableHead><TableHead>Expected episodes</TableHead><TableHead>Latest workflow</TableHead></TableRow></TableHeader><TableBody>{(detail?.seasons ?? []).map((season) => <TableRow key={season.number}><TableCell className="font-semibold">Season {season.number}</TableCell><TableCell><StatusChip tone={season.monitored ? "good" : "neutral"}>{season.monitored ? "Monitored" : "Not monitored"}</StatusChip></TableCell><TableCell>{season.prefer_season_pack ? "Complete pack" : "No pack preference"}</TableCell><TableCell>{season.expected_episodes || "Not snapshotted"}</TableCell><TableCell><StatusChip tone={workflowTone(season.workflow_state ?? "")}>{season.workflow_state ? prettyState(season.workflow_state) : prettyState(season.state)}</StatusChip></TableCell></TableRow>)}</TableBody></Table></div>{detail?.seasons.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No seasons are monitored.</div> : null}</Panel> : null}
    <WorkflowTable workflows={detail?.workflows ?? []} empty="No workflows for this media." />
    <Panel className="!p-0" title="Published files" subtitle="Plex-safe link generations with immutable read-only AltMount provenance."><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Destination</TableHead><TableHead>Source</TableHead><TableHead>Generation</TableHead><TableHead>State</TableHead><TableHead>Plex</TableHead></TableRow></TableHeader><TableBody>{(detail?.files ?? []).map((file) => <TableRow key={file.id}><TableCell><Link href={`/files/${file.id}`} className="font-mono text-[9px] text-[#dfe5ec] hover:text-[#aebdff]">{file.destination_relative}</Link></TableCell><TableCell className="max-w-md break-all font-mono text-[9px]">{file.source_relative}</TableCell><TableCell>{file.generation}</TableCell><TableCell><StatusChip>{prettyState(file.state)}</StatusChip></TableCell><TableCell>{file.plex_confirmed ? "Confirmed" : "Not confirmed"}</TableCell></TableRow>)}</TableBody></Table></div>{detail?.files.length === 0 ? <div className="p-8 text-center text-[11px] text-[#7f8a9a]">No managed files have been published for this item.</div> : null}</Panel>
  </div>
}

export function LiveRecycleScreen() {
  const [entries, setEntries] = React.useState<APIRecycleEntry[]>([])
  const [error, setError] = React.useState("")
  const [busy, setBusy] = React.useState("")
  const refresh = React.useCallback(async () => { try { setEntries(await loadRecycle()); setError("") } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not load recycle entries") } }, [])
  React.useEffect(() => { const initial = window.setTimeout(() => void refresh(), 0); return () => window.clearTimeout(initial) }, [refresh])
  const groups = React.useMemo(() => {
    const grouped = new Map<string, APIRecycleEntry[]>()
    for (const entry of entries) grouped.set(entry.media_id, [...(grouped.get(entry.media_id) ?? []), entry])
    return Array.from(grouped.entries())
  }, [entries])
  async function restore(entry: APIRecycleEntry) { setBusy(entry.media_id); try { await restoreRecycle(entry.id); toast.success("Complete media restore queued"); await refresh() } catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not restore media") } finally { setBusy("") } }
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{groups.length === 0 ? <EmptyPanel title="Recycle bin is empty" copy="Managed links retained after deletion will appear here for 30 days." /> : groups.map(([mediaId, mediaEntries]) => { const restorable = mediaEntries.find((entry) => entry.state === "retained"); const purgeAt = mediaEntries.reduce((latest, entry) => entry.purge_after > latest ? entry.purge_after : latest, ""); return <Panel key={mediaId} title={mediaEntries[0].media_title} subtitle={`${mediaEntries.length} managed link${mediaEntries.length === 1 ? "" : "s"} · AltMount source retained`} action={restorable ? <Button size="sm" disabled={busy === mediaId} onClick={() => void restore(restorable)}><ArchiveRestore /> Restore all</Button> : undefined}><div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Original path</TableHead><TableHead>State</TableHead><TableHead>Retention</TableHead></TableRow></TableHeader><TableBody>{mediaEntries.map((entry) => <TableRow key={entry.id}><TableCell className="max-w-xl break-all font-mono text-[9px]">{entry.original_relative}</TableCell><TableCell><StatusChip tone={entry.state === "restored" ? "good" : entry.state === "purged" ? "neutral" : "warn"}>{prettyState(entry.state)}</StatusChip></TableCell><TableCell>{new Date(entry.purge_after).toLocaleDateString()}</TableCell></TableRow>)}</TableBody></Table></div><div className="mt-3 text-[9.5px] text-[#778392]">Final link purge: {purgeAt ? new Date(purgeAt).toLocaleString() : "—"}. Real source files are never auto-deleted.</div></Panel> })}</div>
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
