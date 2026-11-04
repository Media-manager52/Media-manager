import { useQuery } from "@tanstack/react-query"
import { Check } from "lucide-react"
import { Link, useParams } from "react-router"

import { loadWorkflow, loadWorkflowCandidates, loadWorkflowSearches, type APIWorkflow, type APIWorkflowCandidate } from "@/api/client"
import { Badge, Card, CardHeader, cx, Empty, ErrorBox, KeyValue, PageHeader, Progress, Skeleton } from "@/components/ui"
import { WorkflowActions } from "@/components/workflow-actions"
import { formatBytes, formatDateTime, formatRelative } from "@/format"
import { attentionLabel, humanize, stateLabel, stateTone, translateDetail, workflowDisplayState } from "@/i18n"
import { candidateReasonText } from "@/lib/candidate-reasons"
import { describeVariant, titleHref } from "@/lib/links"
import type { Tone } from "@/lib/tone"
import { workflowStep } from "@/lib/workflow-presentation"
import { ListSkeleton } from "@/pages/overview"

const steps = ["Søg", "Vælg", "Send", "Hent", "Importér", "Kontrollér", "Udgiv", "Plex"]

export function WorkflowPage() {
  const { id = "" } = useParams()
  const workflow = useQuery({ queryKey: ["workflow", id], queryFn: ({ signal }) => loadWorkflow(id, signal), refetchInterval: 20_000 })
  if (workflow.error) return <ErrorBox error={workflow.error} retry={() => workflow.refetch()} />
  if (!workflow.data) return <Skeleton className="h-64" />
  const item = workflow.data
  return (
    <>
      <PageHeader
        eyebrow={<Link to={titleHref(item.media_id)} className="hover:underline">← {item.title}{item.year ? ` (${item.year})` : ""}</Link>}
        title={`${item.title} · ${describeVariant(item)}`}
        actions={<Badge tone={stateTone(workflowDisplayState(item))} className="h-7 px-3 text-sm">{stateLabel(workflowDisplayState(item))}</Badge>}
      />
      <Stepper state={workflowDisplayState(item)} />
      {item.attention_code || item.attention_detail ? (
        <div className="mb-6 rounded-2xl border border-warn/30 bg-warn-soft p-4">
          <div className="font-medium text-warn">{attentionLabel(item.attention_code) || "Kræver handling"}</div>
          {item.attention_detail ? <p className="mt-1 break-words text-sm text-text">{translateDetail(item.attention_detail)}</p> : null}
        </div>
      ) : null}
      <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <Card>
            <CardHeader title="Hvad kan du gøre?" description="Serveren tjekker alt igen, når du trykker." />
            <div className="px-5 py-4">
              {(item.available_actions ?? []).length === 0 ? <p className="text-sm text-text-2">Ingen handlinger for dette arbejde.</p> : <WorkflowActions workflow={item} showUnavailable />}
            </div>
          </Card>
          <SearchHistory id={item.id} />
          <Candidates id={item.id} />
        </div>
        <Facts workflow={item} />
      </div>
    </>
  )
}

function Stepper({ state }: { state: string }) {
  const { current, complete } = workflowStep(state)
  if (current < 0) return null
  return (
    <ol className="mb-6 grid grid-cols-4 gap-2 sm:grid-cols-8" aria-label="Fremskridt">
      {steps.map((label, index) => {
        const done = complete || index < current
        const active = !complete && index === current
        return (
          <li key={label} className="flex flex-col gap-1.5" aria-current={active ? "step" : undefined}>
            <div className={cx("h-1.5 rounded-full", done ? "bg-good" : active ? "bg-info" : "bg-surface-3")} />
            <span className={cx("flex items-center gap-1 text-xs", active ? "font-medium text-text" : "text-text-3")}>
              {done ? <Check aria-hidden className="size-3 text-good" /> : null}
              {label}
            </span>
          </li>
        )
      })}
    </ol>
  )
}

function Facts({ workflow: w }: { workflow: APIWorkflow }) {
  const items: Array<[string, React.ReactNode]> = [
    ["Kvalitetsprofil", w.quality_profile ?? "—"],
    ["Runde", w.round_ordinal ? `${w.round_ordinal}${w.round_state ? ` · ${roundState(w.round_state)}` : ""}` : "—"],
    ["Forsøg i runden", w.round_attempt_limit ? `${w.round_slots_used ?? 0} af ${w.round_attempt_limit}` : "—"],
    ["Fejlede filer gemt", w.retained_output_limit ? `${w.retained_output_count ?? 0} af ${w.retained_output_limit} (${formatBytes(w.retained_output_bytes)})` : "—"],
    ["Udelukkede udgivelser", String(w.excluded_release_count ?? 0)],
    ["Seneste opdagelse", w.last_discovery_at ? formatRelative(w.last_discovery_at) : "—"],
    ["Næste søgning", w.next_rescan_at ? formatRelative(w.next_rescan_at) : "—"],
    ["Oprettet", formatDateTime(w.created_at)],
    ["Ændret", formatDateTime(w.updated_at)],
  ]
  if (w.ready_release_title) items.splice(5, 0, ["Ny udgivelse klar", `${w.ready_release_title}${w.ready_indexer_name ? ` (${w.ready_indexer_name})` : ""}`])
  return (
    <Card className="self-start">
      <CardHeader title="Detaljer" />
      <div className="space-y-4 px-5 py-4">
        {w.download_percent !== undefined ? (
          <div>
            <div className="mb-1.5 flex justify-between text-sm"><span>Hentning{w.download_stage ? ` · ${humanize(w.download_stage)}` : ""}</span><span className="tabular">{Math.round(w.download_percent)} %</span></div>
            <Progress value={w.download_percent} max={100} label="Hentning" />
          </div>
        ) : null}
        <KeyValue items={items} />
      </div>
    </Card>
  )
}

function roundState(state: string): string {
  return { open: "åben", exhausted: "brugt op", closed: "lukket" }[state] ?? state
}

function SearchHistory({ id }: { id: string }) {
  const query = useQuery({ queryKey: ["workflow", id, "searches"], queryFn: ({ signal }) => loadWorkflowSearches(id, signal) })
  const runs = query.data ?? []
  return (
    <Card>
      <CardHeader title="Søgninger" description="De seneste søgninger og hvad hver indexer svarede." />
      {query.isPending ? <ListSkeleton rows={3} /> : null}
      {query.error ? <div className="p-4"><ErrorBox error={query.error} /></div> : null}
      {!query.isPending && runs.length === 0 ? <Empty title="Ingen søgninger endnu" /> : null}
      <ul className="divide-y divide-line">
        {runs.map((run) => (
          <li key={run.id} className="px-5 py-3">
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="font-medium">{formatDateTime(run.started_at)}</span>
              <Badge tone={run.state === "complete" || run.state === "completed" ? "good" : run.state === "partial" ? "warn" : "neutral"}>{searchState(run.state)}</Badge>
              <span className="text-text-2">{run.new_candidates} nye udgivelser</span>
            </div>
            <div className="mt-2 flex flex-wrap gap-1.5">
              {run.indexers.map((indexer) => (
                <Badge key={indexer.indexer_id} tone={indexerTone(indexer.state)} className="font-normal">
                  <span title={indexer.detail}>{indexer.indexer_name.replace(/\s*\{DK\}\s*/i, "")}</span>
                </Badge>
              ))}
            </div>
          </li>
        ))}
      </ul>
    </Card>
  )
}

function searchState(state: string): string {
  return { complete: "Fuldført", completed: "Fuldført", partial: "Delvis", running: "Kører", failed: "Fejlet" }[state] ?? humanize(state)
}

function indexerTone(state: string): Tone {
  if (state === "succeeded" || state === "success") return "good"
  if (state === "deferred" || state === "budget_deferred" || state === "skipped") return "neutral"
  if (state === "failed" || state === "error") return "bad"
  return "neutral"
}

function Candidates({ id }: { id: string }) {
  const query = useQuery({ queryKey: ["workflow", id, "candidates"], queryFn: ({ signal }) => loadWorkflowCandidates(id, signal) })
  const items = [...(query.data ?? [])].sort(candidateOrder)
  return (
    <Card>
      <CardHeader title="Fundne udgivelser" description="Hvad søgningerne fandt, og hvad Media Manager besluttede om hver." />
      {query.isPending ? <ListSkeleton rows={3} /> : null}
      {!query.isPending && items.length === 0 ? <Empty title="Ingen udgivelser fundet endnu" /> : null}
      <ul className="divide-y divide-line">
        {items.slice(0, 60).map((candidate) => <CandidateRow key={candidate.id} candidate={candidate} />)}
      </ul>
    </Card>
  )
}

function CandidateRow({ candidate: c }: { candidate: APIWorkflowCandidate }) {
  const verdict = c.decision?.verdict ?? "unknown"
  const tone: Tone = c.selected ? "info" : c.exclusion ? "bad" : verdict === "eligible" ? "good" : verdict === "held" ? "warn" : "neutral"
  const label = c.selected ? "Valgt" : c.exclusion ? "Udelukket" : ({ eligible: "Egnet", rejected: "Afvist", held: "Holdt tilbage" } as Record<string, string>)[verdict] ?? humanize(verdict)
  const reasons = reasonText(c)
  return (
    <li className="px-5 py-3">
      <div className="flex flex-wrap items-start gap-2">
        <span className="min-w-0 flex-1 break-all text-sm font-medium">{c.title}</span>
        <Badge tone={tone}>{label}</Badge>
      </div>
      <div className="mt-1 text-xs text-text-3">
        {c.indexer_name.replace(/\s*\{DK\}\s*/i, "")} · {formatBytes(c.size_bytes)}{c.decision?.score !== undefined ? ` · score ${c.decision.score}` : ""}{c.attempted ? " · prøvet" : ""}
      </div>
      {c.exclusion ? <p className="mt-1 break-words text-xs text-bad">{c.exclusion.reason}</p> : reasons ? <p className="mt-1 break-words text-xs text-text-2">{reasons}</p> : null}
    </li>
  )
}

function reasonText(candidate: APIWorkflowCandidate): string {
  return candidateReasonText(candidate.decision?.verdict, candidate.decision?.decision_explanation?.reasons ?? [])
}

function candidateOrder(a: APIWorkflowCandidate, b: APIWorkflowCandidate): number {
  const rank = (c: APIWorkflowCandidate) => (c.selected ? 0 : c.decision?.verdict === "eligible" && !c.exclusion ? 1 : c.exclusion ? 3 : 2)
  return rank(a) - rank(b) || (b.decision?.score ?? 0) - (a.decision?.score ?? 0)
}
