"use client"

import * as React from "react"
import Link from "next/link"
import { CheckCircle2, Plus, RefreshCw } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  loadCalendar,
  loadCatalogMedia,
  loadDashboard,
  loadRequests,
  loadSystemHealth,
  loadWanted,
  loadWorkflows,
  type APISourceStatus,
  type APISystemHealth,
} from "@/lib/media-manager-api"
import { cn } from "@/lib/utils"
import { isActivelyAcquiring, isCompleted } from "@/lib/workflow-states"
import { AttentionCard, attentionEntries, ConsoleRow, mediaHref, MiniPoster, StageStepper } from "./console"
import { ErrorNotice, LoadingPanel, timeAgo, useLiveData } from "./live-operational-screens"
import { PageHeader, Panel, StateBadge, StatusChip } from "./ui"

// The overview reads only existing bounded read models. It never invents a
// statistic and it never triggers a search, scan, or publication.
const overviewLoader = async (signal: AbortSignal) => {
  const [dashboard, wanted, workflows, calendar, requests, health, media] = await Promise.all([
    loadDashboard(signal),
    loadWanted(signal),
    loadWorkflows(false, signal),
    loadCalendar(signal),
    loadRequests(signal),
    loadSystemHealth(signal),
    loadCatalogMedia(signal),
  ])
  return { dashboard, wanted, workflows, calendar, requests, health, media }
}

type Tile = { key: string; tone: "good" | "warn" | "idle"; title: string; detail: string; href?: string }

function sourceHealthy(component: APISourceStatus) {
  return component.enabled && component.configuration_state === "configured" && component.consecutive_failure === 0
}

/** Five status tiles built from persisted health; nothing is guessed. */
export function systemTiles(health: APISystemHealth): Tile[] {
  const components = health.components ?? []
  const byKind = (kind: string) => components.filter((component) => component.kind === kind && component.enabled)
  const indexers = byKind("newznab")
  const healthyIndexers = indexers.filter(sourceHealthy).length
  const clients = byKind("altmount")
  const plex = byKind("plex")
  const coreReady = health.database_state === "ready" && health.schema_version === health.expected_schema
  const outbox = health.outbox
  return [
    { key: "core", tone: coreReady ? "good" : "warn", title: coreReady ? "Core healthy" : "Core needs attention", detail: coreReady ? `Database ready · schema ${health.schema_version}` : `Database ${health.database_state} · schema ${health.schema_version}/${health.expected_schema}`, href: "/system/health" },
    { key: "indexers", tone: indexers.length === 0 ? "idle" : healthyIndexers === indexers.length ? "good" : "warn", title: indexers.length === 0 ? "No indexers enabled" : `${healthyIndexers} of ${indexers.length} indexers healthy`, detail: indexers.length === healthyIndexers ? "Searching within pacing" : `${indexers.length - healthyIndexers} need${indexers.length - healthyIndexers === 1 ? "s" : ""} owner action`, href: "/sources/indexers" },
    { key: "altmount", tone: clients.length === 0 ? "idle" : clients.every(sourceHealthy) ? "good" : "warn", title: clients.length === 0 ? "No download client" : clients.every(sourceHealthy) ? "AltMount connected" : "AltMount needs attention", detail: clients[0]?.last_outcome ? `Last: ${clients[0].last_outcome.replaceAll("_", " ")}` : "Read-only output", href: "/sources/download-clients" },
    { key: "plex", tone: plex.length === 0 ? "idle" : plex.every(sourceHealthy) ? "good" : "warn", title: plex.length === 0 ? "Plex not configured" : plex.every(sourceHealthy) ? "Plex connected" : "Plex needs attention", detail: plex.length ? `${plex.length} server${plex.length === 1 ? "" : "s"} · exact confirmation` : "Publication needs Plex", href: "/settings/connections" },
    { key: "worker", tone: outbox.retrying > 0 ? "warn" : outbox.pending > 0 ? "good" : "idle", title: outbox.retrying > 0 ? "Worker retrying" : outbox.pending > 0 ? "Worker busy" : "Worker idle", detail: outbox.pending ? `${outbox.pending} pending · ${outbox.due} due · ${outbox.retrying} retrying` : "No queued events", href: "/system/tasks" },
  ]
}

export function LiveOverviewScreen() {
  const { data, error, refresh } = useLiveData(overviewLoader)
  const header = <PageHeader title="Overview" subtitle={`${new Date().toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long" })} — everything below is live persisted state`} actions={<><Button variant="outline" size="sm" onClick={refresh}><RefreshCw /> Refresh</Button><Button asChild size="sm"><Link href="/discover"><Plus /> Add media</Link></Button></>} />
  if (!data && !error) return <div className="space-y-5">{header}<LoadingPanel label="Loading the live overview…" /></div>
  if (!data) return <div className="space-y-5">{header}<ErrorNotice error={error} /></div>

  const posters = new Map(data.media.map((item) => [item.id, item.metadata?.poster_url]))
  const workflows = data.workflows.length ? data.workflows : data.dashboard.recent_workflows
  const attention = attentionEntries(data.wanted, workflows)
  const active = workflows.filter((workflow) => isActivelyAcquiring(workflow.state)).sort((left, right) => new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime())
  const completed = workflows.filter((workflow) => isCompleted(workflow.state)).sort((left, right) => new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime())
  const now = new Date().getTime()
  const today = new Date().toDateString()
  const completedToday = completed.filter((workflow) => new Date(workflow.updated_at).toDateString() === today).length
  const airing = data.calendar.filter((entry) => new Date(entry.air_date).getTime() >= now - 86400000).sort((left, right) => new Date(left.air_date).getTime() - new Date(right.air_date).getTime()).slice(0, 4)
  const retries = workflows.filter((workflow) => workflow.next_rescan_at && !workflow.owner_action_required && new Date(workflow.next_rescan_at).getTime() >= now).sort((left, right) => new Date(left.next_rescan_at ?? 0).getTime() - new Date(right.next_rescan_at ?? 0).getTime()).slice(0, 4)
  const monitored = data.wanted.filter((item) => !item.owner_action_required).length
  const requests = [...data.requests].sort((left, right) => new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime()).slice(0, 4)

  return <div className="space-y-4">
    {header}
    {error ? <ErrorNotice error={error} /> : null}

    <div className="grid grid-cols-2 gap-2.5 md:grid-cols-3 xl:grid-cols-5">
      {systemTiles(data.health).map((tile) => <Link key={tile.key} href={tile.href ?? "/system/health"} className="flex items-center gap-3 rounded-[11px] border border-line bg-surface px-3.5 py-3 outline-none transition hover:border-line-strong focus-visible:ring-2 focus-visible:ring-ring">
        <span className={cn("size-[9px] shrink-0 rounded-full", tile.tone === "good" ? "bg-good-solid shadow-[0_0_8px_var(--good)]" : tile.tone === "warn" ? "bg-warn-solid shadow-[0_0_8px_var(--warn)]" : "bg-fg-subtle")} aria-hidden />
        <div className="min-w-0"><div className="truncate text-[12px] font-semibold text-fg">{tile.title}</div><div className="truncate text-[10.5px] text-fg-subtle">{tile.detail}</div></div>
      </Link>)}
    </div>

    <div className="grid grid-cols-1 items-start gap-4 xl:grid-cols-[1.6fr_1fr]">
      <div className="min-w-0 space-y-4">
        <Panel flush title="Needs attention" subtitle={attention.length ? `${attention.length} item${attention.length === 1 ? "" : "s"} waiting on a decision only you can make` : "Nothing is waiting on you"} action={attention.length ? <Button asChild variant="ghost" size="sm"><Link href="/wanted">View all →</Link></Button> : undefined} className={cn(attention.length && "border-warn-line")}>
          {attention.length === 0 ? <div className="flex items-center gap-2.5 px-4 py-4 text-[12.5px] text-good"><CheckCircle2 className="size-4" aria-hidden />Nothing needs your attention. Waiting and searching continue automatically.</div>
            : attention.slice(0, 4).map((entry) => <AttentionCard key={entry.key} item={entry.item} workflow={entry.workflow} poster={posters.get(entry.item.mediaId)} />)}
        </Panel>

        <Panel flush title="Active now" subtitle={`${active.length} workflow${active.length === 1 ? "" : "s"} moving toward Plex`} action={<Button asChild variant="ghost" size="sm"><Link href="/activity/queue">All workflows →</Link></Button>}>
          {active.length === 0 ? <div className="px-4 py-5 text-[12.5px] text-fg-subtle">Nothing is actively acquiring right now.</div>
            : active.slice(0, 6).map((workflow) => <ConsoleRow key={workflow.id} href={mediaHref(workflow.media_kind, workflow.media_id)} leading={<MiniPoster src={posters.get(workflow.media_id)} title={workflow.title} />} title={`${workflow.title}${workflow.year ? ` (${workflow.year})` : ""}`} caption={`${workflow.variant} · ${workflow.round_ordinal ? `round ${workflow.round_ordinal}` : "first attempt"}`}>
              <StageStepper state={workflow.state} className="hidden w-[150px] sm:flex" />
              <StateBadge state={workflow.state} />
              <span className="w-12 shrink-0 text-right text-[11px] text-fg-subtle">{timeAgo(workflow.updated_at)}</span>
            </ConsoleRow>)}
        </Panel>

        <Panel flush title="Recently completed" subtitle={completedToday ? `${completedToday} confirmed in Plex today` : "Plex confirmed the exact file and identity"}>
          {completed.length === 0 ? <div className="px-4 py-5 text-[12.5px] text-fg-subtle">No completed publication yet.</div>
            : completed.slice(0, 5).map((workflow) => <ConsoleRow key={workflow.id} href={mediaHref(workflow.media_kind, workflow.media_id)} className="py-[9px]" leading={<CheckCircle2 className="size-4 shrink-0 text-good" aria-hidden />} title={`${workflow.title}${workflow.variant !== "movie" && workflow.variant !== "default" ? ` — ${workflow.variant}` : ""}`}>
              <span className="shrink-0 text-[11px] text-fg-subtle">published {timeAgo(workflow.updated_at)}</span>
            </ConsoleRow>)}
        </Panel>
      </div>

      <div className="min-w-0 space-y-4">
        <Panel flush title="Waiting" subtitle="Normal · nothing to do">
          <div className="px-4 pb-1 pt-3 text-[10px] font-bold uppercase tracking-[0.1em] text-fg-subtle">Airing soon — acquired automatically</div>
          {airing.length === 0 ? <div className="px-4 py-1.5 text-[12px] text-fg-subtle">No upcoming air dates are persisted.</div>
            : airing.map((entry) => <Link key={`${entry.workflow_id}:${entry.season}:${entry.episode}`} href={`/media/series/${entry.media_id}`} className="flex items-center gap-2.5 px-4 py-[7px] text-[12.5px] text-fg-muted hover:text-fg"><b className="truncate font-semibold text-fg">{entry.media_title}</b><span className="shrink-0">S{String(entry.season).padStart(2, "0")} E{String(entry.episode).padStart(2, "0")}</span><span className="ml-auto shrink-0 whitespace-nowrap text-[11px] text-fg-subtle">{new Date(entry.air_date).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" })}</span></Link>)}
          <div className="px-4 pb-1 pt-3 text-[10px] font-bold uppercase tracking-[0.1em] text-fg-subtle">Scheduled retries</div>
          {retries.length === 0 ? <div className="px-4 py-1.5 text-[12px] text-fg-subtle">No paced retries are scheduled.</div>
            : retries.map((workflow) => <Link key={workflow.id} href={mediaHref(workflow.media_kind, workflow.media_id)} className="flex items-center gap-2.5 px-4 py-[7px] text-[12.5px] text-fg-muted hover:text-fg"><b className="truncate font-semibold text-fg">{workflow.title}</b><span className="shrink-0">{workflow.variant}</span><span className="ml-auto shrink-0 whitespace-nowrap text-[11px] text-fg-subtle">{new Date(workflow.next_rescan_at ?? "").toLocaleString(undefined, { weekday: "short", hour: "2-digit", minute: "2-digit" })}</span></Link>)}
          <div className="mx-4 mb-3.5 mt-2.5 flex items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-[11.5px] text-fg-subtle"><CheckCircle2 className="size-3.5 shrink-0 text-good" aria-hidden />{monitored} item{monitored === 1 ? "" : "s"} monitored — waiting is normal operation, not an error.</div>
        </Panel>

        <Panel flush title="Recent requests" subtitle="From Seerr" action={<Button asChild variant="ghost" size="sm"><Link href="/requests">View all →</Link></Button>}>
          {requests.length === 0 ? <div className="px-4 py-5 text-[12.5px] text-fg-subtle">No Seerr requests are persisted yet.</div>
            : requests.map((request) => <ConsoleRow key={request.id} href="/requests" leading={<MiniPoster src={request.poster_url ?? (request.media_id ? posters.get(request.media_id) : undefined)} title={request.title || "Request"} />} title={request.title || `Request ${request.request_id}`} caption={`${request.media_kind === "series" ? "TV" : "Movie"} · ${request.requester || "Unknown requester"} · ${timeAgo(request.updated_at)}`}>
              <StateBadge state={request.lifecycle} />
            </ConsoleRow>)}
        </Panel>

        <Panel title="Plex publication">
          <div className="flex gap-6">
            <div><div className="text-[22px] font-extrabold tabular-nums text-fg">{completedToday}</div><div className="text-[10.5px] text-fg-subtle">published today</div></div>
            <div><div className="text-[22px] font-extrabold tabular-nums text-fg">{data.dashboard.counts.current_files}</div><div className="text-[10.5px] text-fg-subtle">current managed files</div></div>
            <div><div className="text-[22px] font-extrabold tabular-nums text-fg">{data.dashboard.counts.movies + data.dashboard.counts.series}</div><div className="text-[10.5px] text-fg-subtle">titles cataloged</div></div>
          </div>
          <div className="mt-3"><StatusChip tone="good">Nothing reaches Plex unverified</StatusChip></div>
        </Panel>
      </div>
    </div>
  </div>
}
