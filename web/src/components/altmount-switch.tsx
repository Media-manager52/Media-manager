import { useMutation, useQueryClient } from "@tanstack/react-query"
import { ArrowRightLeft, CheckCircle2 } from "lucide-react"
import { useState } from "react"

import { ApiError, switchAltMount, type APIAltMountSwitchPlan } from "@/api/client"
import { CosmosTokenField } from "@/components/cosmos-token"
import { fieldClass } from "@/components/setup-forms"
import { Button } from "@/components/ui"
import { errorMessage } from "@/i18n"
import { altMountSwitchErrorText } from "@/lib/altmount-switch"

function errorText(error: unknown): string {
  if (error instanceof ApiError) return altMountSwitchErrorText(error.code, error.message)
  return errorMessage(error)
}

/**
 * "Skift til Media Managers AltMount": an existing normal AltMount gets
 * Media Manager's build through Cosmos. Only the program changes; settings,
 * database, files and drive stay. The owner pastes a Cosmos API token, sees
 * the change and confirms; the token is sent for that one request.
 */
export function AltMountSwitch({ version }: { version?: string }) {
  const queryClient = useQueryClient()
  const [token, setToken] = useState("")
  const [container, setContainer] = useState("")
  const [candidates, setCandidates] = useState<string[]>([])
  const request = (apply: boolean) => switchAltMount({ cosmos_host: window.location.host, cosmos_api_key: token.trim(), altmount_container: container || undefined, apply })
  const preview = useMutation({
    mutationFn: () => request(false),
    onSuccess: (result) => { if (result.problem === "choose_altmount") setCandidates(result.candidates ?? []) },
  })
  const apply = useMutation({
    mutationFn: () => request(true),
    onSuccess: async () => {
      setToken("")
      await queryClient.invalidateQueries({ queryKey: ["system"] })
    },
  })
  const plan: APIAltMountSwitchPlan | undefined = preview.data
  const error = apply.error ?? preview.error
  const choosing = plan?.problem === "choose_altmount"
  const ready = token.trim().length > 0
  const canApply = plan && !choosing && !plan.already_media_manager

  if (apply.data?.applied) {
    return (
      <div role="status" className="flex items-start gap-3 rounded-xl border border-good/40 bg-good/10 px-4 py-3 text-sm">
        <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-good" aria-hidden />
        <div>
          <div className="font-medium">AltMount kører nu Media Managers udgave{apply.data.new_version ? ` (${apply.data.new_version})` : ""}.</div>
          <p className="mt-1 text-text-2">Indstillinger, database og filer er de samme som før. En kopi af databasen og indstillingerne fra før skiftet ligger i AltMounts config-mappe under "before-media-manager-…".</p>
          <p className="mt-1 text-text-2">Cosmos-nøglen er ikke gemt. Du kan slette den i Cosmos nu.</p>
        </div>
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-line bg-surface-2 px-4 py-4 text-sm">
      <div className="flex items-center gap-2 font-medium"><ArrowRightLeft className="size-4" aria-hidden /> Skift til Media Managers AltMount</div>
      <p className="text-text-2">
        Du bruger den normale AltMount{version ? ` (${version})` : ""}. Media Managers udgave er den samme AltMount med rettelser: den henter en del fra næste server, når en server sender en forkert del, følger gamle RAR-pakker bedre, og drevet tåler fejl bedre.
      </p>
      <p className="text-text-2">
        Kun programmet bliver skiftet. Dine indstillinger, din database, dine filer og drevet bliver, hvor de er. Før den nye udgave starter første gang, tager den en kopi af databasen og indstillingerne. Kommer den ikke op, sætter Media Manager din gamle AltMount tilbage.
      </p>
      <CosmosTokenField value={token} onChange={(value) => { setToken(value); preview.reset() }} />
      {candidates.length > 0 ? (
        <label className="block">
          <span className="font-medium">Hvilken container er din AltMount?</span>
          <select value={container} onChange={(event) => { setContainer(event.target.value); preview.reset() }} className={fieldClass}>
            <option value="">Vælg …</option>
            {candidates.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </label>
      ) : null}
      {plan && !choosing ? (
        plan.already_media_manager ? (
          <p role="status" className="text-text-2">AltMount{plan.container ? ` (${plan.container})` : ""} kører allerede Media Managers udgave.</p>
        ) : (
          <div>
            <div className="font-medium">Det her bliver ændret i {plan.container}:</div>
            <ul className="mt-1 list-disc pl-5 text-text-2">
              <li className="[overflow-wrap:anywhere]">Programmet skiftes fra {plan.current_image} til {plan.new_image}.</li>
              <li>Indstillinger, database, filer, mapper og grænser bliver som før.</li>
            </ul>
            <p className="mt-1 text-text-3">AltMount genstarter (typisk under et minut). Imens kan der ikke afspilles fra drevet.</p>
          </div>
        )
      ) : null}
      {error ? <p role="alert" className="text-bad">{errorText(error)}</p> : null}
      <div className="flex flex-wrap gap-3">
        <Button variant={canApply ? "ghost" : "primary"} busy={preview.isPending} disabled={!ready || (candidates.length > 0 && !container)} onClick={() => { apply.reset(); preview.mutate() }}>
          Vis hvad der ændres
        </Button>
        {canApply ? <Button variant="primary" busy={apply.isPending} disabled={!ready} onClick={() => apply.mutate()}>Skift AltMount</Button> : null}
      </div>
    </div>
  )
}
