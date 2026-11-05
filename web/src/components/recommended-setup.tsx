import { useMutation, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, CheckCircle2, Circle, PlusCircle } from "lucide-react"

import { ApiError, runRecommendedSetup, type APIRecommendedReport, type APIRecommendedStep } from "@/api/client"
import { Button, Card, CardHeader } from "@/components/ui"
import { recommendedText, recommendedTitle } from "@/lib/setup"

function setupErrorText(error: unknown): string {
  if (error instanceof ApiError && error.code === "database_error") return "Den nuværende opsætning kunne ikke læses. Prøv igen om lidt."
  if (error instanceof ApiError && error.code === "recommended_setup_unavailable") return "Den anbefalede opsætning er ikke tilgængelig på denne server."
  return error instanceof Error ? error.message : String(error)
}

function StepIcon({ step }: { step: APIRecommendedStep }) {
  if (step.state === "blocked") return <AlertTriangle aria-label="Kan ikke" className="mt-0.5 size-5 shrink-0 text-warn" />
  if (step.state === "create") return <PlusCircle aria-label="Bliver oprettet" className="mt-0.5 size-5 shrink-0 text-accent" />
  if (step.state === "created") return <CheckCircle2 aria-label="Oprettet" className="mt-0.5 size-5 shrink-0 text-good" />
  return <Circle aria-label="Findes allerede" className="mt-0.5 size-5 shrink-0 text-text-3" />
}

function Steps({ report }: { report: APIRecommendedReport }) {
  return (
    <ul className="divide-y divide-line">
      {report.steps.map((step) => (
        <li key={step.id} className="flex items-start gap-3 px-5 py-3">
          <StepIcon step={step} />
          <div className="min-w-0 flex-1">
            <div className="font-medium">{recommendedTitle(step)}</div>
            <div className="mt-0.5 break-words text-sm text-text-2">{recommendedText(step)}</div>
            {step.root && step.source_proven === false ? <div className="mt-0.5 text-xs text-text-3">AltMount-mappen er tom endnu, så det kunne kun kontrolleres, at Plex kan se mappen {step.root.source_visible_root}.</div> : null}
          </div>
        </li>
      ))}
    </ul>
  )
}

/**
 * "Brug anbefalet opsætning": shows what Media Manager can set up by itself
 * (library folders proven with Plex, the Danish quality profile) and creates
 * it after the owner has seen the proposal.
 */
export function RecommendedSetupCard({ ready }: { ready: boolean }) {
  const queryClient = useQueryClient()
  const preview = useMutation({ mutationFn: () => runRecommendedSetup(false) })
  const apply = useMutation({
    mutationFn: () => runRecommendedSetup(true),
    onSuccess: async () => {
      await Promise.all([queryClient.invalidateQueries({ queryKey: ["setup"] }), queryClient.invalidateQueries({ queryKey: ["settings"] })])
    },
  })
  const report = apply.data ?? preview.data
  const creatable = preview.data?.steps.some((step) => step.state === "create") ?? false
  const error = apply.error ?? preview.error
  return (
    <Card className="mb-6">
      <CardHeader
        title="Brug anbefalet opsætning"
        description={ready
          ? "Media Manager finder selv Plex-bibliotekerne og mapperne, og laver profilen \"Dansk tekst eller tale\". Du ser forslaget, før noget bliver oprettet. Det, du allerede har sat op, bliver ikke ændret."
          : "Forbind Plex og AltMount først. Derefter kan Media Manager selv sætte biblioteker og kvalitetsprofiler op."}
        action={!apply.data
          ? <Button variant={preview.data ? "ghost" : "primary"} busy={preview.isPending} disabled={!ready} onClick={() => { apply.reset(); preview.mutate() }}>{preview.data ? "Tjek igen" : "Vis forslag"}</Button>
          : null}
      />
      {report ? <Steps report={report} /> : null}
      {error ? <p role="alert" className="px-5 pb-4 text-sm text-bad">{setupErrorText(error)}</p> : null}
      {preview.data && !apply.data ? (
        <div className="flex flex-wrap items-center gap-3 px-5 pb-5">
          {creatable ? <Button variant="primary" busy={apply.isPending} onClick={() => apply.mutate()}>Opret det</Button> : <span className="text-sm text-text-2">Der er intet at oprette.</span>}
        </div>
      ) : null}
      {apply.data ? <p role="status" className="px-5 pb-5 text-sm text-text-2">Færdig. Du kan altid rette biblioteker og profiler under Indstillinger.</p> : null}
    </Card>
  )
}
