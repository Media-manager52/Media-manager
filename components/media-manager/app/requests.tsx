"use client"

import * as React from "react"
import Link from "next/link"
import { CircleCheck } from "lucide-react"

import { relativeTime, requestStatus } from "@/lib/app-presentation"
import type { APIRequest } from "@/lib/media-manager-api"
import { useAppData } from "./app-data"
import { AppShell } from "./shell"
import { ApprovalActions, kindLabel, LoadState, NeedsCard, PageTitle, StatusPill, Thumb, titleHref } from "./parts"

type Filter = "all" | "waiting_for_you" | "needs_you" | "on_the_way" | "in_plex"
const filters: Array<[Filter, string]> = [["all", "All"], ["waiting_for_you", "Waiting for your OK"], ["needs_you", "Needs you"], ["on_the_way", "On the way"], ["in_plex", "In Plex"]]
const pageSize = 50

function RequestRow({ request }: { request: APIRequest }) {
  const status = requestStatus(request)
  const name = request.title || `Request ${request.request_id}`
  const seasons = request.media_kind === "series" && request.season_count ? ` · ${request.season_count} ${request.season_count === 1 ? "season" : "seasons"}` : ""
  return <div className="mm-request">
    <Thumb title={name} src={request.poster_url} small />
    <div className="mm-request-body">
      {request.media_id ? <Link href={titleHref(request.media_id)}>{name}</Link> : <strong>{name}</strong>}
      <div className="mm-small mm-muted">{kindLabel(request.media_kind)}{request.year ? ` · ${request.year}` : ""}{seasons}{request.requester ? ` · asked by ${request.requester}` : ""}{request.requested_at ? ` ${relativeTime(request.requested_at)}` : ""}</div>
    </div>
    <StatusPill label={status.label} tone={status.tone} />
    {status.group === "waiting_for_you" ? <ApprovalActions request={request} /> : null}
  </div>
}

function RequestsContent() {
  const { data, error } = useAppData()
  const [filter, setFilter] = React.useState<Filter>("all")
  const [limit, setLimit] = React.useState(pageSize)
  const all = [...(data?.requests ?? [])].sort((a, b) => (b.requested_at ?? b.updated_at).localeCompare(a.requested_at ?? a.updated_at))
  const shown = all.filter((request) => filter === "all" || requestStatus(request).group === filter)
  return <div className="mm-stack-tight">
    <PageTitle title="Requests" lede="What your household asked for in Seerr, and how far each one has come." />
    <div className="mm-chips" role="group" aria-label="Show">
      {filters.map(([value, label]) => <button key={value} type="button" className="mm-chip" aria-pressed={filter === value} onClick={() => { setFilter(value); setLimit(pageSize) }}>{label}<span>{value === "all" ? all.length : all.filter((request) => requestStatus(request).group === value).length}</span></button>)}
    </div>
    <LoadState error={error} loading={!data && !error} />
    {data && !shown.length ? <p className="mm-state">{all.length ? "Nothing in this group." : "No requests yet. Requests made in Seerr show up here."}</p> : null}
    <div className="mm-list">{shown.slice(0, limit).map((request) => <RequestRow key={request.id} request={request} />)}</div>
    {shown.length > limit ? <div><button type="button" className="mm-btn" onClick={() => setLimit((current) => current + pageSize)}>Show more ({shown.length - limit} left)</button></div> : null}
  </div>
}

function NeedsContent() {
  const { data, error } = useAppData()
  const needs = data?.needs ?? []
  return <div className="mm-stack-tight">
    <PageTitle title="Needs you" lede="Only decisions Media Manager can’t make on its own end up here." />
    <LoadState error={error} loading={!data && !error} />
    {data && !needs.length ? <div className="mm-empty"><CircleCheck size={40} color="#8FE0AE" aria-hidden /><strong>All clear</strong><span>Everything else is in Plex or on its way.</span></div> : null}
    {needs.map((item) => <NeedsCard key={item.key} item={item} />)}
  </div>
}

export function AppRequests() {
  return <AppShell section="requests"><RequestsContent /></AppShell>
}

export function AppNeedsYou() {
  return <AppShell section="needs"><NeedsContent /></AppShell>
}
