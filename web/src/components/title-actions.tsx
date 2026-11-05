import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Download, FolderInput, Plus, Trash2 } from "lucide-react"
import { useState } from "react"
import { useNavigate } from "react-router"

import { ApiError, createMediaWorkflow, loadDiscoveryMetadata, loadLibraryRoots, loadQualityProfiles, recycleMedia, relocateMedia, type APILibraryRoot, type APIMediaDetail, type APIQualityProfile } from "@/api/client"
import { useToast } from "@/components/toast"
import { Badge, Button, cx, Dialog } from "@/components/ui"
import { errorMessage } from "@/i18n"

/** Danish text for the refusals a move or a delete can meet. */
function actionError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === "workflow_in_flight") return "Noget af titlen hentes eller udgives lige nu. Prøv igen, når det er færdigt."
    if (error.code === "nothing_to_move") return "Titlen ligger allerede i den mappe."
    if (error.code === "library_root_disabled") return "Mappen er slået fra. Vælg en anden."
    if (error.code === "media_removed") return "Titlen er allerede slettet."
    if (error.code === "recycle_failed" && /adopted/i.test(error.message)) return "Titlen har filer, Media Manager har overtaget fra dit gamle bibliotek. De skal afklares, før titlen kan slettes."
  }
  return errorMessage(error)
}

/** Where the title lives now: the roots of its published files and open work. */
function currentRoots(detail: APIMediaDetail): { ids: Set<string>; editions: Set<string> } {
  const ids = new Set<string>()
  const editions = new Set<string>()
  for (const workflow of detail.workflows) {
    if (workflow.state === "cancelled" || !workflow.root_id) continue
    const published = detail.files.some((file) => file.state === "current" && file.variant === workflow.variant)
    if (workflow.terminal_at && !published) continue
    ids.add(workflow.root_id)
    editions.add(workflow.edition ?? "standard")
  }
  return { ids, editions }
}

export function TitleActions({ detail }: { detail: APIMediaDetail }) {
  const [dialog, setDialog] = useState<"get" | "move" | "delete" | null>(null)
  const series = detail.media.kind === "series"
  const inPlex = detail.files.some((file) => file.state === "current")
  // A film that is already being fetched shows Søg under Status instead; a
  // film in Plex can look for a better copy, which replaces it only when found.
  const movieOpen = !series && detail.workflows.some((workflow) => !workflow.terminal_at && workflow.variant === "default")
  const getLabel = series ? "Tilføj sæson" : inPlex ? "Find bedre udgave" : "Hent"
  return (
    <>
      {movieOpen && !inPlex ? null : (
        <Button variant={series || inPlex ? "secondary" : "primary"} className="h-8 text-xs" onClick={() => setDialog("get")}>{series ? <Plus aria-hidden className="size-3.5" /> : <Download aria-hidden className="size-3.5" />}{getLabel}</Button>
      )}
      <Button variant="secondary" className="h-8 text-xs" onClick={() => setDialog("move")}><FolderInput aria-hidden className="size-3.5" />Flyt</Button>
      <Button variant="ghost" className="h-8 text-xs text-bad" onClick={() => setDialog("delete")}><Trash2 aria-hidden className="size-3.5" />Slet</Button>
      {dialog === "get" ? <GetDialog detail={detail} onClose={() => setDialog(null)} /> : null}
      {dialog === "move" ? <MoveDialog detail={detail} onClose={() => setDialog(null)} /> : null}
      {dialog === "delete" ? <DeleteDialog detail={detail} onClose={() => setDialog(null)} /> : null}
    </>
  )
}

function MoveDialog({ detail, onClose }: { detail: APIMediaDetail; onClose: () => void }) {
  const { media } = detail
  const queryClient = useQueryClient()
  const toast = useToast()
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const [choice, setChoice] = useState("")
  const here = currentRoots(detail)
  const options = (roots.data ?? [])
    .filter((root) => root.enabled && root.media_kind === media.kind && here.editions.has(root.edition ?? "standard"))
    .sort((a, b) => a.name.localeCompare(b.name, "da"))
  const now = options.filter((root) => here.ids.has(root.id)).map((root) => root.name)
  const target = options.find((root) => root.id === choice)
  const mutation = useMutation({
    mutationFn: () => relocateMedia(media.id, choice),
    onSuccess: async () => {
      toast("good", `${media.title} er flyttet til ${target?.name ?? "den nye mappe"}. Plex opdaterer om lidt.`)
      onClose()
      await queryClient.invalidateQueries()
    },
  })
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Flyt ${media.title}`}
      footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!choice || here.ids.has(choice)} onClick={() => mutation.mutate()}>Flyt</Button></>}
    >
      <div className="space-y-3">
        <p>Vælg det bibliotek i Plex, titlen skal ligge i.{media.kind === "series" ? " Alle sæsoner flyttes, også dem der stadig ventes på." : ""}</p>
        {now.length ? <p className="text-text-3">Ligger nu i: {now.join(", ")}</p> : null}
        {here.ids.size === 0 ? <p className="text-warn">Titlen har ingen filer eller ventende arbejde, der kan flyttes.</p> : null}
        <div role="radiogroup" aria-label="Bibliotek" className="max-h-72 space-y-1 overflow-y-auto">
          {options.map((root) => {
            const current = here.ids.has(root.id)
            return (
              <label key={root.id} className={cx("flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2", choice === root.id ? "border-accent bg-accent-soft" : "border-line", current && "cursor-default opacity-60")}>
                <input type="radio" name="move-root" value={root.id} checked={choice === root.id} disabled={current} onChange={() => setChoice(root.id)} className="size-4" />
                <span className="min-w-0 flex-1">
                  <span className="font-medium text-text">{root.name}</span>
                  <span className="block break-all text-xs text-text-3">{root.plex_visible_root || root.destination_root}</span>
                </span>
                {root.edition === "4k" ? <Badge tone="info">4K</Badge> : null}
                {current ? <Badge>Her nu</Badge> : null}
              </label>
            )
          })}
        </div>
        <p className="text-xs text-text-3">Kun links i biblioteket flyttes. Selve filen i AltMount bliver ikke rørt.</p>
        {mutation.error ? <p role="alert" className="text-sm text-bad">{actionError(mutation.error)}</p> : null}
      </div>
    </Dialog>
  )
}

function DeleteDialog({ detail, onClose }: { detail: APIMediaDetail; onClose: () => void }) {
  const { media } = detail
  const queryClient = useQueryClient()
  const toast = useToast()
  const navigate = useNavigate()
  const inPlex = detail.files.some((file) => file.state === "current")
  const open = detail.workflows.some((workflow) => !workflow.terminal_at)
  const mutation = useMutation({
    mutationFn: () => recycleMedia(media.id, true),
    onSuccess: async () => {
      toast("good", inPlex ? `${media.title} er lagt i papirkurven. Du kan gendanne den under System → Papirkurv i 30 dage.` : `${media.title} er fjernet.`)
      onClose()
      await queryClient.invalidateQueries()
      navigate("/bibliotek")
    },
  })
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Slet ${media.title}?`}
      footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="danger" busy={mutation.isPending} onClick={() => mutation.mutate()}>Slet titel</Button></>}
    >
      <div className="space-y-3">
        {inPlex ? (
          <p>Titlen forsvinder fra Plex og fra Media Manager. Linkene lægges i papirkurven i 30 dage, så du kan fortryde under System → Papirkurv.</p>
        ) : (
          <p>Titlen er ikke i Plex, så den fjernes kun fra Media Manager. Du kan tilføje den igen senere.</p>
        )}
        {open ? <p>{media.kind === "series" ? "Søgninger og overvågning af nye afsnit stoppes." : "Søgningen efter titlen stoppes."}</p> : null}
        <p className="text-xs text-text-3">Selve filen i AltMount bliver ikke slettet.</p>
        {mutation.error ? <p role="alert" className="text-sm text-bad">{actionError(mutation.error)}</p> : null}
      </div>
    </Dialog>
  )
}

/** Danish text for the refusals when new work is started on a title. */
function getError(error: unknown, series: boolean): string {
  if (error instanceof ApiError) {
    if (error.code === "workflow_already_open") return series ? "Der er allerede arbejde i gang for sæsonen. Brug Søg på sæsonen i stedet." : "Filmen hentes eller ventes allerede. Brug Søg under Status i stedet."
    if (error.code === "upgrades_disabled") return "Den ligger allerede i Plex, og kvalitetsprofilen leder ikke efter bedre udgaver. Vælg en anden profil."
    if (error.code === "no_commissioned_indexer") return "Ingen indexer er klar til at søge. Tilføj eller slå en til under Kilder."
    if (/adopted/i.test(error.message)) return "Titlen har en fil, Media Manager har overtaget fra dit gamle bibliotek. Den skal afklares under Handling først."
  }
  return errorMessage(error)
}

/** The folder and profile the title already uses, so new work lands next to it. */
function currentChoice(detail: APIMediaDetail, roots: APILibraryRoot[], profiles: APIQualityProfile[]): { rootId: string; profileId: string } {
  const kind = detail.media.kind
  const latest = [...detail.workflows].filter((workflow) => workflow.state !== "cancelled").sort((a, b) => b.updated_at.localeCompare(a.updated_at))
  const usable = (id?: string) => Boolean(id && roots.some((root) => root.id === id && root.enabled))
  const rootId = latest.map((workflow) => workflow.root_id).find((id) => usable(id) && roots.find((root) => root.id === id)?.edition !== "4k")
    ?? roots.find((root) => root.media_kind === kind && root.enabled && root.is_default)?.id ?? ""
  const profileId = latest.map((workflow) => workflow.quality_profile_id).find((id) => id && profiles.some((profile) => profile.id === id && profile.enabled))
    ?? profiles.find((profile) => profile.media_kind === kind && profile.enabled && profile.is_default)?.id ?? ""
  return { rootId, profileId }
}

const seasonVariant = (number: number) => `season:${String(number).padStart(2, "0")}`

/**
 * Hent (a film) or Tilføj sæson (a series): starts new work on a title that is
 * already in the library, like Radarr's search or Sonarr's season monitor.
 */
function GetDialog({ detail, onClose }: { detail: APIMediaDetail; onClose: () => void }) {
  const { media } = detail
  const series = media.kind === "series"
  const queryClient = useQueryClient()
  const toast = useToast()
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const profiles = useQuery({ queryKey: ["settings", "profiles"], queryFn: ({ signal }) => loadQualityProfiles(signal) })
  const tmdb = Number(media.identities.find((identity) => identity.namespace === "tmdb")?.value ?? 0)
  const knownCount = media.metadata?.season_count ?? 0
  const catalogue = useQuery({ queryKey: ["discovery", "tv", tmdb], queryFn: ({ signal }) => loadDiscoveryMetadata("tv", tmdb, undefined, signal), enabled: series && knownCount === 0 && tmdb > 0, staleTime: 300_000 })
  const seasonCount = knownCount || catalogue.data?.metadata?.season_count || 0
  const [picked, setPicked] = useState<number[]>([])
  const [rootChoice, setRootChoice] = useState("")
  const [profileChoice, setProfileChoice] = useState("")
  const [searchNow, setSearchNow] = useState(true)
  const kindRoots = (roots.data ?? []).filter((root) => root.media_kind === media.kind && root.enabled).sort((a, b) => a.name.localeCompare(b.name, "da"))
  const kindProfiles = (profiles.data ?? []).filter((profile) => profile.media_kind === media.kind && profile.enabled).sort((a, b) => a.name.localeCompare(b.name, "da"))
  const defaults = currentChoice(detail, roots.data ?? [], profiles.data ?? [])
  const rootId = rootChoice || defaults.rootId
  const profileId = profileChoice || defaults.profileId
  const edition = (roots.data ?? []).find((root) => root.id === rootId)?.edition ?? "standard"
  const sameEdition = (workflowEdition?: string) => (workflowEdition ?? "standard") === edition
  const busyVariant = (variant: string) => detail.workflows.some((workflow) => !workflow.terminal_at && workflow.variant === variant && sameEdition(workflow.edition))
  const seasonInPlex = (number: number) => {
    const season = detail.seasons.find((entry) => entry.number === number)
    const expected = season?.expected_episodes ?? 0
    const prefix = `Season ${String(number).padStart(2, "0")}/`
    const present = detail.files.filter((file) => file.state === "current" && file.destination_relative.includes(prefix)).length
    return expected > 0 && present >= expected
  }
  const rootEdition = (name: string) => (roots.data ?? []).find((root) => root.name === name)?.edition ?? "standard"
  const movieInPlex = detail.files.some((file) => file.state === "current" && rootEdition(file.root_name) === edition)
  const movieBusy = !series && busyVariant("default")
  const mutation = useMutation({
    mutationFn: async () => {
      const base = { ...(rootId ? { root_id: rootId } : {}), ...(profileId ? { quality_profile_id: profileId } : {}), search_immediately: searchNow }
      if (!series) return [await createMediaWorkflow(media.id, base)]
      if (picked.length === 0) throw new Error("Vælg mindst én sæson.")
      const created = []
      for (const number of [...picked].sort((a, b) => a - b)) {
        // Each season is frozen from TMDB's exact episode list, never a guessed count.
        const snapshot = await loadDiscoveryMetadata("tv", tmdb, number)
        if (!snapshot.season || snapshot.season.episodes.length === 0) throw new Error(`TMDB har ingen afsnitsliste for sæson ${number}.`)
        created.push(await createMediaWorkflow(media.id, {
          ...base,
          season: { number, monitored: true, search_immediately: searchNow, prefer_season_pack: true, episodes: snapshot.season.episodes.map((episode) => ({ number: episode.number, provider_episode_id: episode.provider_id, title: episode.title, air_date: episode.air_date })) },
        }))
      }
      return created
    },
    onSuccess: async (created) => {
      const what = series ? (created.length === 1 ? `Sæson ${[...picked].sort((a, b) => a - b)[0]} er tilføjet` : `${created.length} sæsoner er tilføjet`) : `${media.title} er sat i gang`
      toast("good", `${what}${searchNow ? ", og søgningen starter nu" : ""}.`)
      onClose()
      await queryClient.invalidateQueries({ queryKey: ["media"] })
    },
    onSettled: async () => { await queryClient.invalidateQueries({ queryKey: ["media", media.id] }) },
  })
  const ready = Boolean(rootId && profileId) && (series ? picked.length > 0 : !movieBusy)
  const toggle = (number: number) => setPicked((current) => current.includes(number) ? current.filter((value) => value !== number) : [...current, number])
  return (
    <Dialog
      open
      onClose={onClose}
      title={series ? `Tilføj sæson til ${media.title}` : `Hent ${media.title}`}
      footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!ready} onClick={() => mutation.mutate()}>{series ? (picked.length > 1 ? `Tilføj ${picked.length} sæsoner` : "Tilføj sæson") : movieInPlex ? "Find en bedre udgave" : "Hent filmen"}</Button></>}
    >
      <div className="space-y-4">
        {series ? (
          <fieldset>
            <legend className="mb-2">Sæsoner</legend>
            {tmdb === 0 ? <p className="text-warn">Serien har intet TMDB-id, så sæsonerne kan ikke hentes. Prøv Opdatér oplysninger.</p> : null}
            {catalogue.isFetching ? <p className="text-text-3">Henter sæsonerne fra TMDB…</p> : null}
            {tmdb > 0 && !catalogue.isFetching && seasonCount === 0 ? <p className="text-warn">TMDB kender ingen sæsoner for serien endnu.</p> : null}
            <div className="flex flex-wrap gap-2">
              {Array.from({ length: seasonCount }, (_, index) => index + 1).map((number) => {
                const busy = busyVariant(seasonVariant(number))
                const done = seasonInPlex(number)
                const blocked = busy || done
                return (
                  <button key={number} type="button" disabled={blocked} aria-pressed={picked.includes(number)} onClick={() => toggle(number)} title={busy ? "Allerede i gang" : done ? "Ligger allerede i Plex" : undefined}
                    className={cx("h-9 min-w-12 rounded-lg border px-3 text-sm font-medium", picked.includes(number) ? "border-accent bg-accent-soft text-text" : "border-line bg-surface text-text-2", blocked && "cursor-default opacity-50")}>
                    {number}{busy ? " · i gang" : done ? " · i Plex" : ""}
                  </button>
                )
              })}
            </div>
            <p className="mt-2 text-xs text-text-3">Sæsoner, der allerede er i gang eller ligger i Plex, kan ikke vælges.</p>
          </fieldset>
        ) : movieBusy ? (
          <p className="text-warn">Filmen hentes eller ventes allerede. Brug Søg under Status for at søge igen nu.</p>
        ) : movieInPlex ? (
          <p>Filmen ligger allerede i Plex. Media Manager leder efter en bedre udgave og beholder den nuværende, indtil en bedre er klar.</p>
        ) : (
          <p>Media Manager søger efter filmen og lægger den i Plex, når en god udgave er fundet.</p>
        )}
        <label className="block">
          <span>Bibliotek</span>
          <select value={rootId} onChange={(event) => setRootChoice(event.target.value)} className="mt-1 h-9 w-full rounded-lg border border-line bg-surface px-3 text-text">
            {kindRoots.map((root) => <option key={root.id} value={root.id}>{root.name}{root.edition === "4k" ? " (4K)" : ""}</option>)}
          </select>
          {edition === "4k" ? <span className="mt-1 block text-xs text-text-3">En 4K-udgave hentes ved siden af den almindelige; den erstatter den ikke.</span> : null}
        </label>
        <label className="block">
          <span>Kvalitetsprofil</span>
          <select value={profileId} onChange={(event) => setProfileChoice(event.target.value)} className="mt-1 h-9 w-full rounded-lg border border-line bg-surface px-3 text-text">
            {kindProfiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}
          </select>
        </label>
        <label className="flex items-center gap-2 text-text">
          <input type="checkbox" checked={searchNow} onChange={(event) => setSearchNow(event.target.checked)} className="size-4" />
          Søg med det samme
        </label>
        {mutation.error ? <p role="alert" className="text-sm text-bad">{getError(mutation.error, series)}</p> : null}
      </div>
    </Dialog>
  )
}
