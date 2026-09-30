"use client"

import * as React from "react"
import Link from "next/link"
import { AlertTriangle, CalendarDays, ChevronLeft, ChevronRight, Clapperboard, Info, Plus, Workflow } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  loadCalendar,
  loadCatalogMedia,
  loadDashboard,
  loadRequests,
  loadSystemHealth,
  loadWanted,
  type APIMedia,
} from "@/lib/media-manager-api"
import { pipelineStage, pipelineStages } from "@/lib/pipeline"
import { cn } from "@/lib/utils"
import { collectOwnerAttention, isActivelyAcquiring, isCompleted } from "@/lib/workflow-states"
import { ErrorNotice, LoadingPanel, overviewQueueItems, timeAgo, useLiveData } from "./live-operational-screens"
import { Backdrop, Poster } from "./poster"

// Home reads only existing bounded read models. It never invents a statistic
// and never triggers a search, scan, or publication.
const homeLoader = async (signal: AbortSignal) => {
  const [dashboard, wanted, calendar, requests, health, media] = await Promise.all([
    loadDashboard(signal),
    loadWanted(signal),
    loadCalendar(signal),
    loadRequests(signal),
    loadSystemHealth(signal),
    loadCatalogMedia(signal),
  ])
  return { dashboard, wanted, calendar, requests, health, media }
}

type ShelfItem = { key: string; href: string; title: string; year?: number; poster?: string; eyebrow?: string; caption: string; tone?: "warn" | "info" | "good" | "neutral" }

const stageLabel = new Map(pipelineStages.map((stage) => [stage.key, stage.label]))

function mediaHref(kind: "movie" | "series" | "", id?: string) {
  if (!id) return "/requests"
  return kind === "series" ? `/media/series/${id}` : `/media/movies/${id}`
}

export function HomeFeed() {
  const { data, error } = useLiveData(homeLoader)
  if (!data && !error) return <div className="px-4 pt-6 sm:px-8"><h1 className="sr-only">Home</h1><LoadingPanel label="Loading your library…" /></div>
  if (!data) return <div className="px-4 pt-6 sm:px-8"><h1 className="sr-only">Home</h1><ErrorNotice error={error} /></div>

  const catalog = new Map<string, APIMedia>(data.media.map((item) => [item.id, item]))
  const posterFor = (id?: string) => (id ? catalog.get(id)?.metadata?.poster_url : undefined)
  const workflows = data.dashboard.recent_workflows
  const counts = data.dashboard.counts

  const attention = collectOwnerAttention(data.wanted, workflows)
  const queue = overviewQueueItems(workflows, data.wanted)
  const onTheWay = queue.filter((item) => isActivelyAcquiring(item.state) && !item.ownerActionRequired)
  const arrivals = uniqueBy(workflows.filter((workflow) => isCompleted(workflow.state)).sort(newestFirst), (workflow) => workflow.media_id)
  const now = new Date().getTime()
  const upcoming = data.calendar
    .filter((entry) => new Date(entry.air_date).getTime() >= now)
    .sort((left, right) => new Date(left.air_date).getTime() - new Date(right.air_date).getTime())
    .slice(0, 12)
  const requests = [...data.requests].sort(newestFirst).slice(0, 16)
  const movies = data.media.filter((item) => item.kind === "movie").slice(0, 20)
  const shows = data.media.filter((item) => item.kind === "series").slice(0, 20)
  const outbox = data.health.outbox
  const outboxRetrying = outbox.due > 0 && outbox.retrying > 0
  const unconfigured = data.health.components.filter((component) => component.configuration_state !== "configured").length

  // The hero features the newest Plex-confirmed arrival; with nothing
  // confirmed yet it falls back to a catalog title with artwork, and it never
  // claims availability it cannot prove.
  const heroArrival = arrivals[0]
  const heroMedia = heroArrival ? catalog.get(heroArrival.media_id) : data.media.find((item) => item.metadata?.backdrop_url) ?? data.media[0]

  return <div className="pb-6">
    <h1 className="sr-only">Home</h1>
    {error ? <div className="px-4 pt-4 sm:px-8"><ErrorNotice error={error} /></div> : null}

    {heroMedia ? <section aria-label="Featured" className="relative isolate -mt-16 flex min-h-[440px] items-end overflow-hidden pt-16 sm:min-h-[520px] lg:min-h-[600px]">
      <Backdrop src={heroMedia.metadata?.backdrop_url} title={heroMedia.title} className="-z-10" />
      <div className="w-full max-w-[1680px] px-4 pb-10 sm:px-8 lg:px-12 lg:pb-14">
        <div className="max-w-[640px]">
          <div className="text-[12px] font-semibold uppercase tracking-[0.18em] text-brand-text">{heroArrival ? `New in Plex · ${timeAgo(heroArrival.updated_at)}` : "From your library"}</div>
          <h2 className="mt-3 text-[34px] font-bold leading-[1.02] tracking-[-0.035em] text-fg sm:text-[48px] lg:text-[60px]">{heroMedia.title}</h2>
          <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px] font-medium text-fg-secondary">
            {heroMedia.year ? <span>{heroMedia.year}</span> : null}
            <span>{heroMedia.kind === "series" ? "TV Show" : "Movie"}</span>
            {heroMedia.metadata?.runtime_minutes ? <span>{Math.floor(heroMedia.metadata.runtime_minutes / 60)}h {heroMedia.metadata.runtime_minutes % 60}m</span> : null}
            {heroMedia.metadata?.season_count ? <span>{heroMedia.metadata.season_count} season{heroMedia.metadata.season_count === 1 ? "" : "s"}</span> : null}
            {heroMedia.metadata?.genres.slice(0, 3).map((genre) => <span key={genre} className="rounded-full border border-line-strong px-2 py-0.5 text-[12px]">{genre}</span>)}
          </div>
          {heroMedia.metadata?.overview ? <p className="mt-4 line-clamp-3 max-w-[560px] text-[15px] leading-6 text-fg-secondary">{heroMedia.metadata.overview}</p> : null}
          <div className="mt-6 flex flex-wrap gap-2.5">
            <Button asChild size="lg" className="h-11 rounded-full px-6"><Link href={mediaHref(heroMedia.kind, heroMedia.id)}><Info /> View details</Link></Button>
            <Button asChild size="lg" variant="outline" className="h-11 rounded-full border-line-strong bg-canvas/40 px-6 backdrop-blur"><Link href="/discover"><Plus /> Add media</Link></Button>
          </div>
        </div>
      </div>
    </section> : <section aria-label="Get started" className="mx-4 mt-6 rounded-2xl border border-line bg-surface px-6 py-12 text-center sm:mx-8">
      <Clapperboard className="mx-auto size-8 text-brand-text" aria-hidden />
      <h2 className="mt-3 text-[22px] font-bold tracking-[-0.02em] text-fg">Your library is empty</h2>
      <p className="mx-auto mt-2 max-w-md text-[14px] text-fg-muted">Add a movie or show, or connect Seerr, and every title will appear here as it moves toward Plex.</p>
      <Button asChild className="mt-5 rounded-full"><Link href="/discover"><Plus /> Add media</Link></Button>
    </section>}

    <div className="relative z-10 space-y-9 lg:space-y-11">
      <StatusStrip movies={counts.movies} series={counts.series} active={onTheWay.length} attention={attention.length} outboxRetrying={outboxRetrying} unconfigured={unconfigured} />

      {attention.length ? <Shelf title="Needs you" subtitle="Paused for a decision only you can make." href="/wanted" linkLabel="Open Wanted" items={attention.map((entry) => ({ key: entry.id, href: entry.href, title: entry.title, year: entry.year, poster: posterFor(entry.mediaId), eyebrow: "Needs you", caption: entry.reason, tone: "warn" }))} /> : null}

      <Shelf title="On its way" subtitle="Searching, downloading, verifying or publishing right now; no invented percentages." href="/activity/queue" linkLabel="Control room" empty="Nothing is moving right now. New requests start here." items={onTheWay.map((item) => ({ key: item.id, href: item.href, title: item.title, year: item.year, poster: posterFor(item.href.split("/").pop()), eyebrow: stageLabel.get(pipelineStage({ state: item.state, owner_action_required: item.ownerActionRequired })) ?? "In progress", caption: `${item.variant} · ${timeAgo(item.updatedAt)}`, tone: "info" }))} />

      <Shelf title="Recently added" subtitle="Plex confirmed the exact file and identity." href="/activity/history" linkLabel="History" empty="Nothing has been confirmed in Plex yet." items={arrivals.map((workflow) => ({ key: workflow.id, href: mediaHref(workflow.media_kind, workflow.media_id), title: workflow.title, year: workflow.year, poster: posterFor(workflow.media_id), eyebrow: "In Plex", caption: `${workflow.variant} · ${timeAgo(workflow.updated_at)}`, tone: "good" }))} />

      {upcoming.length ? <section aria-labelledby="shelf-coming-up" className="px-4 sm:px-8 lg:px-12">
        <ShelfHeader id="shelf-coming-up" title="Coming up" subtitle="Air dates from your providers. A date never triggers a search on its own." href="/calendar" linkLabel="Calendar" />
        <ul className="mm-shelf mt-3 flex gap-3 overflow-x-auto pb-2">
          {upcoming.map((entry) => { const date = new Date(entry.air_date); return <li key={`${entry.workflow_id}:${entry.season}:${entry.episode}`} className="w-[260px] shrink-0 snap-start">
            <Link href={`/media/series/${entry.media_id}`} className="flex h-full items-center gap-3.5 rounded-xl border border-line bg-surface p-3 outline-none transition hover:border-line-strong hover:bg-surface-2 focus-visible:ring-2 focus-visible:ring-ring">
              <div className="flex w-12 shrink-0 flex-col items-center rounded-lg bg-brand-soft py-1.5 text-brand-text"><span className="text-[11px] font-semibold uppercase">{date.toLocaleDateString(undefined, { month: "short" })}</span><span className="text-[20px] font-bold leading-none">{date.getDate()}</span></div>
              <div className="min-w-0"><div className="truncate text-[14px] font-semibold text-fg">{entry.media_title}</div><div className="mt-0.5 truncate text-[12.5px] text-fg-muted">S{String(entry.season).padStart(2, "0")}E{String(entry.episode).padStart(2, "0")}{entry.episode_title ? ` · ${entry.episode_title}` : ""}</div></div>
            </Link>
          </li> })}
        </ul>
      </section> : null}

      <Shelf title="Requests" subtitle="What people asked for in Seerr, and where it stands." href="/requests" linkLabel="All requests" empty="No Seerr requests yet." items={requests.map((request) => ({ key: request.id, href: request.media_id ? mediaHref(request.media_kind, request.media_id) : "/requests", title: request.title || `Request ${request.request_id}`, year: request.year, poster: request.poster_url ?? posterFor(request.media_id), eyebrow: request.requester || "Seerr", caption: request.attention_count ? "Needs you" : request.confirmed_count ? "In Plex" : request.workflow_count ? "On its way" : "Requested", tone: request.attention_count ? "warn" : request.confirmed_count ? "good" : "neutral" }))} />

      {movies.length ? <Shelf title="Movies" href="/media" linkLabel="Browse movies" items={movies.map((item) => ({ key: item.id, href: mediaHref("movie", item.id), title: item.title, year: item.year, poster: item.metadata?.poster_url, caption: item.year ? String(item.year) : "Movie" }))} /> : null}
      {shows.length ? <Shelf title="TV Shows" href="/media/series" linkLabel="Browse TV shows" items={shows.map((item) => ({ key: item.id, href: mediaHref("series", item.id), title: item.title, year: item.year, poster: item.metadata?.poster_url, caption: item.metadata?.season_count ? `${item.metadata.season_count} season${item.metadata.season_count === 1 ? "" : "s"}` : "TV Show" }))} /> : null}
    </div>
  </div>
}

function StatusStrip({ movies, series, active, attention, outboxRetrying, unconfigured }: { movies: number; series: number; active: number; attention: number; outboxRetrying: boolean; unconfigured: number }) {
  return <div className="flex flex-wrap items-center gap-2 px-4 pt-2 sm:px-8 lg:px-12">
    <Link href="/media" className="mm-chip">{movies} movies</Link>
    <Link href="/media/series" className="mm-chip">{series} TV shows</Link>
    <Link href="/activity/queue" className="mm-chip"><Workflow className="size-3.5 text-info" aria-hidden />{active} on its way</Link>
    <Link href="/wanted" className={cn("mm-chip", attention && "border-warn-line bg-warn-soft text-warn")}><AlertTriangle className={cn("size-3.5", attention ? "text-warn" : "text-fg-subtle")} aria-hidden />{attention ? `${attention} need${attention === 1 ? "s" : ""} you` : "Nothing needs you"}</Link>
    {outboxRetrying ? <Link href="/system/health" className="mm-chip border-warn-line bg-warn-soft text-warn">Worker is retrying events</Link> : null}
    {unconfigured ? <Link href="/system/health" className="mm-chip">{unconfigured} integration{unconfigured === 1 ? "" : "s"} not configured</Link> : null}
    <Link href="/calendar" className="mm-chip"><CalendarDays className="size-3.5 text-fg-subtle" aria-hidden />Calendar</Link>
  </div>
}

function ShelfHeader({ id, title, subtitle, href, linkLabel, onPrev, onNext }: { id: string; title: string; subtitle?: string; href?: string; linkLabel?: string; onPrev?: () => void; onNext?: () => void }) {
  return <div className="flex items-end justify-between gap-4">
    <div className="min-w-0">
      <h2 id={id} className="text-[19px] font-bold tracking-[-0.02em] text-fg sm:text-[21px]">{href ? <Link href={href} className="group inline-flex items-center gap-1 outline-none hover:text-fg focus-visible:underline">{title}<ChevronRight className="size-5 text-fg-subtle transition group-hover:translate-x-0.5 group-hover:text-fg" aria-hidden /></Link> : title}</h2>
      {subtitle ? <p className="mt-0.5 truncate text-[13px] text-fg-muted">{subtitle}</p> : null}
    </div>
    <div className="flex shrink-0 items-center gap-1.5">
      {href && linkLabel ? <Link href={href} className="hidden text-[13px] font-medium text-fg-muted hover:text-fg sm:inline">{linkLabel}</Link> : null}
      {onPrev && onNext ? <div className="hidden gap-1 md:flex"><Button type="button" size="icon" variant="ghost" className="size-8 rounded-full" onClick={onPrev} aria-label={`Scroll ${title} back`}><ChevronLeft /></Button><Button type="button" size="icon" variant="ghost" className="size-8 rounded-full" onClick={onNext} aria-label={`Scroll ${title} forward`}><ChevronRight /></Button></div> : null}
    </div>
  </div>
}

const toneClass = { warn: "text-warn", info: "text-info", good: "text-good", neutral: "text-fg-muted" } as const

function Shelf({ title, subtitle, href, linkLabel, items, empty }: { title: string; subtitle?: string; href?: string; linkLabel?: string; items: ShelfItem[]; empty?: string }) {
  const id = `shelf-${title.toLowerCase().replace(/[^a-z]+/g, "-")}`
  const listRef = React.useRef<HTMLUListElement>(null)
  const scroll = (direction: 1 | -1) => listRef.current?.scrollBy({ left: direction * listRef.current.clientWidth * 0.85, behavior: "smooth" })
  return <section aria-labelledby={id} className="px-4 sm:px-8 lg:px-12">
    <ShelfHeader id={id} title={title} subtitle={subtitle} href={href} linkLabel={linkLabel} onPrev={items.length > 4 ? () => scroll(-1) : undefined} onNext={items.length > 4 ? () => scroll(1) : undefined} />
    {items.length === 0 ? <div className="mt-3 rounded-xl border border-dashed border-line-strong px-4 py-6 text-[13px] text-fg-subtle">{empty}</div>
      : <ul ref={listRef} className="mm-shelf mt-3 flex gap-3 overflow-x-auto pb-2 sm:gap-4">
        {items.map((item) => <li key={item.key} className="w-[132px] shrink-0 snap-start sm:w-[156px] lg:w-[172px]">
          <Link href={item.href} className="group block rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-canvas" aria-label={`${item.title}${item.year ? ` (${item.year})` : ""}${item.eyebrow ? `, ${item.eyebrow}` : ""}`}>
            <Poster src={item.poster} title={item.title} subtitle={item.year ? String(item.year) : undefined} className="shadow-[var(--shadow-panel)] ring-1 ring-line transition duration-200 group-hover:-translate-y-1 group-hover:ring-line-strong" />
            <div className="mt-2 min-w-0 px-0.5">
              {item.eyebrow ? <div className={cn("truncate text-[11.5px] font-semibold uppercase tracking-[0.06em]", toneClass[item.tone ?? "neutral"])}>{item.eyebrow}</div> : null}
              <div className="truncate text-[13.5px] font-semibold text-fg">{item.title}</div>
              <div className="truncate text-[12px] text-fg-muted" title={item.caption}>{item.caption}</div>
            </div>
          </Link>
        </li>)}
      </ul>}
  </section>
}

function newestFirst(left: { updated_at: string }, right: { updated_at: string }) {
  return new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime()
}

function uniqueBy<T>(items: T[], key: (item: T) => string) {
  const seen = new Set<string>()
  return items.filter((item) => { const value = key(item); if (seen.has(value)) return false; seen.add(value); return true })
}
