"use client"

import * as React from "react"
import Image from "next/image"
import Link from "next/link"
import { Check, ChevronRight, LoaderCircle } from "lucide-react"

import { decideRequest, runWorkflowAction, type APIRequest } from "@/lib/media-manager-api"
import { appSteps, attentionTitle, offeredActions, posterTone, relativeTime, type AppTone, type OfferedAction, type StageInfo } from "@/lib/app-presentation"
import { useAppData, type AppTitle, type NeedsItem } from "./app-data"

export function titleHref(id: string) {
  return `/library/${encodeURIComponent(id)}`
}

/** The same title in the expert console, where every recovery option lives. */
export function consoleHref(kind: string, id: string) {
  return `/media/${kind === "series" ? "series" : "movies"}/${encodeURIComponent(id)}`
}

export function kindLabel(kind: string) {
  return kind === "series" ? "TV show" : "Movie"
}

export function PageTitle({ title, lede, children }: { title: string; lede?: React.ReactNode; children?: React.ReactNode }) {
  return <div><h1 className="mm-h1">{title}</h1>{lede ? <p className="mm-lede">{lede}</p> : null}{children}</div>
}

/** Loading and error states shared by every page. */
export function LoadState({ error, loading, label = "Loading your library…" }: { error: string; loading: boolean; label?: string }) {
  const { refresh } = useAppData()
  if (error) return <div role="alert" className="mm-notice mm-notice-error"><span>Couldn’t reach Media Manager: {error}</span><button type="button" className="mm-btn mm-btn-small" onClick={refresh}>Try again</button></div>
  if (loading) return <p role="status" className="mm-state">{label}</p>
  return null
}

/** Provider artwork when it loads; otherwise a toned poster with the title set in type. */
export function Poster({ title, year, src, badge, sizes = "(max-width: 720px) 40vw, 180px" }: { title: string; year?: number; src?: string; badge?: { text: string; tone: AppTone }; sizes?: string }) {
  const [failed, setFailed] = React.useState<string | null>(null)
  const showArt = Boolean(src) && failed !== src
  return <span className="mm-poster" style={{ background: posterTone(title) }}>
    {showArt ? <Image src={src as string} alt="" fill sizes={sizes} className="mm-poster-img" onError={() => setFailed(src ?? null)} /> : <>
      <span className="mm-poster-year">{year ?? ""}</span>
      <span className="mm-poster-title">{title}</span>
    </>}
    {badge ? <span className="mm-badge" data-tone={badge.tone}>{badge.text}</span> : null}
  </span>
}

export function Thumb({ title, src, small = false }: { title: string; src?: string; small?: boolean }) {
  const [failed, setFailed] = React.useState<string | null>(null)
  return <span className={small ? "mm-thumb mm-thumb-small" : "mm-thumb"} style={{ background: posterTone(title) }} aria-hidden>
    {src && failed !== src ? <Image src={src} alt="" fill sizes="72px" className="mm-poster-img" onError={() => setFailed(src)} /> : title.charAt(0).toUpperCase()}
  </span>
}

export function PosterCard({ title }: { title: AppTitle }) {
  const status = title.status
  const badge = status.stage === "in_plex" ? undefined : { text: status.short, tone: status.tone }
  const sub = title.kind === "series" && title.metadata?.season_count ? `${title.metadata.season_count} ${title.metadata.season_count === 1 ? "season" : "seasons"}` : title.year ? String(title.year) : kindLabel(title.kind)
  return <Link href={titleHref(title.id)} className="mm-poster-card" aria-label={`${title.title}${title.year ? ` (${title.year})` : ""}, ${status.label}`}>
    <Poster title={title.title} year={title.year} src={title.metadata?.poster_url} badge={badge} />
    <span className="mm-poster-name">{title.title}</span>
    <span className="mm-poster-sub">{sub}</span>
  </Link>
}

export function PosterRow({ heading, titles, href }: { heading: string; titles: AppTitle[]; href?: string }) {
  if (!titles.length) return null
  return <section className="mm-section" aria-label={heading}>
    <div className="mm-section-head"><h2 className="mm-h2">{heading}</h2>{href ? <Link href={href} className="mm-see-all">See all<ChevronRight size={16} aria-hidden /></Link> : null}</div>
    <div className="mm-row">{titles.map((title) => <PosterCard key={title.id} title={title} />)}</div>
  </section>
}

export function StatusPill({ label, tone }: { label: string; tone: AppTone }) {
  return <span className="mm-pill" data-tone={tone}>{label}</span>
}

/** Five progress segments driven only by the persisted stage. */
export function Segments({ status }: { status: StageInfo }) {
  return <span className="mm-segments" aria-hidden>{appSteps.map((step, index) => <span key={step} data-state={index < status.step ? "done" : index === status.step ? "current" : "todo"} />)}</span>
}

export function ProgressSteps({ status }: { status: StageInfo }) {
  const problem = status.stage === "needs_you"
  return <ol className="mm-steps" aria-label={`Progress: ${status.label}`}>{appSteps.map((step, index) => {
    const done = index < status.step
    const state = done ? "done" : index === status.step ? problem ? "problem" : "current" : "todo"
    return <li key={step} data-state={state} data-line={index + 1 <= status.step && !(problem && index + 1 === status.step) ? "done" : "todo"} aria-current={state === "current" || state === "problem" ? "step" : undefined}>
      <span><span className="mm-dot">{done ? <Check size={12} strokeWidth={3.5} aria-hidden /> : null}</span><span className="mm-line" /></span>
      <span className="mm-step-label">{step}</span>
    </li>
  })}</ol>
}

/**
 * Buttons for the server-listed actions of one workflow. Stop asks first.
 * Success and failure are reported in plain words and the snapshot is re-read.
 */
export function WorkflowActions({ actions, title, onDone }: { actions: OfferedAction[]; title: string; onDone?: () => void }) {
  const { refresh, notify } = useAppData()
  const [busy, setBusy] = React.useState("")
  const [confirming, setConfirming] = React.useState(false)
  async function run(item: OfferedAction) {
    setBusy(item.action.action)
    try {
      await runWorkflowAction(item.action)
      notify(item.action.action === "cancel" ? `Stopped ${title}. Nothing was deleted.` : `${item.label}: done for ${title}. Media Manager takes it from here.`)
      refresh(); onDone?.()
    } catch (reason) {
      notify(`Couldn’t ${item.label.toLowerCase()} for ${title}: ${reason instanceof Error ? reason.message : "unknown error"}`)
    } finally { setBusy(""); setConfirming(false) }
  }
  if (!actions.length) return null
  return <div className="mm-actions">{actions.map((item) => item.confirm && confirming
    ? <React.Fragment key={item.action.action}><span className="mm-small">Stop {title}?</span><button type="button" className="mm-btn mm-btn-danger" disabled={Boolean(busy)} onClick={() => void run(item)}>Yes, stop</button><button type="button" className="mm-btn" onClick={() => setConfirming(false)}>Keep going</button></React.Fragment>
    : <button key={item.action.action} type="button" className={item.primary ? "mm-btn mm-btn-primary" : "mm-btn"} disabled={Boolean(busy)} onClick={() => item.confirm ? setConfirming(true) : void run(item)}>{busy === item.action.action ? <LoaderCircle size={18} className="animate-spin" aria-hidden /> : null}{item.label}</button>)}</div>
}

export function ApprovalActions({ request }: { request: APIRequest }) {
  const { refresh, notify } = useAppData()
  const [busy, setBusy] = React.useState(false)
  const name = request.title || `Request ${request.request_id}`
  async function decide(decision: "approve" | "decline") {
    setBusy(true)
    try {
      await decideRequest(request.id, decision)
      notify(decision === "approve" ? `Approved ${name}. Media Manager will start looking for it.` : `Declined ${name}.${request.requester ? ` ${request.requester} will see that in Seerr.` : ""}`)
      refresh()
    } catch (reason) {
      notify(`Couldn’t ${decision} ${name}: ${reason instanceof Error ? reason.message : "unknown error"}`)
    } finally { setBusy(false) }
  }
  return <div className="mm-actions">
    <button type="button" className="mm-btn mm-btn-primary" disabled={busy} onClick={() => void decide("approve")} aria-label={`Approve ${name}`}>Approve</button>
    <button type="button" className="mm-btn" disabled={busy} onClick={() => void decide("decline")} aria-label={`Decline ${name}`}>Decline</button>
  </div>
}

function seasonText(variant: string) {
  const match = /^season:0*(\d+)(?::episode:0*(\d+))?/.exec(variant)
  if (!match) return ""
  return match[2] ? `Season ${match[1]}, episode ${match[2]}` : `Season ${match[1]}`
}

/** One owner decision, with the server's own reason and only the actions it allows. */
export function NeedsCard({ item }: { item: NeedsItem }) {
  if (item.kind === "approval") {
    const request = item.request
    const name = request.title || `Request ${request.request_id}`
    return <article className="mm-needs">
      <Thumb title={name} src={request.poster_url} />
      <div className="mm-needs-body">
        <div className="mm-needs-kicker">{kindLabel(request.media_kind)}{request.year ? ` · ${request.year}` : ""}{request.requested_at ? ` · ${relativeTime(request.requested_at)}` : ""}</div>
        <div className="mm-needs-title">{request.media_id ? <Link href={titleHref(request.media_id)}>{name}</Link> : name}<span> — {request.requester ? `${request.requester} asked for it` : "Waiting for your OK"}</span></div>
        <div className="mm-needs-detail">Approve it and Media Manager starts looking right away. The decision goes back to Seerr.</div>
      </div>
      <ApprovalActions request={request} />
    </article>
  }
  const workflow = item.workflow
  const scope = seasonText(workflow.variant)
  const actions = offeredActions(workflow)
  return <article className="mm-needs">
    <Thumb title={workflow.title} src={item.title?.metadata?.poster_url} />
    <div className="mm-needs-body">
      <div className="mm-needs-kicker">{kindLabel(workflow.media_kind)}{workflow.year ? ` · ${workflow.year}` : ""}{scope ? ` · ${scope}` : ""}</div>
      <div className="mm-needs-title"><Link href={titleHref(workflow.media_id)}>{workflow.title}</Link><span> — {attentionTitle(workflow)}</span></div>
      {workflow.attention_detail ? <div className="mm-needs-detail">{workflow.attention_detail}</div> : null}
    </div>
    {actions.length ? <WorkflowActions actions={actions} title={workflow.title} /> : <div className="mm-actions"><Link className="mm-btn" href={consoleHref(workflow.media_kind, workflow.media_id)}>See all options</Link></div>}
  </article>
}
