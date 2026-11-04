import { useQuery } from "@tanstack/react-query"
import { CheckCircle2, Circle } from "lucide-react"
import { Link } from "react-router"

import { loadSetupStatus } from "@/api/client"
import { Badge, Card, CardHeader, ErrorBox, PageHeader, Progress } from "@/components/ui"
import { setupStepDetail, setupStepText } from "@/lib/setup"
import { ListSkeleton } from "@/pages/overview"

export function SetupPage() {
  const query = useQuery({ queryKey: ["setup", "status"], queryFn: ({ signal }) => loadSetupStatus(signal) })
  const required = query.data?.steps.filter((step) => !step.optional) ?? []
  const done = required.filter((step) => step.done).length
  return (
    <>
      <PageHeader title="Kom i gang" description="Det her skal være på plads, før Media Manager kan hente film og serier. Et punkt bliver grønt, når det er sat op." />
      {query.error ? <ErrorBox error={query.error} retry={() => query.refetch()} /> : null}
      {query.isPending ? <Card><ListSkeleton rows={7} /></Card> : null}
      {query.data ? (
        <Card>
          <CardHeader
            title={query.data.complete ? "Alt det nødvendige er klar" : `${done} af ${required.length} trin klaret`}
            description={query.data.complete ? "Media Manager kan hente film og serier. Valgfrie trin kan sættes op, når du vil." : "Tag trinnene i rækkefølge; hvert trin linker til siden, hvor det sættes op."}
          />
          <div className="px-5 pb-2"><Progress value={done} max={Math.max(required.length, 1)} tone={query.data.complete ? "good" : "info"} label="Trin klaret" /></div>
          <ol className="divide-y divide-line">
            {query.data.steps.map((step) => {
              const text = setupStepText[step.id]
              return (
                <li key={step.id} className="flex items-start gap-3 px-5 py-3">
                  {step.done ? <CheckCircle2 aria-label="Klaret" className="mt-0.5 size-5 shrink-0 text-good" /> : <Circle aria-label="Mangler" className="mt-0.5 size-5 shrink-0 text-text-3" />}
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">{text?.title ?? step.id}</span>
                      {step.optional ? <Badge>Valgfri</Badge> : null}
                    </div>
                    <div className="mt-0.5 break-words text-sm text-text-2">{setupStepDetail(step)}</div>
                  </div>
                  {text?.to && !step.done ? <Link to={text.to} aria-label={`Sæt op: ${text.title}`} className="shrink-0 text-sm font-medium text-accent-strong hover:underline">Sæt op</Link> : null}
                  {text?.to && step.done ? <Link to={text.to} aria-label={`Se: ${text.title}`} className="shrink-0 text-sm text-text-2 hover:underline">Se</Link> : null}
                </li>
              )
            })}
          </ol>
        </Card>
      ) : null}
      {query.data && !query.data.secret_storage_enabled ? (
        <div role="alert" className="mt-6 rounded-xl border border-warn/30 bg-warn-soft p-4 text-sm text-warn">
          Serveren kan ikke gemme nøgler fra hjemmesiden, fordi SECRET_ENCRYPTION_KEY mangler. Kør opsætningsscriptet igen, så bliver den lavet.
        </div>
      ) : null}
    </>
  )
}
