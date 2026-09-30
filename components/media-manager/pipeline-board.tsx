"use client"

import * as React from "react"
import Link from "next/link"
import { ArrowDown } from "lucide-react"

import type { APIMedia, APIWorkflow } from "@/lib/media-manager-api"
import { groupPipeline, pipelineStages, type PipelineStage } from "@/lib/pipeline"
import { cn } from "@/lib/utils"
import { timeAgo } from "./live-operational-screens"
import { Poster } from "./poster"

const columnLimit = 8

const stageAccent: Record<PipelineStage, string> = {
  attention: "bg-warn-solid",
  requested: "bg-fg-subtle",
  searching: "bg-brand",
  downloading: "bg-info-solid",
  verifying: "bg-info-solid",
  publishing: "bg-info-solid",
  in_plex: "bg-good-solid",
  stopped: "bg-fg-subtle",
}

/**
 * The control-room board: every workflow placed in the stage its persisted
 * state proves, left to right from request to Plex. Cards only link; every
 * recovery action stays in the owner-gated table below the board.
 */
export function PipelineBoard({ workflows, media }: { workflows: APIWorkflow[]; media: Map<string, APIMedia> }) {
  const groups = React.useMemo(() => groupPipeline(workflows), [workflows])
  const moving = groups.searching.length + groups.downloading.length + groups.verifying.length + groups.publishing.length
  return <section aria-labelledby="pipeline-heading" className="space-y-3">
    <div className="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 id="pipeline-heading" className="text-[19px] font-bold tracking-[-0.02em] text-fg">Pipeline</h2>
        <p className="mt-0.5 text-[13px] text-fg-muted">{moving} moving · {groups.attention.length} need{groups.attention.length === 1 ? "s" : ""} you · {groups.requested.length} requested · {groups.in_plex.length} in Plex</p>
      </div>
      <a href="#workflow-recovery" className="inline-flex items-center gap-1 text-[13px] font-medium text-fg-muted hover:text-fg"><ArrowDown className="size-3.5" aria-hidden />Recovery actions</a>
    </div>
    <div className="mm-shelf -mx-4 flex gap-3 overflow-x-auto px-4 pb-2 sm:-mx-6 sm:px-6 lg:-mx-10 lg:px-10" role="list" aria-label="Pipeline stages">
      {pipelineStages.map((stage) => {
        const items = groups[stage.key]
        return <div key={stage.key} role="listitem" aria-label={`${stage.label}: ${items.length}`} className={cn("flex w-[248px] shrink-0 snap-start flex-col rounded-2xl border bg-surface/60 xl:w-auto xl:min-w-[180px] xl:flex-1", stage.key === "attention" && items.length ? "border-warn-line bg-warn-soft/40" : "border-line")}>
          <div className="border-b border-line px-3.5 py-3">
            <div className="flex items-center gap-2"><span className={cn("size-2 rounded-full", stageAccent[stage.key])} aria-hidden /><span className="text-[13px] font-semibold text-fg">{stage.label}</span><span className="ml-auto rounded-full bg-surface-3 px-2 py-0.5 text-[11.5px] font-semibold tabular-nums text-fg-secondary">{items.length}</span></div>
            <p className="mt-1 text-[11.5px] leading-4 text-fg-subtle">{stage.hint}</p>
          </div>
          <ul className="flex flex-1 flex-col gap-2 p-2">
            {items.length === 0 ? <li className="px-1.5 py-4 text-center text-[12px] text-fg-subtle">Empty</li> : null}
            {items.slice(0, columnLimit).map((workflow) => <li key={workflow.id}><PipelineCard workflow={workflow} poster={media.get(workflow.media_id)?.metadata?.poster_url} attention={stage.key === "attention"} /></li>)}
            {items.length > columnLimit ? <li className="px-1.5 py-1 text-[12px] text-fg-subtle">+{items.length - columnLimit} more in the list below</li> : null}
          </ul>
        </div>
      })}
    </div>
  </section>
}

function PipelineCard({ workflow, poster, attention }: { workflow: APIWorkflow; poster?: string; attention: boolean }) {
  const href = workflow.media_kind === "movie" ? `/media/movies/${workflow.media_id}` : `/media/series/${workflow.media_id}`
  return <Link href={href} className={cn("flex gap-2.5 rounded-xl border bg-surface p-2 outline-none transition hover:border-line-strong hover:bg-surface-2 focus-visible:ring-2 focus-visible:ring-ring", attention ? "border-warn-line" : "border-line")}>
    <Poster src={poster} title={workflow.title} className="w-10 shrink-0 rounded-md [&_div]:text-[0px]" sizes="40px" />
    <div className="min-w-0 flex-1">
      <div className="truncate text-[13px] font-semibold text-fg">{workflow.title}</div>
      <div className="truncate font-mono text-[11.5px] text-fg-subtle">{workflow.variant}</div>
      {attention && (workflow.attention_detail || workflow.attention_code)
        ? <div className="mt-1 line-clamp-2 text-[11.5px] leading-4 text-warn">{workflow.attention_detail || workflow.attention_code?.replaceAll("_", " ")}</div>
        : <div className="mt-1 text-[11.5px] text-fg-muted">{workflow.state.replaceAll("_", " ")} · {timeAgo(workflow.updated_at)}</div>}
    </div>
  </Link>
}
