import { useQuery, useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, CheckCircle2, Clapperboard, Download, Plus, Tv } from "lucide-react"
import { useMemo, useState } from "react"
import { Link, useSearchParams } from "react-router"

import { createMediaWorkflow, loadLibraryGaps, loadLibraryRoots, loadQualityProfiles, seasonWorkflowInput, type APILibraryRoot, type APIMovieGap, type APIQualityProfile, type APISeriesGap } from "@/api/client"
import { Poster, sizedArtwork } from "@/components/poster"
import { startWorkError } from "@/components/title-actions"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, Dialog, Empty, ErrorBox, PageHeader, SearchField, Segmented, Skeleton } from "@/components/ui"
import { formatDate, formatNumber } from "@/format"
import { stateLabel } from "@/i18n"
import { gapChoice, matchesTitle, seasonList, seriesStatusLabel } from "@/lib/library-gaps"

type View = "nye" | "aeldre" | "film"
const views: View[] = ["nye", "aeldre", "film"]
/** One click adds at most this many titles, so a mistake stays small. */
const bulkLimit = 25

type Added = Record<string, "busy" | "done" | string>

export function MissingPage() {
  const [params, setParams] = useSearchParams()
  const view = views.includes(params.get("vis") as View) ? params.get("vis") as View : "nye"
  const [filter, setFilter] = useState("")
  const [added, setAdded] = useState<Added>({})
  const [bulk, setBulk] = useState(false)
  const gaps = useQuery({ queryKey: ["media", "gaps"], queryFn: ({ signal }) => loadLibraryGaps(signal) })
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const profiles = useQuery({ queryKey: ["settings", "profiles"], queryFn: ({ signal }) => loadQualityProfiles(signal) })
  const queryClient = useQueryClient()
  const toast = useToast()

  const { series, newSeries, olderSeries, movies } = useMemo(() => {
    const all = gaps.data?.series ?? []
    return { series: all, newSeries: all.filter((gap) => gap.new_seasons.length > 0), olderSeries: all.filter((gap) => gap.older_seasons.length > 0), movies: gaps.data?.movies ?? [] }
  }, [gaps.data])
  const counts = { nye: newSeries.length, aeldre: olderSeries.length, film: movies.length }
  const shown = useMemo(() => {
    const list: Array<APISeriesGap | APIMovieGap> = view === "film" ? movies : view === "nye" ? newSeries : olderSeries
    return list.filter((gap) => matchesTitle(gap.title, filter))
  }, [view, movies, newSeries, olderSeries, filter])
  const pending = shown.filter((gap) => added[gap.media_id] === undefined)
  const ready = Boolean(roots.data && profiles.data)

  const seasonsFor = (gap: APISeriesGap) => view === "aeldre" ? gap.older_seasons : gap.new_seasons

  /** Adds the gap's seasons (or fetches the film) the way the title page does. */
  async function add(gap: APISeriesGap | APIMovieGap): Promise<boolean> {
    const isSeries = "season_count" in gap
    const choice = gapChoice(gap, isSeries ? "series" : "movie", roots.data ?? [], profiles.data ?? [])
    if (!choice.rootId || !choice.profileId) {
      setAdded((current) => ({ ...current, [gap.media_id]: "Vælg en standardmappe og en standardprofil under Indstillinger først." }))
      return false
    }
    setAdded((current) => ({ ...current, [gap.media_id]: "busy" }))
    const base = { root_id: choice.rootId, quality_profile_id: choice.profileId, search_immediately: true }
    try {
      if (isSeries) {
        const tmdb = Number(gap.tmdb_id ?? 0)
        if (!tmdb) throw new Error("Serien har intet TMDB-id. Åbn titlen og brug Opdatér oplysninger.")
        for (const number of [...seasonsFor(gap)].sort((a, b) => a - b)) {
          await createMediaWorkflow(gap.media_id, await seasonWorkflowInput(tmdb, number, base))
        }
      } else {
        await createMediaWorkflow(gap.media_id, base)
      }
      setAdded((current) => ({ ...current, [gap.media_id]: "done" }))
      return true
    } catch (error) {
      setAdded((current) => ({ ...current, [gap.media_id]: startWorkError(error, isSeries) }))
      return false
    }
  }

  async function addMany() {
    setBulk(false)
    let done = 0
    for (const gap of pending.slice(0, bulkLimit)) {
      if (await add(gap)) done++
    }
    toast(done > 0 ? "good" : "bad", done > 0 ? `${formatNumber(done)} ${view === "film" ? "film er sat i gang" : done === 1 ? "serie fik sine sæsoner tilføjet" : "serier fik deres sæsoner tilføjet"}. Søgningen starter nu.` : "Intet blev tilføjet. Se beskeden ved hver titel.")
    await queryClient.invalidateQueries({ queryKey: ["media"] })
  }

  const latestMetadata = series.map((gap) => gap.metadata_at).filter(Boolean).sort().at(-1)

  return (
    <>
      <PageHeader
        eyebrow={<Link to="/bibliotek" className="inline-flex items-center gap-1 hover:text-text"><ArrowLeft aria-hidden className="size-3.5" />Bibliotek</Link>}
        title="Find manglende"
        description="Sæsoner af dine serier, som Media Manager hverken følger eller har i Plex, og film uden fil. Intet hentes, før du trykker."
      />
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <Segmented
          label="Vis"
          value={view}
          onChange={(value) => { const next = new URLSearchParams(params); if (value === "nye") next.delete("vis"); else next.set("vis", value); setParams(next, { replace: true }) }}
          options={[
            { value: "nye", label: "Nye sæsoner", count: gaps.data ? counts.nye : undefined },
            { value: "aeldre", label: "Ældre sæsoner", count: gaps.data ? counts.aeldre : undefined },
            { value: "film", label: "Film uden fil", count: gaps.data ? counts.film : undefined },
          ]}
        />
        <SearchField value={filter} onChange={setFilter} placeholder="Filtrér titler" label="Filtrér titler" />
        {view !== "aeldre" && pending.length > 0 ? (
          <Button variant="primary" className="sm:ml-auto" disabled={!ready} onClick={() => setBulk(true)}>
            <Plus aria-hidden className="size-4" />
            {view === "film" ? `Hent ${pending.length > bulkLimit ? `de første ${bulkLimit}` : `alle ${formatNumber(pending.length)}`}` : `Tilføj ${pending.length > bulkLimit ? `de første ${bulkLimit}` : `alle ${formatNumber(pending.length)}`}`}
          </Button>
        ) : null}
      </div>
      <p className="mb-4 text-sm text-text-2">{viewHelp[view]}{view !== "film" && latestMetadata ? ` Antallet af sæsoner er TMDB's, senest hentet ${formatDate(latestMetadata)}.` : ""}</p>
      {gaps.error ? <ErrorBox error={gaps.error} retry={() => gaps.refetch()} /> : null}
      {gaps.isPending ? <Card><div className="space-y-3 p-5">{[0, 1, 2, 3].map((index) => <Skeleton key={index} className="h-16" />)}</div></Card> : null}
      {gaps.data && shown.length === 0 ? (
        <Card><Empty title={filter ? "Ingen titler passer til filteret" : emptyTitle[view]} icon={<CheckCircle2 className="size-8 text-good" />}>{filter ? "Prøv et andet navn." : emptyText[view]}</Empty></Card>
      ) : null}
      {shown.length > 0 ? (
        <Card as="div">
          <ul className="divide-y divide-line">
            {shown.map((gap) => (
              <GapRow key={gap.media_id} gap={gap} seasons={"season_count" in gap ? seasonsFor(gap) : undefined} state={added[gap.media_id]} onAdd={() => void add(gap).then(async (ok) => { if (ok) await queryClient.invalidateQueries({ queryKey: ["media", gap.media_id] }) })} ready={ready} roots={roots.data ?? []} profiles={profiles.data ?? []} />
            ))}
          </ul>
        </Card>
      ) : null}
      <Dialog
        open={bulk}
        onClose={() => setBulk(false)}
        title={view === "film" ? "Hent film" : "Tilføj nye sæsoner"}
        footer={<><Button variant="ghost" onClick={() => setBulk(false)}>Fortryd</Button><Button variant="primary" onClick={() => void addMany()}>{view === "film" ? "Hent" : "Tilføj"} {formatNumber(Math.min(pending.length, bulkLimit))}</Button></>}
      >
        <p>
          {view === "film"
            ? `Media Manager begynder at søge efter ${formatNumber(Math.min(pending.length, bulkLimit))} film med det samme.`
            : `${formatNumber(Math.min(pending.length, bulkLimit))} serier får deres nye sæsoner tilføjet, og søgningen starter med det samme. Sæsoner, der ikke er sendt endnu, hentes, når afsnittene kommer.`}
          {" "}Hver titel bruger sin egen mappe og kvalitetsprofil, ellers standarden. Sprog- og kvalitetskrav er de samme som altid.
        </p>
        {pending.length > bulkLimit ? <p className="mt-2">Der er {formatNumber(pending.length)} i alt. De næste kan du tilføje bagefter.</p> : null}
      </Dialog>
    </>
  )
}

const viewHelp: Record<View, string> = {
  nye: "Sæsoner efter den nyeste, Media Manager kender for serien.",
  aeldre: "Tidligere sæsoner, du aldrig har bedt om. Måske er det med vilje, så de tilføjes kun én serie ad gangen.",
  film: "Film, der hverken ligger i Plex eller hentes, fx fordi arbejdet blev annulleret.",
}
const emptyTitle: Record<View, string> = { nye: "Ingen nye sæsoner", aeldre: "Ingen huller i ældre sæsoner", film: "Ingen film mangler" }
const emptyText: Record<View, string> = {
  nye: "Alle dine serier følges til og med den nyeste sæson, TMDB kender.",
  aeldre: "Alle tidligere sæsoner af dine serier er i Plex eller følges.",
  film: "Alle film ligger i Plex eller er ved at blive hentet.",
}

function GapRow({ gap, seasons, state, onAdd, ready, roots, profiles }: { gap: APISeriesGap | APIMovieGap; seasons?: number[]; state?: string; onAdd: () => void; ready: boolean; roots: APILibraryRoot[]; profiles: APIQualityProfile[] }) {
  const isSeries = seasons !== undefined
  const seriesGap = isSeries ? gap as APISeriesGap : undefined
  const movieGap = isSeries ? undefined : gap as APIMovieGap
  const choice = gapChoice(gap, isSeries ? "series" : "movie", roots, profiles)
  const folder = roots.find((root) => root.id === choice.rootId)?.name
  const stopped = seriesGap ? seriesGap.stopped_seasons.filter((number) => (seasons ?? []).includes(number)) : []
  const Icon = isSeries ? Tv : Clapperboard
  return (
    <li className="flex flex-wrap items-center gap-4 px-5 py-3">
      <Link to={`/bibliotek/${gap.media_id}`} className="w-11 shrink-0" tabIndex={-1} aria-hidden>
        <Poster src={sizedArtwork(gap.poster_url, "w185")} title={gap.title} kind={isSeries ? "series" : "movie"} compact className="rounded-md" />
      </Link>
      <div className="min-w-0 flex-1">
        <Link to={`/bibliotek/${gap.media_id}`} className="font-medium hover:underline">{gap.title}</Link>
        {gap.year ? <span className="text-text-3"> ({gap.year})</span> : null}
        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-text-2">
          <Icon aria-hidden className="size-3.5 text-text-3" />
          {seriesGap ? <span className="font-medium text-text">{seasonList(seasons ?? [])}</span> : null}
          {seriesGap ? <span>{seriesGap.known_seasons.length > 0 ? `Følger ${seasonList(seriesGap.known_seasons).toLowerCase()}` : "Ingen sæsoner følges"} af {seriesGap.season_count}</span> : null}
          {seriesGap && seriesStatusLabel(seriesGap.status) ? <Badge>{seriesStatusLabel(seriesGap.status)}</Badge> : null}
          {stopped.length > 0 ? <Badge tone="warn">{seasonList(stopped)} blev stoppet før</Badge> : null}
          {movieGap?.last_state ? <span>{stateLabel(movieGap.last_state)}{movieGap.last_at ? ` ${formatDate(movieGap.last_at)}` : ""}</span> : null}
          {movieGap && !movieGap.last_state ? <span>Aldrig hentet</span> : null}
          {folder ? <span className="text-text-3">→ {folder}</span> : null}
        </div>
        {state && state !== "busy" && state !== "done" ? <p role="alert" className="mt-1 text-sm text-bad">{state}</p> : null}
      </div>
      {state === "done" ? (
        <Badge tone="good"><CheckCircle2 aria-hidden className="size-3.5" />{isSeries ? "Tilføjet" : "Sat i gang"}</Badge>
      ) : (
        <Button busy={state === "busy"} disabled={!ready || state === "busy"} onClick={onAdd}>
          {isSeries ? <Plus aria-hidden className="size-4" /> : <Download aria-hidden className="size-4" />}
          {isSeries ? ((seasons ?? []).length === 1 ? "Tilføj sæson" : `Tilføj ${(seasons ?? []).length} sæsoner`) : "Hent"}
        </Button>
      )}
    </li>
  )
}
