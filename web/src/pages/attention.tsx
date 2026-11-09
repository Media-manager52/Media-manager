import { useQuery } from "@tanstack/react-query"
import { CheckCircle2 } from "lucide-react"
import { useMemo, useState } from "react"
import { Link } from "react-router"

import { loadAttentionWorkflows, loadProblems, type APIWorkflow } from "@/api/client"
import { Badge, Card, CardHeader, Empty, ErrorBox, PageHeader, SearchField, ShowMore } from "@/components/ui"
import { WorkflowActions } from "@/components/workflow-actions"
import { formatRelative, pluralize } from "@/format"
import { attentionLabel, repairReasonLabel, stateLabel, translateDetail } from "@/i18n"
import { describeVariant, titleHref, workflowHref } from "@/lib/links"
import { useShowMore } from "@/lib/show-more"
import { ListSkeleton } from "@/pages/overview"

export function AttentionPage() {
  const workflows = useQuery({ queryKey: ["workflows", "attention"], queryFn: ({ signal }) => loadAttentionWorkflows(signal), refetchInterval: 60_000 })
  const problems = useQuery({ queryKey: ["problems"], queryFn: ({ signal }) => loadProblems(signal), refetchInterval: 120_000 })
  const [query, setQuery] = useState("")
  const groups = useMemo(() => groupByReason(filterByTitle(workflows.data ?? [], query)), [workflows.data, query])
  const systemProblems = (problems.data?.problems ?? []).filter((problem) => problem.area !== "workflows")
  const total = workflows.data?.length ?? 0
  return (
    <>
      <PageHeader
        title="Kræver handling"
        description="Her er alt, der ikke kan komme videre af sig selv. Hver sag viser, hvad der skete, og hvad du kan gøre."
        actions={<SearchField value={query} onChange={setQuery} placeholder="Søg i titler" label="Søg i sager" />}
      />
      {workflows.error ? <ErrorBox error={workflows.error} retry={() => workflows.refetch()} /> : null}
      {problems.error ? <div className="mb-6"><ErrorBox error={problems.error} retry={() => problems.refetch()} /></div> : null}
      {systemProblems.length > 0 ? (
        <Card className="mb-6">
          <CardHeader title="Systemet" description="Problemer, der ikke hører til én bestemt titel." />
          <ul className="divide-y divide-line">
            {systemProblems.map((problem) => (
              <li key={problem.code} className="flex flex-wrap items-start gap-3 px-5 py-3">
                <Badge tone={problem.level === "error" ? "bad" : "warn"}>{problem.count}</Badge>
                <div className="min-w-0 flex-1">
                  <div className="font-medium">{problemTitle(problem.title)}</div>
                  {problemDetail(problem.detail) ? <div className="text-sm text-text-2">{problemDetail(problem.detail)}</div> : null}
                </div>
                {problem.latest_at ? <span className="text-xs text-text-3">{formatRelative(problem.latest_at)}</span> : null}
              </li>
            ))}
          </ul>
        </Card>
      ) : null}
      {workflows.isPending ? <Card><ListSkeleton rows={6} /></Card> : null}
      {!workflows.isPending && total === 0 && !workflows.error ? (
        systemProblems.length > 0
          ? <Card><Empty title="Ingen titler kræver dig">Alt arbejde kører eller venter helt normalt. Se systemets problemer ovenfor.</Empty></Card>
          : <Card><Empty title="Intet kræver dig" icon={<CheckCircle2 className="size-8 text-good" />}>Alt arbejde kører eller venter helt normalt på nye udgivelser.</Empty></Card>
      ) : null}
      {total > 0 && groups.length === 0 ? <Card><Empty title="Ingen sager passer til søgningen" /></Card> : null}
      <div className="flex flex-col gap-6">
        {groups.map(([reason, items]) => <AttentionGroup key={reason} reason={reason} items={items} />)}
      </div>
    </>
  )
}

function AttentionGroup({ reason, items }: { reason: string; items: APIWorkflow[] }) {
  const { shown, remaining, showMore } = useShowMore(items, 20)
  return (
    <Card>
      <CardHeader title={reason} description={pluralize(items.length, "sag", "sager")} />
      <ul className="divide-y divide-line">
        {shown.map((workflow) => <AttentionItem key={workflow.id} workflow={workflow} />)}
      </ul>
      <ShowMore remaining={remaining} onClick={showMore} />
    </Card>
  )
}

function AttentionItem({ workflow }: { workflow: APIWorkflow }) {
  return (
    <li className="px-5 py-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <Link to={titleHref(workflow.media_id)} className="font-medium hover:underline">
            {workflow.title}{workflow.year ? <span className="font-normal text-text-3"> ({workflow.year})</span> : null}
          </Link>
          <div className="text-sm text-text-2">
            {describeVariant(workflow)} · {stateLabel(workflow.state)} · ændret {formatRelative(workflow.updated_at)}
          </div>
        </div>
        <Link to={workflowHref(workflow)} className="text-sm font-medium text-accent-strong hover:underline">Detaljer</Link>
      </div>
      {workflow.attention_detail ? <p className="mt-2 break-words text-sm text-text-2">{translateDetail(workflow.attention_detail)}</p> : null}
      <div className="mt-3">
        <WorkflowActions workflow={workflow} compact />
      </div>
    </li>
  )
}

const workerTasks: Record<string, string> = {
  "Search requested": "søgning",
  "Release.retrieve requested": "hentning af NZB",
  "Altmount.reconcile requested": "tjek af AltMount",
  "Library.publish requested": "udgivelse til biblioteket",
  "Plex.scan requested": "Plex-scanning",
}

/** Danish for the system problems; anything else is shown as written. */
function problemTitle(title: string): string {
  const retrying = title.match(/^Worker retrying: (.+)$/)
  if (retrying) return `Baggrundsopgave prøver igen: ${workerTasks[retrying[1]] ?? retrying[1]}`
  const repair = title.match(/^Repair: (.+)$/)
  if (repair) return `Reparation: ${repairReasonLabel(repair[1].toLowerCase().replaceAll(" ", "_"))}`
  return title
}

function problemDetail(detail: string | undefined): string {
  // The worker records only an error's Go type when it has no safe code;
  // that says nothing to a person.
  if (!detail || /^\*?[a-z][a-z0-9]*\.[A-Za-z][A-Za-z0-9]*$/.test(detail)) return ""
  return translateDetail(detail)
}

function filterByTitle(items: APIWorkflow[], query: string): APIWorkflow[] {
  const needle = query.trim().toLocaleLowerCase("da")
  if (!needle) return items
  return items.filter((item) => item.title.toLocaleLowerCase("da").includes(needle))
}

function groupByReason(items: APIWorkflow[]): Array<[string, APIWorkflow[]]> {
  const groups = new Map<string, APIWorkflow[]>()
  for (const item of items) {
    const reason = attentionLabel(item.attention_code) || stateLabel(item.state)
    const list = groups.get(reason) ?? []
    list.push(item)
    groups.set(reason, list)
  }
  return [...groups.entries()].sort((a, b) => b[1].length - a[1].length)
}
