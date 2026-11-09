import { useMutation, useQuery } from "@tanstack/react-query"
import { CheckCircle2, Circle } from "lucide-react"
import { useState } from "react"
import { Link } from "react-router"

import { checkIntegration, loadSetupStatus, loadSystemHealth, type APIConnectionCheck } from "@/api/client"
import { RecommendedSetupCard } from "@/components/recommended-setup"
import { Badge, Button, Card, CardHeader, ErrorBox, PageHeader, Progress } from "@/components/ui"
import { formatDateTime } from "@/format"
import { errorMessage } from "@/i18n"
import { checkText, connectionStepKinds, setupStepDetail, setupStepText } from "@/lib/setup"
import { ListSkeleton } from "@/pages/overview"

export function SetupPage() {
  const query = useQuery({ queryKey: ["setup", "status"], queryFn: ({ signal }) => loadSetupStatus(signal) })
  const required = query.data?.steps.filter((step) => !step.optional) ?? []
  const done = required.filter((step) => step.done).length
  const stepDone = (id: string) => query.data?.steps.some((step) => step.id === id && step.done) ?? false
  const librariesMissing = query.data ? ["movie_root", "series_root", "movie_profile", "series_profile"].some((id) => !stepDone(id)) : false
  // Once shown, the card stays until the page is left, so its result is
  // still visible after it has made the steps green.
  const [showRecommended, setShowRecommended] = useState(false)
  if (librariesMissing && !showRecommended) setShowRecommended(true)
  const health = useQuery({ queryKey: ["health"], queryFn: ({ signal }) => loadSystemHealth(signal) })
  // A step can be tested when its default connection is enabled. The answer
  // below says what answered just now; the stored time says when it last did.
  const checkTargets = Object.entries(connectionStepKinds).flatMap(([step, kind]) => {
    const connection = health.data?.components.find((component) => component.kind === kind && component.enabled && component.is_default)
    return connection && stepDone(step) ? [{ step, id: connection.id, connection }] : []
  })
  const checks = useMutation({
    mutationFn: () => Promise.all(checkTargets.map(async ({ step, id }): Promise<[string, APIConnectionCheck]> => {
      try {
        return [step, await checkIntegration(id)]
      } catch (error) {
        return [step, { ok: false, detail: errorMessage(error) }]
      }
    })).then((results) => new Map(results)),
  })
  return (
    <>
      <PageHeader title="Kom i gang" description="Grønt viser gemt opsætning. Tryk Test forbindelser for at se, om de svarer lige nu, og kontrollér en første titel i Plex for at afprøve hele forløbet." />
      {query.error ? <ErrorBox error={query.error} retry={() => query.refetch()} /> : null}
      {query.isPending ? <Card><ListSkeleton rows={7} /></Card> : null}
      {query.data && showRecommended ? <RecommendedSetupCard ready={stepDone("plex") && stepDone("altmount")} /> : null}
      {query.data ? (
        <Card>
          <CardHeader
            title={query.data.complete ? "Den nødvendige opsætning er gemt" : `${done} af ${required.length} trin sat op`}
            description={query.data.complete ? "Gemte forbindelser er ikke en bekræftelse af, at de virker. Test dem her. Valgfrie trin kan sættes op, når du vil." : "Tag trinnene i rækkefølge; hvert trin linker til siden, hvor det sættes op."}
            action={checkTargets.length > 0 ? <Button busy={checks.isPending} onClick={() => checks.mutate()}>Test forbindelser</Button> : undefined}
          />
          <div className="px-5 pb-2"><Progress value={done} max={Math.max(required.length, 1)} tone={query.data.complete ? "good" : "info"} label="Trin sat op" /></div>
          <ol className="divide-y divide-line">
            {query.data.steps.map((step) => {
              const text = setupStepText[step.id]
              const check = checks.data?.get(step.id)
              const target = checkTargets.find((entry) => entry.step === step.id)
              return (
                <li key={step.id} className="flex items-start gap-3 px-5 py-3">
                  {step.done ? <CheckCircle2 aria-label="Sat op" className="mt-0.5 size-5 shrink-0 text-good" /> : <Circle aria-label="Mangler" className="mt-0.5 size-5 shrink-0 text-text-3" />}
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">{text?.title ?? step.id}</span>
                      {step.optional ? <Badge>Valgfri</Badge> : null}
                    </div>
                    <div className="mt-0.5 break-words text-sm text-text-2">{setupStepDetail(step)}</div>
                    {check ? <div className={`mt-0.5 break-words text-sm ${check.ok ? "text-good" : "text-bad"}`}>{check.ok ? "Testet lige nu: virker." : "Testet lige nu: virker ikke."} {checkText(check.detail)}</div> : null}
                    {!check && target ? <div className="mt-0.5 text-sm text-text-3">{target.connection.last_check_at ? `Testet senest ${formatDateTime(target.connection.last_check_at)}: ${target.connection.last_check_ok ? "virkede" : "virkede ikke"}.` : "Ikke testet endnu"}</div> : null}
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
          Serveren kan ikke gemme nøgler fra hjemmesiden, fordi SECRET_ENCRYPTION_KEY mangler. Kør opsætningsscriptet igen, så bliver den lavet. Installeret fra Cosmos-markedet før november 2026? Sæt SECRET_ENCRYPTION_KEY på API-containeren til resultatet af <code>openssl rand -hex 32</code>.
        </div>
      ) : null}
    </>
  )
}
