"use client"

import * as React from "react"
import Link from "next/link"
import { AlertTriangle, CheckCircle2, ClipboardCopy, Download } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { ErrorNotice, LoadingPanel, RefreshButton, timeAgo, useLiveData } from "./live-operational-screens"
import { InlineNotice, MetricCard, Panel, StatusChip } from "./ui"
import { loadProblems, loadSupportBundle, type APIProblem } from "@/lib/media-manager-api"

export function LiveProblemsScreen() {
  const { data, error, refresh } = useLiveData(loadProblems, { autoRefresh: true })
  const [copying, setCopying] = React.useState(false)
  async function copySupport() {
    setCopying(true)
    try {
      await navigator.clipboard.writeText(await loadSupportBundle())
      toast.success("Redacted support bundle copied")
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : "Could not copy the support bundle")
    } finally {
      setCopying(false)
    }
  }
  if (!data && !error) return <LoadingPanel label="Loading the problem list…" />
  const problems = data?.problems ?? []
  const errors = problems.filter((problem) => problem.level === "error").length
  const warnings = problems.filter((problem) => problem.level === "warning").length
  const affected = problems.reduce((sum, problem) => sum + problem.count, 0)
  return <div className="space-y-4">{error ? <ErrorNotice error={error} /> : null}{data ? <>
    <div className="grid gap-3 sm:grid-cols-3">
      <MetricCard label="Errors" value={String(errors)} note="Need an owner action" tone={errors ? "bad" : "good"} icon={AlertTriangle} />
      <MetricCard label="Warnings" value={String(warnings)} note="Degraded but still operating" tone={warnings ? "warn" : "neutral"} icon={AlertTriangle} />
      <MetricCard label="Affected items" value={String(affected)} note="Grouped rows, not raw log lines" tone={affected ? "info" : "neutral"} icon={CheckCircle2} />
    </div>
    <InlineNotice tone={errors ? "bad" : warnings ? "warn" : "good"} title={errors ? "Action is required" : warnings ? "Worth reviewing" : "No actionable problems"}>
      {problems.length ? "Each row is derived from persisted state and disappears when its cause is fixed. Details are bounded; secrets are not included." : "Workflows, integrations, worker retries, backups, notifications and repairs report no current problem."}
    </InlineNotice>
    <Panel flush title="Problems" subtitle="Grouped by stable code. Counts collapse repeats so one root cause does not hide the rest." action={<RefreshButton onClick={refresh} />}>
      <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>Level</TableHead><TableHead>Area</TableHead><TableHead>Problem</TableHead><TableHead>Count</TableHead><TableHead>Oldest</TableHead><TableHead>Latest</TableHead><TableHead>Action</TableHead></TableRow></TableHeader><TableBody>
        {problems.map((problem) => <ProblemRow key={`${problem.code}-${problem.area}-${problem.title}`} problem={problem} />)}
      </TableBody></Table></div>
      {problems.length === 0 ? <div className="p-8 text-center text-[12px] text-fg-subtle">No actionable problems are recorded.</div> : null}
    </Panel>
    <Panel title="Support bundle" subtitle="A plain-text snapshot for debugging: version, schema, grouped problems, component state, recent backups and queue counts. Tokens and credentials are redacted.">
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="outline" disabled={copying} onClick={() => void copySupport()}><ClipboardCopy />{copying ? "Copying…" : "Copy for support"}</Button>
        <Button asChild size="sm" variant="outline"><a href="/api/v1/problems/support" download="media-manager-support.txt"><Download />Download text</a></Button>
      </div>
    </Panel>
  </> : null}</div>
}

function ProblemRow({ problem }: { problem: APIProblem }) {
  return <TableRow>
    <TableCell><StatusChip tone={problem.level === "error" ? "bad" : "warn"}>{problem.level === "error" ? "Error" : "Warning"}</StatusChip></TableCell>
    <TableCell className="capitalize">{problem.area}</TableCell>
    <TableCell className="max-w-xl whitespace-normal"><div className="font-semibold text-fg">{problem.title}</div>{problem.detail ? <div className="mt-1 text-[12px] leading-4 text-fg-subtle">{problem.detail}</div> : null}<div className="mt-1 font-mono text-[11px] text-fg-subtle">{problem.code}</div></TableCell>
    <TableCell>{problem.count}</TableCell>
    <TableCell>{problem.oldest_at ? timeAgo(problem.oldest_at) : "—"}</TableCell>
    <TableCell>{problem.latest_at ? timeAgo(problem.latest_at) : "—"}</TableCell>
    <TableCell>{problem.href ? <Link className="text-[12px] font-semibold text-info hover:underline" href={problem.href}>Open</Link> : "—"}</TableCell>
  </TableRow>
}
