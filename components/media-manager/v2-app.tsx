"use client"

import * as React from "react"
import Link from "next/link"
import { Activity, ArrowLeft, ArrowRight, Check, CheckCircle2, ChevronRight, CircleAlert, Clock3, Film, Inbox, LayoutGrid, Library, Moon, RefreshCw, Search, Settings2, Sun, Tv } from "lucide-react"

import { loadCatalogMedia, loadManagedFiles, loadRequests, loadSystemHealth, loadWanted, loadWorkflows, type APIRequest, type APIWorkflow } from "@/lib/media-manager-api"
import { buildMediaLibraryRecords, type MediaLibraryRecord } from "@/lib/media-library"
import { filterRequests, requestLifecycleLabel, requestLifecycleTone, requestProgressMessage, seerrStateLabel, type RequestFilter } from "@/lib/media-manager-requests"
import { isActivelyAcquiring } from "@/lib/workflow-states"
import { attentionHeadline, retryPlan } from "@/lib/workflow-presentation"
import { themeStorageKey } from "@/lib/theme"
import { parseSeriesVariant } from "@/lib/series-variants"
import { cn } from "@/lib/utils"
import { attentionEntries, DownloadProgress, mediaHref, MiniPoster, StageStepper } from "./console"
import { useLiveData } from "./live-operational-screens"
import { MetadataArtwork } from "./metadata-artwork"
import { StateBadge, StatusChip } from "./ui"

export type V2View = "overview" | "library" | "requests" | "attention"

// The preview reads the same bounded owner models as V1. It has no mutation
// handlers; reviewed writes remain available in the established owner console.
async function loadV2(signal: AbortSignal) {
  const [media, files, wanted, workflows, requests, health] = await Promise.all([
    loadCatalogMedia(signal), loadManagedFiles(signal), loadWanted(signal),
    loadWorkflows(false, signal), loadRequests(signal), loadSystemHealth(signal),
  ])
  const library = ["movie", "series"].flatMap((kind) => buildMediaLibraryRecords(kind as "movie" | "series", media, files, wanted, workflows))
  return { media, library, workflows, requests, health, attention: attentionEntries(wanted, workflows) }
}

const navigation = [
  { view: "overview", href: "/v2", label: "Overview", icon: LayoutGrid },
  { view: "library", href: "/v2/library", label: "Library", icon: Library },
  { view: "requests", href: "/v2/requests", label: "Requests", icon: Inbox },
  { view: "attention", href: "/v2/attention", label: "Needs attention", icon: CircleAlert },
] as const

const headings = {
  overview: { title: "Your library, in motion.", description: "See what’s arriving, what’s ready, and what needs a closer look." },
  library: { title: "A home for every story.", description: "Your movies and series, with their actual library status." },
  requests: { title: "Every request. A clear next step.", description: "Follow requests from Seerr approval to confirmation in Plex." },
  attention: { title: "Let’s get these moving.", description: "The actual blockers, why they happened, and where to review them." },
}

function recordedTime(value: string | undefined) {
  if (!value || !Number.isFinite(Date.parse(value))) return null
  return new Date(value).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
}

function scopeLabel(value: string) {
  const scope = parseSeriesVariant(value)
  if (scope.kind === "episode") return `Season ${scope.season} · Episode ${scope.episode}`
  if (scope.kind === "season") return `Season ${scope.season}`
  return value === "movie" || value === "default" ? "Movie" : value
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="v2-empty"><Film aria-hidden /><p>{children}</p></div>
}

function SectionHeading({ title, detail, href, linkLabel = "View all" }: { title: string; detail?: string; href?: string; linkLabel?: string }) {
  return <div className="v2-section-heading"><div><h2>{title}</h2>{detail ? <p>{detail}</p> : null}</div>{href ? <Link className="v2-text-link" href={href}>{linkLabel}<ArrowRight aria-hidden /></Link> : null}</div>
}

function PosterCard({ item }: { item: MediaLibraryRecord }) {
  return <Link href={mediaHref(item.kind, item.id)} className="v2-poster-card">
    <div className="v2-poster-art">
      <MetadataArtwork src={item.metadata?.poster_url} fallback={item.kind === "series" ? "/media/series-placeholder.svg" : "/media/movie-placeholder.svg"} alt="" width={300} height={450} />
      <span className="v2-poster-kind">{item.kind === "series" ? <Tv aria-hidden /> : <Film aria-hidden />}{item.kind === "series" ? "Series" : "Movie"}</span>
      <span className={cn("v2-poster-status", item.available && "v2-poster-ready")}>{item.available ? <CheckCircle2 aria-hidden /> : <Clock3 aria-hidden />}{item.available ? "In Plex" : "Tracked"}</span>
    </div>
    <h3>{item.title}</h3><p>{item.year ?? "Year unknown"}{item.resolutions.length ? ` · ${item.resolutions.join(" / ")}` : ""}</p>
  </Link>
}

function RequestCard({ request }: { request: APIRequest }) {
  const next = recordedTime(request.next_search_at)
  const last = recordedTime(request.last_search_at)
  const message = requestProgressMessage(request)
  return <article className="v2-request-card">
    <div className="v2-request-top"><MiniPoster src={request.poster_url} title={request.title || `Request ${request.request_id}`} /><div className="v2-grow"><h3>{request.title || `Request ${request.request_id}`}</h3><p>{request.year ?? "Year unknown"} · {request.media_kind === "series" ? "Series" : "Movie"}{request.requester ? ` · ${request.requester}` : ""}</p></div><StatusChip tone={requestLifecycleTone(request.lifecycle)}>{requestLifecycleLabel(request.lifecycle)}</StatusChip></div>
    <div className="v2-request-facts"><span>Seerr: {seerrStateLabel(request.seerr_state)}</span><span>{request.workflow_count > 0 ? <><Check aria-hidden />Added to Media Manager</> : "No workflow created"}</span></div>
    {message ? <p className="v2-request-message">{message}</p> : null}
    {last || next ? <div className="v2-search-times">{last ? <span>Last search <time dateTime={request.last_search_at}>{last}</time></span> : null}{next ? <span>Scheduled search <time dateTime={request.next_search_at}>{next}</time></span> : null}</div> : null}
    <Link className="v2-text-link" href={request.media_id ? mediaHref(request.media_kind, request.media_id) : "/requests"}>Review {request.media_id ? "title" : "request"}<ArrowRight aria-hidden /></Link>
  </article>
}

function WorkflowRow({ workflow, poster }: { workflow: APIWorkflow; poster?: string }) {
  return <Link href={mediaHref(workflow.media_kind, workflow.media_id)} className="v2-workflow-row"><MiniPoster src={poster} title={workflow.title} /><div className="v2-grow"><h3>{workflow.title}</h3><p>{scopeLabel(workflow.variant)}</p>{workflow.download_percent !== undefined ? <DownloadProgress workflow={workflow} /> : <StageStepper state={workflow.state} className="mt-2 max-w-48" />}</div><StateBadge state={workflow.state} /><ChevronRight className="v2-chevron" aria-hidden /></Link>
}

export function MediaManagerV2({ view }: { view: V2View }) {
  const { data, error, refresh } = useLiveData(loadV2, { autoRefresh: true })
  const [query, setQuery] = React.useState("")
  const [kind, setKind] = React.useState<"all" | "movie" | "series">("all")
  const [requestFilter, setRequestFilter] = React.useState<RequestFilter>("all")
  const [page, setPage] = React.useState(1)
  function toggleTheme() {
    const nextLight = document.documentElement.classList.contains("dark")
    document.documentElement.classList.toggle("dark", !nextLight)
    localStorage.setItem(themeStorageKey, nextLight ? "light" : "dark")
  }
  function search(value: string) { setQuery(value); setPage(1) }
  const needle = query.trim().toLocaleLowerCase()
  const active = data?.workflows.filter((workflow) => isActivelyAcquiring(workflow.state)) ?? []
  const waiting = data?.requests.filter((request) => request.lifecycle === "waiting") ?? []
  const attention = data?.attention.filter((entry) => `${entry.item.title} ${entry.item.year ?? ""} ${entry.item.detail}`.toLocaleLowerCase().includes(needle)) ?? []
  const library = data?.library.filter((item) => (kind === "all" || item.kind === kind) && item.searchText.includes(needle)) ?? []
  const requests = filterRequests(data?.requests ?? [], requestFilter, query).sort((a, b) => b.updated_at.localeCompare(a.updated_at))
  const activity = active.filter((workflow) => `${workflow.title} ${workflow.year ?? ""}`.toLocaleLowerCase().includes(needle))
  const posters = new Map(data?.media.map((item) => [item.id, item.metadata?.poster_url]))
  const coreReady = data?.health.database_state === "ready" && data?.health.schema_version === data?.health.expected_schema
  const heading = headings[view]
  const pageSize = view === "library" ? 24 : 12
  const rows = view === "library" ? library.length : requests.length
  const pages = Math.max(1, Math.ceil(rows / pageSize))
  const currentPage = Math.min(page, pages)
  const pageStart = (currentPage - 1) * pageSize

  return <div className="mm-v2">
    <a href="#v2-main" className="v2-skip">Skip to content</a>
    <aside className="v2-sidebar">
      <Link href="/v2" className="v2-brand"><span className="v2-brand-mark"><Film aria-hidden /></span><span>Media Manager<small>YOUR MEDIA WORKSPACE</small></span></Link>
      <div className="v2-preview-label"><span>V2</span>Test preview</div>
      <nav aria-label="V2 navigation">{navigation.map((item) => <Link key={item.view} href={item.href} aria-current={view === item.view ? "page" : undefined} className={cn("v2-nav-item", view === item.view && "v2-nav-selected")}><item.icon aria-hidden /><span>{item.label}</span>{item.view === "attention" && data ? <b>{data.attention.length}</b> : null}</Link>)}</nav>
      <div className="v2-sidebar-secondary"><p>THE OWNER CONSOLE</p><Link href="/activity/queue"><Activity aria-hidden />Workflows<ArrowRight aria-hidden /></Link><Link href="/settings/connections"><Settings2 aria-hidden />Connections<ArrowRight aria-hidden /></Link></div>
      <div className="v2-sidebar-bottom"><div className="v2-preview-note"><span className="v2-small-label">A NEW PERSPECTIVE</span><p>Same library. A fresh way to see it.</p><Link href="/">Back to current UI<ArrowLeft aria-hidden /></Link></div><Link href="/system/health" className="v2-core"><i className={cn(data ? coreReady ? "v2-dot-good" : "v2-dot-warn" : "v2-dot-idle")} aria-hidden />{data ? coreReady ? "Core healthy" : "Core needs attention" : error ? "Status unavailable" : "Checking status…"}<ChevronRight aria-hidden /></Link></div>
    </aside>
    <div className="v2-body">
      <header className="v2-topbar"><span className="v2-breadcrumb">Workspace<ChevronRight aria-hidden /><strong>{navigation.find((item) => item.view === view)?.label}</strong></span><div className="v2-topbar-actions"><button onClick={toggleTheme} aria-label="Switch color theme" className="v2-icon-button"><Moon className="v2-moon-icon" aria-hidden /><Sun className="v2-sun-icon" aria-hidden /></button><button onClick={refresh} aria-label="Refresh live data" className="v2-icon-button"><RefreshCw aria-hidden /></button><Link href="/" className="v2-classic-link">Current UI<ArrowRight aria-hidden /></Link></div></header>
      <main id="v2-main" className="v2-main">
        <div className="v2-page-heading"><div><span className="v2-eyebrow">MEDIA MANAGER / V2 PREVIEW</span><h1>{heading.title}</h1><p>{heading.description}</p></div><label className="v2-search"><Search aria-hidden /><input type="search" aria-label="Search this view" placeholder="Search titles…" value={query} onChange={(event) => search(event.target.value)} /></label></div>
        {error ? <div role="alert" className="v2-error"><CircleAlert aria-hidden /><div><strong>Could not refresh your library</strong><p>{error}</p>{data ? <p>Showing the last successfully loaded data.</p> : null}</div><button onClick={refresh}>Try again</button></div> : null}
        {!data && !error ? <div className="v2-loading" role="status"><RefreshCw aria-hidden /><p>Loading your library…</p></div> : null}
        {data ? <>
          {view === "overview" ? <>
            <div className="v2-metrics">{[{ label: "Library titles", value: data.media.length, icon: Library, href: "/v2/library", detail: "Movies & series" }, { label: "Active now", value: active.length, icon: Activity, href: "/activity/queue", detail: "On the way to Plex" }, { label: "Waiting requests", value: waiting.length, icon: Clock3, href: "/v2/requests", detail: "See the next step" }, { label: "Needs attention", value: data.attention.length, icon: CircleAlert, href: "/v2/attention", detail: "Review the actual blockers" }].map((metric) => <Link key={metric.label} href={metric.href} className="v2-metric"><div><span>{metric.label}</span><metric.icon aria-hidden /></div><strong>{metric.value}</strong><p>{metric.detail}<ArrowRight aria-hidden /></p></Link>)}</div>
            <div className="v2-overview-grid"><section className="v2-arrivals"><SectionHeading title="In your library" detail="Titles with files confirmed in Plex" href="/v2/library" linkLabel="Explore library" /><div className="v2-poster-grid v2-poster-grid-small">{library.filter((item) => item.available).slice(0, 4).map((item) => <PosterCard key={item.id} item={item} />)}</div>{!library.some((item) => item.available) ? <Empty>{needle ? "No Plex-confirmed titles match your search." : "No files have been confirmed in Plex yet."}</Empty> : null}</section><section className="v2-activity-panel"><SectionHeading title="On the way" detail="Active acquisition workflows" href="/activity/queue" />{activity.length ? activity.slice(0, 5).map((workflow) => <WorkflowRow key={workflow.id} workflow={workflow} poster={posters.get(workflow.media_id)} />) : <Empty>{needle ? "No active workflows match your search." : "Nothing is actively acquiring right now."}</Empty>}<div className="v2-calm-note"><Clock3 aria-hidden /><p>Waiting is a separate stage. Check Requests for the recorded reason and next search.</p></div></section></div>
            <section><SectionHeading title="Recent requests" detail="Seerr approval and library progress, side by side" href="/v2/requests" /><div className="v2-request-grid">{requests.slice(0, 4).map((request) => <RequestCard key={request.id} request={request} />)}</div>{!requests.length ? <Empty>No requests match this view.</Empty> : null}</section>
            {data.attention.length ? <Link href="/v2/attention" className="v2-attention-banner"><CircleAlert aria-hidden /><div><h2>{data.attention.length} {data.attention.length === 1 ? "item needs" : "items need"} a closer look</h2><p>See what happened and review the recovery options.</p></div><span>Open attention inbox<ArrowRight aria-hidden /></span></Link> : null}
          </> : null}
          {view === "library" ? <section><div className="v2-controls"><div className="v2-segments" role="group" aria-label="Library type">{[{ value: "all", label: "All titles" }, { value: "movie", label: "Movies" }, { value: "series", label: "Series" }].map((tab) => <button key={tab.value} aria-pressed={kind === tab.value} onClick={() => { setKind(tab.value as typeof kind); setPage(1) }}>{tab.label}</button>)}</div><p>{library.length} title{library.length === 1 ? "" : "s"}</p></div><div className="v2-poster-grid">{library.slice(pageStart, pageStart + pageSize).map((item) => <PosterCard key={item.id} item={item} />)}</div>{!library.length ? <Empty>{data.media.length ? "No titles match these filters." : "Your library is empty. Add a title in the current UI to get started."}</Empty> : null}</section> : null}
          {view === "requests" ? <section><div className="v2-controls"><div className="v2-segments" role="group" aria-label="Request status">{[{ value: "all", label: "All requests" }, { value: "active", label: "In progress" }, { value: "completed", label: "Available" }, { value: "attention", label: "Needs review" }].map((tab) => <button key={tab.value} aria-pressed={requestFilter === tab.value} onClick={() => { setRequestFilter(tab.value as RequestFilter); setPage(1) }}>{tab.label}</button>)}</div><p>{requests.length} request{requests.length === 1 ? "" : "s"}</p></div><div className="v2-request-grid">{requests.slice(pageStart, pageStart + pageSize).map((request) => <RequestCard key={request.id} request={request} />)}</div>{!requests.length ? <Empty>No requests match these filters.</Empty> : null}</section> : null}
          {view === "attention" ? <section><SectionHeading title={`${attention.length} ${attention.length === 1 ? "item" : "items"} to review`} detail="Waiting requests are kept separate from failures requiring your review." /><div className="v2-attention-grid">{attention.map((entry) => { const headline = attentionHeadline(entry.workflow?.attention_code, entry.workflow?.state); return <article key={entry.key} className="v2-attention-card"><div className="v2-attention-card-top"><StatusChip tone={headline.tone}>{headline.label}</StatusChip><span>{retryPlan(entry.workflow)}</span></div><div className="v2-request-top"><MiniPoster src={posters.get(entry.item.mediaId)} title={entry.item.title} /><div><h3>{entry.item.title}</h3><p>{entry.item.year ?? "Year unknown"} · {scopeLabel(entry.item.variant)}</p></div></div><p className="v2-attention-reason">{entry.item.detail}</p>{entry.workflow?.ready_release_title ? <p className="v2-candidate">Candidate: {entry.workflow.ready_release_title}</p> : null}<div className="v2-attention-actions"><Link href={mediaHref(entry.item.mediaKind, entry.item.mediaId)}>Review title<ArrowRight aria-hidden /></Link><Link href="/activity/queue">Recovery options<ChevronRight aria-hidden /></Link></div></article> })}</div>{!attention.length ? <Empty>{needle ? "No attention items match your search." : "Nothing needs your attention."}</Empty> : null}</section> : null}
          {(view === "library" || view === "requests") && pages > 1 ? <nav aria-label="Results pages" className="v2-pagination"><button disabled={currentPage === 1} onClick={() => setPage(currentPage - 1)}><ArrowLeft aria-hidden />Previous</button><span>Page {currentPage} of {pages}</span><button disabled={currentPage === pages} onClick={() => setPage(currentPage + 1)}>Next<ArrowRight aria-hidden /></button></nav> : null}
        </> : null}
        <footer className="v2-footer"><span>V2 test preview · Live library data</span><Link href="/">Return to the current UI<ArrowRight aria-hidden /></Link></footer>
      </main>
    </div>
  </div>
}
