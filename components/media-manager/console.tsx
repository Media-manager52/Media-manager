"use client"

import * as React from "react"
import Link from "next/link"
import Image from "next/image"

import { toast } from "sonner"

import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { decideRequest, type APIRequest, type APIWantedItem, type APIWorkflow } from "@/lib/media-manager-api"
import { cn } from "@/lib/utils"
import { attentionHeadline, retryPlan, workflowStep, workflowSteps } from "@/lib/workflow-presentation"
import { StatusChip } from "./ui"

/** Stable hue per title, so artwork-less items stay distinguishable. */
export function titleHue(title: string) {
  let hash = 0
  for (let index = 0; index < title.length; index += 1) hash = (hash * 31 + title.charCodeAt(index)) >>> 0
  return hash % 360
}

function initials(title: string) {
  const words = title.replace(/[^\p{L}\p{N} ]/gu, "").split(" ").filter(Boolean)
  return ((words[0]?.[0] ?? "") + (words[1]?.[0] ?? words[0]?.[1] ?? "")).toUpperCase() || "?"
}

/**
 * A 34x50 poster thumbnail, or a tinted initials tile when provider artwork
 * is absent or fails. It never requests anything but the provider artwork.
 */
export function MiniPoster({ src, title, className }: { src?: string; title: string; className?: string }) {
  const [failed, setFailed] = React.useState<string | null>(null)
  const hue = titleHue(title)
  return <span className={cn("relative grid h-[50px] w-[34px] shrink-0 place-items-center overflow-hidden rounded-[5px] text-[12px] font-extrabold text-white/85 [text-shadow:0_1px_4px_rgba(0,0,0,.5)]", className)} style={{ background: `linear-gradient(160deg, hsl(${hue} 45% 36%), hsl(${hue} 50% 13%))` }} aria-hidden>
    {src && failed !== src ? <Image src={src} alt="" fill sizes="34px" onError={() => setFailed(src)} className="object-cover" /> : initials(title)}
  </span>
}

/** Eight-segment acquisition stepper driven only by the persisted state; no invented percentages. */
export function StageStepper({ state, className }: { state: string; className?: string }) {
  const { current, complete } = workflowStep(state)
  return <span className={cn("flex items-center gap-[3px]", className)} role="img" aria-label={current < 0 ? "Not in an acquisition step" : `Step ${current + 1} of ${workflowSteps.length}: ${workflowSteps[current]}`}>
    {workflowSteps.map((step, index) => <i key={step} className={cn("h-1 flex-1 rounded-[2px] bg-surface-3", complete ? "bg-good-solid" : index < current ? "bg-brand/55" : index === current && "bg-brand shadow-[0_0_8px_var(--brand-line)]")} />)}
  </span>
}

/** A card body row: leading visual, title and caption, trailing content. */
export function ConsoleRow({ leading, title, caption, children, href, className }: { leading?: React.ReactNode; title: React.ReactNode; caption?: React.ReactNode; children?: React.ReactNode; href?: string; className?: string }) {
  const content = <>
    {leading}
    <div className="min-w-0 flex-1"><div className="truncate text-[12.5px] font-semibold text-fg">{title}</div>{caption ? <div className="truncate text-[11px] text-fg-subtle">{caption}</div> : null}</div>
    {children}
  </>
  const classes = cn("flex items-center gap-3 border-t border-line px-4 py-[11px] first:border-t-0", href && "outline-none transition hover:bg-surface-2/60 focus-visible:bg-surface-2", className)
  return href ? <Link href={href} className={classes}>{content}</Link> : <div className={classes}>{content}</div>
}

export function mediaHref(kind: string, id: string) {
  return kind === "series" ? `/media/series/${id}` : `/media/movies/${id}`
}

/**
 * One owner-attention entry in plain language: what kind of problem, the exact
 * persisted reason, the selected candidate when known, and when (or whether)
 * Media Manager retries on its own.
 */
export function AttentionCard({ item, workflow, poster }: { item: { title: string; year?: number; variant: string; detail: string; mediaKind: "movie" | "series"; mediaId: string }; workflow?: APIWorkflow; poster?: string }) {
  const headline = attentionHeadline(workflow?.attention_code, workflow?.state)
  const href = mediaHref(item.mediaKind, item.mediaId)
  return <div className="border-t border-line border-l-[3px] border-l-warn-solid bg-warn-soft/50 px-4 py-3.5 first:border-t-0">
    <div className="mb-2 flex flex-wrap items-center gap-2"><StatusChip tone={headline.tone}>{headline.label}</StatusChip><span className="ml-auto text-[11px] text-fg-subtle">{retryPlan(workflow)}</span></div>
    <div className="flex gap-3">
      <MiniPoster src={poster} title={item.title} />
      <div className="min-w-0 flex-1">
        <h4 className="text-[13.5px] font-bold text-fg"><Link href={href} className="hover:underline hover:underline-offset-2">{item.title}{item.year ? ` (${item.year})` : ""}</Link><span className="font-medium text-fg-subtle"> · {item.variant}</span></h4>
        <p className="mb-2.5 mt-1 text-[12.5px] leading-[19px] text-fg-muted">{item.detail}</p>
        {workflow?.ready_release_title ? <div className="mb-2.5 truncate text-[11px] text-fg-subtle">Candidate: <span className="font-mono text-[11px] text-fg-muted">{workflow.ready_release_title}</span>{workflow.ready_indexer_name ? ` · ${workflow.ready_indexer_name}` : ""}</div> : null}
        <div className="flex flex-wrap gap-1.5"><Button asChild size="sm" variant="outline"><Link href={href}>Review {item.mediaKind === "series" ? "series" : "movie"}</Link></Button><Button asChild size="sm" variant="outline"><Link href="/activity/queue">Recovery options</Link></Button></div>
      </div>
    </div>
  </div>
}

/** Owner-attention items joined to their workflows, wanted rows first. */
export function attentionEntries(wanted: APIWantedItem[], workflows: APIWorkflow[]) {
  const entries: Array<{ key: string; item: Parameters<typeof AttentionCard>[0]["item"]; workflow?: APIWorkflow }> = []
  const seen = new Set<string>()
  for (const row of wanted) {
    if (!row.owner_action_required) continue
    seen.add(row.workflow_id)
    entries.push({ key: row.workflow_id, workflow: workflows.find((workflow) => workflow.id === row.workflow_id), item: { title: row.title, year: row.year, variant: row.variant, detail: row.attention_detail?.trim() || "Media Manager paused this for a decision only you can make.", mediaKind: row.media_kind, mediaId: row.media_id } })
  }
  for (const workflow of workflows) {
    if (seen.has(workflow.id)) continue
    if (!(workflow.owner_action_required || ["needs_attention", "blocked", "quarantined", "transport_unknown"].includes(workflow.state))) continue
    entries.push({ key: workflow.id, workflow, item: { title: workflow.title, year: workflow.year, variant: workflow.variant, detail: workflow.attention_detail?.trim() || "Media Manager paused this for a decision only you can make.", mediaKind: workflow.media_kind, mediaId: workflow.media_id } })
  }
  return entries
}

/**
 * Season coverage as a segmented bar. Segments are counts, not positions:
 * acquired, acquiring, needing attention, released but missing, and not yet
 * aired. Grey is calm on purpose; unreleased episodes are simply waiting.
 */
export function CoverageBar({ total, acquired, acquiring = 0, attention = 0, released, className }: { total: number; acquired: number; acquiring?: number; attention?: number; released?: number; className?: string }) {
  const count = Math.max(0, Math.min(total, 60))
  const releasedCount = released ?? total
  const segments = Array.from({ length: count }, (_, index) => index < acquired ? "a" : index < acquired + acquiring ? "b" : index < acquired + acquiring + attention ? "c" : index < releasedCount ? "m" : "u")
  const label = `${acquired} of ${total} acquired${acquiring ? `, ${acquiring} acquiring` : ""}${attention ? `, ${attention} need attention` : ""}${releasedCount < total ? `, ${total - releasedCount} not yet aired` : ""}`
  return <span role="img" aria-label={label} title={label} className={cn("flex h-[5px] gap-[2px] overflow-hidden rounded-[3px]", className)}>
    {count === 0 ? <i className="flex-1 bg-surface-3" /> : segments.map((kind, index) => <i key={index} className={cn("flex-1 rounded-[1.5px]", kind === "a" ? "bg-good-solid" : kind === "b" ? "bg-brand" : kind === "c" ? "bg-warn-solid" : kind === "m" ? "bg-fg-subtle/35" : "bg-surface-3")} />)}
  </span>
}

export function CoverageLegend({ items }: { items: Array<{ kind: "a" | "b" | "c" | "m" | "u"; label: string }> }) {
  return <div className="flex flex-wrap gap-3.5 text-[10.5px] text-fg-subtle">{items.map((item) => <span key={item.label} className="flex items-center gap-1.5"><i className={cn("size-2 rounded-[2.5px]", item.kind === "a" ? "bg-good-solid" : item.kind === "b" ? "bg-brand" : item.kind === "c" ? "bg-warn-solid" : item.kind === "m" ? "bg-fg-subtle/35" : "bg-surface-3")} />{item.label}</span>)}</div>
}

/**
 * The download percentage exactly as AltMount last reported it. Renders
 * nothing when AltMount reported no percentage; it is never estimated.
 */
export function DownloadProgress({ workflow, className }: { workflow: Pick<APIWorkflow, "download_percent" | "download_stage" | "download_observed_at">; className?: string }) {
  if (workflow.download_percent === undefined || workflow.download_percent === null) return null
  const percent = Math.max(0, Math.min(100, workflow.download_percent))
  const observed = workflow.download_observed_at ? new Date(workflow.download_observed_at).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }) : ""
  return <span className={cn("inline-flex min-w-[96px] flex-col gap-1", className)} title={`Reported by AltMount${observed ? ` at ${observed}` : ""}`}>
    <span className="flex items-baseline justify-between gap-2 text-[11px]"><span className="truncate text-fg-subtle">{workflow.download_stage || "AltMount"}</span><b className="font-semibold tabular-nums text-brand-text">{percent}%</b></span>
    <span className="h-1 overflow-hidden rounded-full bg-surface-3" role="progressbar" aria-label="Download progress reported by AltMount" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}><i className="block h-full rounded-full bg-brand" style={{ width: `${percent}%` }} /></span>
  </span>
}

/** True only for a request Seerr still holds for approval. */
export function requestAwaitingApproval(request: Pick<APIRequest, "seerr_state" | "lifecycle">) {
  return request.seerr_state === "1" && request.lifecycle === "not_approved"
}

/**
 * Approve or decline a pending Seerr request. The decision is made in Seerr;
 * Media Manager then re-reads the request through normal intake. Declining
 * asks for confirmation because Seerr notifies the requester.
 */
export function RequestDecisionButtons({ request, onDone, size = "sm" }: { request: Pick<APIRequest, "id" | "title" | "request_id" | "requester">; onDone?: () => void; size?: "sm" | "default" }) {
  const [busy, setBusy] = React.useState(false)
  async function decide(decision: "approve" | "decline") {
    setBusy(true)
    try {
      await decideRequest(request.id, decision)
      toast.success(decision === "approve" ? "Approved in Seerr · intake will pick it up" : "Declined in Seerr")
      onDone?.()
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : "Seerr did not confirm the decision")
    } finally {
      setBusy(false)
    }
  }
  const name = request.title || `Request ${request.request_id}`
  return <span className="flex shrink-0 items-center gap-1.5">
    <Button type="button" size={size} disabled={busy} onClick={() => void decide("approve")} aria-label={`Approve ${name}`}>Approve</Button>
    <AlertDialog>
      <AlertDialogTrigger asChild><Button type="button" size={size} variant="ghost" disabled={busy} aria-label={`Decline ${name}`}>Decline</Button></AlertDialogTrigger>
      <AlertDialogContent className="border-line-strong bg-surface-2 text-fg">
        <AlertDialogHeader><AlertDialogTitle>Decline {name}?</AlertDialogTitle><AlertDialogDescription>Seerr marks the request declined{request.requester ? ` and may notify ${request.requester}` : ""}. Media Manager will not search for it.</AlertDialogDescription></AlertDialogHeader>
        <AlertDialogFooter><AlertDialogCancel>Keep pending</AlertDialogCancel><AlertDialogAction onClick={() => void decide("decline")}>Decline in Seerr</AlertDialogAction></AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </span>
}
