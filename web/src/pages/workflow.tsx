import { useQuery } from "@tanstack/react-query"
import { Check, ChevronRight } from "lucide-react"
import { useEffect } from "react"
import { Link, useParams } from "react-router"

import { isNotFound, loadWorkflow, loadWorkflowCandidates, loadWorkflowDryRun, loadWorkflowSearches, loadWorkflowVerifications, type APIDryRunResult, type APIWorkflow, type APIWorkflowCandidate } from "@/api/client"
import { sizedArtwork } from "@/components/poster"
import { Badge, Button, Card, CardHeader, cx, Dot, Empty, ErrorBox, KeyValue, Progress, Skeleton } from "@/components/ui"
import { WorkflowActions } from "@/components/workflow-actions"
import { formatBytes, formatDateTime, formatNumber, formatRelative, pluralize } from "@/format"
import { attentionLabel, humanize, stateLabel, stateTone, translateDetail, workflowDisplayState } from "@/i18n"
import { candidateReasonText } from "@/lib/candidate-reasons"
import { describeVariant, titleHref } from "@/lib/links"
import type { Tone } from "@/lib/tone"
import { explainWorkflow, indexerLanguageNote, languageCheckLines } from "@/lib/workflow-explanation"
import { workflowStep } from "@/lib/workflow-presentation"
import { ListSkeleton } from "@/pages/overview"

const steps = ["Søg", "Vælg", "Send", "Hent", "Importér", "Kontrollér", "Udgiv", "Plex"]

export function WorkflowPage() {
  const { id = "" } = useParams()
  const workflow = useQuery({ queryKey: ["workflow", id], queryFn: ({ signal }) => loadWorkflow(id, signal), refetchInterval: 20_000 })
  if (isNotFound(workflow.error)) return <Card><Empty title="Dette arbejde findes ikke længere">Det kan være afsluttet og ryddet op, eller linket kan være gammelt. <Link to="/arbejde" className="font-medium text-accent-strong hover:underline">Se alt arbejde</Link></Empty></Card>
  if (workflow.error) return <ErrorBox error={workflow.error} retry={() => workflow.refetch()} />
  if (!workflow.data) return <Skeleton className="h-64" />
  const item = workflow.data
  return (
    <>
      <WorkflowHero workflow={item} />
      <Stepper state={workflowDisplayState(item)} />
      {item.attention_code || item.attention_detail ? (
        <div className="mb-6 rounded-2xl border border-warn/30 bg-warn-soft p-4">
          <div className="font-medium text-warn">{attentionLabel(item.attention_code) || "Kræver handling"}</div>
          {item.attention_detail ? <p className="mt-1 break-words text-sm text-text">{translateDetail(item.attention_detail)}</p> : null}
        </div>
      ) : null}
      <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <WorkflowExplanation workflow={item} />
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

/** The top of a work page: the title's wide picture with the part and its state on it. */
function WorkflowHero({ workflow }: { workflow: APIWorkflow }) {
  const heading = `${workflow.title} · ${describeVariant(workflow)}`
  useEffect(() => {
    document.title = `${heading} · Media Manager`
  }, [heading])
  const wide = sizedArtwork(workflow.backdrop_url, "w1280")
  const tall = sizedArtwork(workflow.poster_url, "w342")
  const state = workflowDisplayState(workflow)
  return (
    <>
      <nav aria-label="Brødkrumme" className="mb-3 flex items-center gap-1 text-sm text-text-3">
        <Link to="/arbejde" className="hover:text-text hover:underline">Arbejde</Link>
        <ChevronRight aria-hidden className="size-3.5" />
        <Link to={titleHref(workflow.media_id)} className="min-w-0 truncate hover:text-text hover:underline">{workflow.title}{workflow.year ? ` (${workflow.year})` : ""}</Link>
      </nav>
      <section aria-label={heading} className="relative mb-6 flex min-h-40 items-end overflow-hidden rounded-2xl bg-[#1d1d1f] shadow-card sm:min-h-52">
        {wide ? <img src={wide} alt="" className="absolute inset-0 size-full object-cover" /> : tall ? <img src={tall} alt="" className="absolute inset-0 size-full scale-110 object-cover opacity-60 blur-2xl" /> : null}
        <div aria-hidden className="absolute inset-0 bg-gradient-to-t from-black/85 via-black/35 to-black/10" />
        <div className="relative flex w-full flex-wrap items-end justify-between gap-3 p-5 sm:p-6">
          <h1 className="min-w-0 break-words text-2xl font-bold leading-tight tracking-tight text-white sm:text-3xl">{heading}</h1>
          <span className="inline-flex items-center gap-1.5 rounded-full bg-black/55 px-3 py-1 text-sm font-semibold text-white backdrop-blur"><Dot tone={stateTone(state)} />{stateLabel(state)}</span>
        </div>
      </section>
    </>
  )
}

function WorkflowExplanation({ workflow }: { workflow: APIWorkflow }) {
  const candidates = useQuery({ queryKey: ["workflow", workflow.id, "candidates"], queryFn: ({ signal }) => loadWorkflowCandidates(workflow.id, signal) })
  const verification = useQuery({ queryKey: ["workflow", workflow.id, "verification"], queryFn: ({ signal }) => loadWorkflowVerifications(workflow.id, signal) })
  const explanation = explainWorkflow(workflow, candidates.isError ? undefined : candidates.data)
  const latest = verification.data?.[0]
  const claim = verification.data && !latest && !workflow.terminal_at && !candidates.isError ? indexerLanguageNote(candidates.data) : undefined
  const rows: Array<[string, string]> = [["Hvorfor har jeg den ikke endnu?", explanation.why], ["Hvad gør Media Manager nu?", explanation.now], ["Skal jeg gøre noget?", explanation.you]]
  return (
    <Card>
      <CardHeader title="Status i klartekst" />
      <dl className="space-y-3 px-5 py-4 text-sm">
        {rows.map(([question, answer]) => <div key={question}><dt className="text-text-3">{question}</dt><dd className="mt-0.5 break-words">{answer}</dd></div>)}
        {latest ? (
          <div>
            <dt className="text-text-3">Sprogkontrol af seneste fil</dt>
            <dd className="mt-0.5 break-words">
              <Badge tone={latest.status === "verified" ? "good" : "warn"}>{latest.status === "verified" ? "Godkendt" : "Ikke godkendt"}</Badge>
              <ul className="mt-1 space-y-0.5">{languageCheckLines(latest).map((line) => <li key={line}>{line}</li>)}</ul>
            </dd>
          </div>
        ) : null}
        {claim ? <div><dt className="text-text-3">Sprog</dt><dd className="mt-0.5 break-words">{claim}</dd></div> : null}
        {verification.error ? <div><dt className="text-text-3">Sprogkontrol af seneste fil</dt><dd className="mt-0.5 text-warn">Kunne ikke hentes lige nu.</dd></div> : null}
      </dl>
    </Card>
  )
}

function dryRunText(result: APIDryRunResult): string {
  const usable = result.candidates.filter((candidate) => candidate.evaluation.verdict === "eligible" && !candidate.excluded).length
  const best = result.candidates.find((candidate) => candidate.candidate_id === result.best_eligible_candidate_id)
  return `Efter profilens nuværende regler kan ${formatNumber(usable)} af ${formatNumber(result.candidates.length)} kendte udgivelser bruges.${best ? ` Bedst: ${best.title}.` : ""}${result.truncated ? " Kun en del af udgivelserne er vurderet." : ""} Intet er hentet eller ændret.`
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
      {query.error ? <div className="p-4"><ErrorBox error={query.error} retry={() => query.refetch()} /></div> : null}
      {!query.isPending && !query.error && runs.length === 0 ? <Empty title="Ingen søgninger endnu" /> : null}
      <ul className="divide-y divide-line">
        {runs.map((run) => (
          <li key={run.id} className="px-5 py-3">
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="font-medium">{formatDateTime(run.started_at)}</span>
              <Badge tone={run.state === "complete" || run.state === "completed" ? "good" : run.state === "partial" ? "warn" : "neutral"}>{searchState(run.state)}</Badge>
              <span className="text-text-2">{pluralize(run.new_candidates, "ny udgivelse", "nye udgivelser")}</span>
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
  // Read-only: replays the stored releases against the profile, only on request.
  const dryRun = useQuery({ queryKey: ["workflow", id, "dry-run"], queryFn: ({ signal }) => loadWorkflowDryRun(id, signal), enabled: false, retry: false })
  const items = [...(query.data ?? [])].sort(candidateOrder)
  return (
    <Card>
      <CardHeader title="Fundne udgivelser" description="Hvad søgningerne fandt, og hvad Media Manager besluttede om hver." action={items.length > 0 ? <Button className="h-8 text-xs" busy={dryRun.isFetching} onClick={() => void dryRun.refetch()}>Vurder igen</Button> : undefined} />
      {dryRun.data ? <p role="status" className="border-b border-line px-5 py-3 text-sm">{dryRunText(dryRun.data)}</p> : null}
      {dryRun.error ? <div className="p-4"><ErrorBox error={dryRun.error} retry={() => void dryRun.refetch()} /></div> : null}
      {query.isPending ? <ListSkeleton rows={3} /> : null}
      {query.error ? <div className="p-4"><ErrorBox error={query.error} retry={() => query.refetch()} /></div> : null}
      {!query.isPending && !query.error && items.length === 0 ? <Empty title="Ingen udgivelser fundet endnu" /> : null}
      <ul className="divide-y divide-line">
        {items.slice(0, 60).map((candidate) => <CandidateRow key={candidate.id} candidate={candidate} />)}
      </ul>
      {items.length > 60 ? <p className="border-t border-line px-5 py-3 text-xs text-text-3">Viser de 60 bedste af {items.length} udgivelser.</p> : null}
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
      {c.exclusion ? <p className="mt-1 break-words text-xs text-bad">{translateDetail(c.exclusion.reason)}</p> : reasons ? <p className="mt-1 break-words text-xs text-text-2">{reasons}</p> : null}
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
