import { useMutation, useQueryClient } from "@tanstack/react-query"
import { CheckCircle2, Wrench } from "lucide-react"
import { useState } from "react"

import { ApiError, fixPlexAccess, type APIPlexAccessPlan } from "@/api/client"
import { fieldClass } from "@/components/setup-forms"
import { Button } from "@/components/ui"
import { errorMessage } from "@/i18n"
import { plexAccessChangeText, plexAccessErrorText } from "@/lib/plex-access"

function errorText(error: unknown): string {
  if (error instanceof ApiError) return plexAccessErrorText(error.code, error.message)
  return errorMessage(error)
}

/**
 * "Ret Plex": Media Manager gives Plex AltMount's drive and its library
 * folders through Cosmos. The owner pastes a Cosmos API token, sees what will
 * change, and confirms; the token is sent for that one request and kept only
 * in this form.
 */
export function PlexAccessFix({ onDone }: { onDone?: () => void }) {
  const queryClient = useQueryClient()
  const [token, setToken] = useState("")
  const [container, setContainer] = useState("")
  const [candidates, setCandidates] = useState<string[]>([])
  const request = (apply: boolean) => fixPlexAccess({ cosmos_host: window.location.host, cosmos_api_key: token.trim(), plex_container: container || undefined, apply })
  const preview = useMutation({
    mutationFn: () => request(false),
    onSuccess: (result) => { if (result.problem === "choose_plex") setCandidates(result.candidates ?? []) },
  })
  const apply = useMutation({
    mutationFn: () => request(true),
    onSuccess: async () => {
      setToken("")
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["setup"] }),
        queryClient.invalidateQueries({ queryKey: ["system"] }),
        queryClient.invalidateQueries({ queryKey: ["settings"] }),
      ])
      onDone?.()
    },
  })
  const plan: APIPlexAccessPlan | undefined = apply.data ?? preview.data
  const error = apply.error ?? preview.error
  const choosing = plan?.problem === "choose_plex"
  const ready = token.trim().length > 0

  if (apply.data?.applied) {
    return (
      <div role="status" className="flex items-start gap-3 rounded-xl border border-good/40 bg-good/10 px-4 py-3 text-sm">
        <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-good" aria-hidden />
        <div>
          <div className="font-medium">Plex er rettet.</div>
          <ul className="mt-1 list-disc pl-5 text-text-2">{apply.data.changes.map((change) => <li key={`${change.kind}-${change.path}`}>{plexAccessChangeText(change)}</li>)}</ul>
          <p className="mt-1 text-text-2">Cosmos-nøglen er ikke gemt. Du kan slette den i Cosmos nu.</p>
        </div>
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-line bg-surface-2 px-4 py-4 text-sm">
      <div className="flex items-center gap-2 font-medium"><Wrench className="size-4" aria-hidden /> Lad Media Manager rette Plex</div>
      <p className="text-text-2">
        Media Manager kan give Plex de mapper, den mangler, gennem Cosmos: AltMounts filer (så Plex beholder dem, når AltMount genstarter) og Media Managers biblioteksmapper. Plex genstarter én gang, og alt andet i Plex bliver som før.
      </p>
      <ol className="list-decimal pl-5 text-text-2">
        <li>Åbn Cosmos → Settings → fanen "API", og tryk "New Token". Lad "Read Only" være slået fra, og vælg gerne udløb "7 days".</li>
        <li>Indsæt nøglen her. Den bruges kun nu og bliver ikke gemt.</li>
      </ol>
      <label className="block">
        <span className="font-medium">Cosmos API-nøgle</span>
        <input type="password" autoComplete="off" value={token} onChange={(event) => { setToken(event.target.value); preview.reset() }} className={`${fieldClass} font-mono`} />
      </label>
      {candidates.length > 0 ? (
        <label className="block">
          <span className="font-medium">Hvilken container er din Plex?</span>
          <select value={container} onChange={(event) => { setContainer(event.target.value); preview.reset() }} className={fieldClass}>
            <option value="">Vælg …</option>
            {candidates.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </label>
      ) : null}
      {preview.data && !choosing ? (
        preview.data.changes.length === 0 ? (
          <p role="status" className="text-text-2">Plex har allerede alt, hvad Media Manager kan give den{preview.data.plex_container ? ` (${preview.data.plex_container})` : ""}.</p>
        ) : (
          <div>
            <div className="font-medium">Det her bliver ændret i {preview.data.plex_container}:</div>
            <ul className="mt-1 list-disc pl-5 text-text-2">{preview.data.changes.map((change) => <li key={`${change.kind}-${change.path}`}>{plexAccessChangeText(change)}</li>)}</ul>
            {preview.data.restart ? <p className="mt-1 text-text-3">Plex genstarter (typisk under et minut). Går noget galt, sætter Cosmos selv Plex tilbage.</p> : null}
          </div>
        )
      ) : null}
      {error ? <p role="alert" className="text-bad">{errorText(error)}</p> : null}
      <div className="flex flex-wrap gap-3">
        <Button variant={preview.data && !choosing && preview.data.changes.length > 0 ? "ghost" : "primary"} busy={preview.isPending} disabled={!ready || (candidates.length > 0 && !container)} onClick={() => { apply.reset(); preview.mutate() }}>
          Vis hvad der ændres
        </Button>
        {preview.data && !choosing && preview.data.changes.length > 0 ? (
          <Button variant="primary" busy={apply.isPending} disabled={!ready} onClick={() => apply.mutate()}>Ret Plex</Button>
        ) : null}
      </div>
    </div>
  )
}
