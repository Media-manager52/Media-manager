import { useQuery } from "@tanstack/react-query"
import { CheckCircle2, ChevronRight, Clock, Download, Search, XCircle } from "lucide-react"
import { useState, type ReactNode } from "react"
import { Link, useSearchParams } from "react-router"

import { loadAttentionWorkflows, loadWorkflowsByState, type APIWorkflow } from "@/api/client"
import { Poster, sizedArtwork } from "@/components/poster"
import { Button, Card, Dot, Empty, ErrorBox, PageHeader, Progress, SearchField, Segmented } from "@/components/ui"
import { formatRelative } from "@/format"
import { attentionLabel, stateLabel, stateTone, workflowDisplayState } from "@/i18n"
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
const emptyStates: Record<Tab, { title: string; text: string; icon: ReactNode }> = {
  henter: { title: "Intet hentes lige nu", text: "Når en søgning finder en egnet udgivelse, kan du følge hentningen her. Titler, der venter på en udgivelse, ligger under Venter.", icon: <Download className="size-8" /> },
  handling: { title: "Intet kræver dig", text: "Alt arbejde kører eller venter helt normalt.", icon: <CheckCircle2 className="size-8 text-good" /> },
  venter: { title: "Intet venter", text: "Ingen titler venter på en udgivelse lige nu.", icon: <Clock className="size-8" /> },
  afventer: { title: "Intet afventer", text: "Ingen titler holdes tilbage og venter på en bedre udgivelse.", icon: <Clock className="size-8" /> },
  faerdig: { title: "Intet er færdigt endnu", text: "Titler, der er hentet, kontrolleret og bekræftet i Plex, kommer her.", icon: <CheckCircle2 className="size-8" /> },
  annulleret: { title: "Intet er annulleret", text: "Arbejde, du eller Seerr har stoppet, kommer her.", icon: <XCircle className="size-8" /> },
}
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
        {!query.isPending && items.length === 0 && !query.error ? (
          needle
            ? <Empty title="Ingen titler passer til filteret" icon={<Search className="size-8" />}>Prøv et andet navn{paged ? ", eller se næste side" : ""}.</Empty>
            : <Empty title={emptyStates[tab].title} icon={emptyStates[tab].icon}>{emptyStates[tab].text}</Empty>
        ) : null}
        {items.length > 0 ? (
          <div aria-hidden className="hidden grid-cols-[2.25rem_minmax(0,1fr)_10rem_9rem_9rem_1rem] items-center gap-4 border-b border-line px-4 py-2.5 text-xs font-medium text-text-3 md:grid">
            <span />
            <span>Titel</span>
            <span>Fremdrift</span>
            <span>Status</span>
            <span>Ændret</span>
            <span />
          </div>
        ) : null}
        <ul className="divide-y divide-line">
          {items.map((workflow) => <WorkRow key={workflow.id} workflow={workflow} tab={tab} />)}
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

/** One row of work, dense like a download list: small poster, title, progress, status and age. */
function WorkRow({ workflow, tab }: { workflow: APIWorkflow; tab: Tab }) {
  const state = workflowDisplayState(workflow)
  const progress = workProgress(workflow, tab)
  return (
    <li>
      <Link to={workflowHref(workflow)} className="grid grid-cols-[2.25rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-4 py-2.5 hover:bg-surface-2 md:grid-cols-[2.25rem_minmax(0,1fr)_10rem_9rem_9rem_1rem] md:gap-x-4">
        <Poster compact src={sizedArtwork(workflow.poster_url, "w185")} title={workflow.title} kind={workflow.media_kind} className="w-9 rounded-md" />
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{workflow.title}{workflow.year ? <span className="font-normal text-text-3"> ({workflow.year})</span> : null}</div>
          <div className="truncate text-xs text-text-2">{describeVariant(workflow)}</div>
          {progress ? <div className="mt-1 md:hidden">{progress}</div> : null}
        </div>
        <div className="hidden min-w-0 md:block">{progress}</div>
        <span className="flex min-w-0 items-center gap-1.5 text-xs font-medium text-text-2"><Dot tone={stateTone(state)} /><span className="truncate">{stateLabel(state)}</span></span>
        <span className="hidden text-xs text-text-3 md:block">{formatRelative(workflow.updated_at)}</span>
        <ChevronRight aria-hidden className="hidden size-4 shrink-0 text-text-3 md:block" />
      </Link>
    </li>
  )
}

/**
 * What the progress column says: AltMount's own percentage while a download is
 * queued (never an estimate), why the work waits on the owner, or when the
 * next search is due. Nothing when there is nothing to say.
 */
function workProgress(workflow: APIWorkflow, tab: Tab): ReactNode {
  if (workflow.download_percent !== undefined && workflow.state === "queued") {
    const percent = Math.max(0, Math.min(100, Math.round(workflow.download_percent)))
    return (
      <div className="flex items-center gap-2">
        <Progress value={percent} max={100} tone="good" label={`Hentet ${percent} %`} />
        <span className="tabular w-9 shrink-0 text-right text-xs text-text-2">{percent} %</span>
      </div>
    )
  }
  if (workflow.attention_code) return <span title={attentionLabel(workflow.attention_code)} className="block truncate text-xs text-warn">{attentionLabel(workflow.attention_code)}</span>
  if (workflow.next_rescan_at && tab === "venter") return <span className="block truncate text-xs text-text-3">Ny søgning {formatRelative(workflow.next_rescan_at)}</span>
  return null
}
