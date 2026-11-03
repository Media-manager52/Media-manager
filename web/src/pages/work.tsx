import { useQuery } from "@tanstack/react-query"
import { ChevronRight } from "lucide-react"
import { useState } from "react"
import { Link, useSearchParams } from "react-router"

import { loadAttentionWorkflows, loadWorkflowsByState, type APIWorkflow } from "@/api/client"
import { Badge, Button, Card, Empty, ErrorBox, PageHeader, SearchField, Segmented } from "@/components/ui"
import { formatRelative } from "@/format"
import { attentionLabel, stateLabel, stateTone } from "@/i18n"
import { describeVariant, workflowHref } from "@/lib/links"
import { ListSkeleton } from "@/pages/overview"

type Tab = "henter" | "handling" | "venter" | "afventer" | "faerdig" | "annulleret"
const tabs: Array<{ value: Tab; label: string }> = [
  { value: "henter", label: "Henter nu" },
  { value: "handling", label: "Kræver handling" },
  { value: "venter", label: "Venter" },
  { value: "afventer", label: "Afventer" },
  { value: "faerdig", label: "I Plex" },
  { value: "annulleret", label: "Annulleret" },
]
const pageSize = 100
/** The persisted workflow states (internal/workflows) in which work is moving. */
const acquiringStates = ["searching", "candidate_selected", "submitting", "queued", "streaming_ready", "import_complete", "verifying", "publishing", "plex_scanning"]

async function loadTab(tab: Tab, offset: number, signal: AbortSignal): Promise<APIWorkflow[]> {
  switch (tab) {
    case "henter": {
      const states = acquiringStates
      const batches = await Promise.all(states.map((state) => loadWorkflowsByState({ state, active: true, limit: 200, offset: 0, signal })))
      return batches.flat().sort((a, b) => b.updated_at.localeCompare(a.updated_at))
    }
    case "handling":
      return await loadAttentionWorkflows(signal)
    case "venter":
      return await loadWorkflowsByState({ state: "waiting", active: true, limit: pageSize, offset, signal })
    case "afventer":
      return await loadWorkflowsByState({ state: "held", active: true, limit: pageSize, offset, signal })
    case "faerdig":
      return await loadWorkflowsByState({ state: "plex_confirmed", active: false, limit: pageSize, offset, signal })
    case "annulleret":
      return await loadWorkflowsByState({ state: "cancelled", active: false, limit: pageSize, offset, signal })
  }
}

export function WorkPage() {
  const [params, setParams] = useSearchParams()
  const tab = (tabs.some((entry) => entry.value === params.get("vis")) ? params.get("vis") : "henter") as Tab
  const [page, setPage] = useState(0)
  const [search, setSearch] = useState("")
  const paged = tab !== "henter" && tab !== "handling"
  const query = useQuery({
    queryKey: ["workflows", "tab", tab, page],
    queryFn: ({ signal }) => loadTab(tab, page * pageSize, signal),
    refetchInterval: tab === "henter" ? 15_000 : 60_000,
  })
  const needle = search.trim().toLocaleLowerCase("da")
  const items = (query.data ?? []).filter((item) => !needle || item.title.toLocaleLowerCase("da").includes(needle))
  return (
    <>
      <PageHeader title="Arbejde" description="Alt, Media Manager arbejder på: søgninger, hentninger, kontrol og udgivelse til Plex." />
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <Segmented label="Vis" value={tab} options={tabs} onChange={(value) => { setParams(value === "henter" ? {} : { vis: value }, { replace: true }); setPage(0) }} />
        <SearchField value={search} onChange={setSearch} placeholder={paged ? "Filtrér denne side" : "Filtrér titler"} label="Filtrér arbejde" />
      </div>
      {query.error ? <ErrorBox error={query.error} retry={() => query.refetch()} /> : null}
      <Card>
        {query.isPending ? <ListSkeleton rows={8} /> : null}
        {!query.isPending && items.length === 0 && !query.error ? <Empty title={tab === "henter" ? "Intet hentes lige nu" : "Ingen sager her"} /> : null}
        <ul className="divide-y divide-line">
          {items.map((workflow) => (
            <li key={workflow.id}>
              <Link to={workflowHref(workflow)} className="flex items-center gap-3 px-5 py-3 hover:bg-surface-2">
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium">{workflow.title}{workflow.year ? <span className="font-normal text-text-3"> ({workflow.year})</span> : null}</div>
                  <div className="truncate text-sm text-text-2">
                    {describeVariant(workflow)}
                    {workflow.attention_code ? ` · ${attentionLabel(workflow.attention_code)}` : ""}
                    {workflow.download_percent !== undefined && workflow.state === "queued" ? ` · ${Math.round(workflow.download_percent)} %` : ""}
                    {workflow.next_rescan_at && tab === "venter" ? ` · ny søgning ${formatRelative(workflow.next_rescan_at)}` : ""}
                  </div>
                </div>
                <span className="hidden text-xs text-text-3 sm:block">{formatRelative(workflow.updated_at)}</span>
                <Badge tone={stateTone(workflow.state)}>{stateLabel(workflow.state)}</Badge>
                <ChevronRight aria-hidden className="size-4 shrink-0 text-text-3" />
              </Link>
            </li>
          ))}
        </ul>
      </Card>
      {paged ? (
        <div className="mt-4 flex items-center justify-between">
          <Button disabled={page === 0} onClick={() => setPage((value) => value - 1)}>Forrige</Button>
          <span className="text-sm text-text-3">Side {page + 1}</span>
          <Button disabled={(query.data?.length ?? 0) < pageSize} onClick={() => setPage((value) => value + 1)}>Næste</Button>
        </div>
      ) : null}
    </>
  )
}
