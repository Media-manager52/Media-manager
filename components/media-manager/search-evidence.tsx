"use client"

import * as React from "react"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { loadWorkflowSearches, type APISearchRun, type APISearchRunIndexer } from "@/lib/media-manager-api"
import { ErrorNotice, pretty, timeAgo } from "./live-operational-screens"
import { Panel, StatusChip } from "./ui"

const stateTone = (state: string) => state === "succeeded" || state === "complete" ? "good" : state === "failed" ? "bad" : state === "deferred" || state === "partial" ? "warn" : "neutral"

export function SearchEvidencePanel({ workflowID, variant }: { workflowID: string; variant: string }) {
  const [runs, setRuns] = React.useState<APISearchRun[] | null>(null)
  const [error, setError] = React.useState("")
  React.useEffect(() => {
    const controller = new AbortController()
    loadWorkflowSearches(workflowID, controller.signal)
      .then((next) => { setRuns(next); setError("") })
      .catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Could not load search evidence") })
    return () => controller.abort()
  }, [workflowID])
  if (error) return <Panel title="Search evidence"><ErrorNotice error={error} /></Panel>
  if (!runs) return <Panel title="Search evidence"><div className="py-4 text-center text-[12px] text-fg-subtle">Loading persisted search runs…</div></Panel>
  return <Panel flush title="Search evidence" subtitle={`Paced searches for ${variant}. Every row is persisted evidence; this screen never contacts an indexer.`}>
    {runs.length === 0 ? <div className="p-6 text-center text-[12px] text-fg-subtle">No search run has been recorded for this workflow yet.</div> : <div className="divide-y divide-line">{runs.map((run) => <div key={run.id} className="p-4">
      <div className="mb-2 flex flex-wrap items-center gap-2 text-[12px] text-fg-subtle">
        <StatusChip tone={stateTone(run.state)}>{pretty(run.state)}</StatusChip>
        <span>{pretty(run.mode)} run · {new Date(run.started_at).toLocaleString()}</span>
        <span>· {run.new_candidates} new candidate{run.new_candidates === 1 ? "" : "s"}</span>
        {run.completed_at ? <span>· completed {timeAgo(run.completed_at)}</span> : null}
      </div>
      <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Indexer</TableHead><TableHead>Type</TableHead><TableHead>Outcome</TableHead><TableHead>Detail</TableHead><TableHead>Completed</TableHead></TableRow></TableHeader><TableBody>
        {run.indexers.map((indexer) => <IndexerRow key={`${run.id}-${indexer.indexer_id}`} indexer={indexer} />)}
      </TableBody></Table></div>
    </div>)}</div>}
  </Panel>
}

function IndexerRow({ indexer }: { indexer: APISearchRunIndexer }) {
  return <TableRow>
    <TableCell className="font-semibold text-fg">{indexer.indexer_name}</TableCell>
    <TableCell>{pretty(indexer.indexer_kind)}</TableCell>
    <TableCell><StatusChip tone={stateTone(indexer.state)}>{pretty(indexer.state)}</StatusChip></TableCell>
    <TableCell className="max-w-md break-words text-[12px] text-fg-subtle">{indexer.detail || "—"}{indexer.retry_at ? <span className="block">Retry {new Date(indexer.retry_at).toLocaleString()}</span> : null}</TableCell>
    <TableCell>{indexer.completed_at ? timeAgo(indexer.completed_at) : "—"}</TableCell>
  </TableRow>
}
