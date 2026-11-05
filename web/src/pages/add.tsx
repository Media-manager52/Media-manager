import { useMutation, useQuery } from "@tanstack/react-query"
import { Search } from "lucide-react"
import { useState, type FormEvent } from "react"
import { Link, useNavigate } from "react-router"

import {
  ApiError, createMedia, loadDiscoveryMetadata, loadLibraryRoots, loadQualityProfiles, searchMetadata,
  type APIDiscoveryDetail, type APIDiscoverySearchResult, type CreateMediaInput,
} from "@/api/client"
import { OptionPicker } from "@/components/option-picker"
import { Poster, sizedArtwork } from "@/components/poster"
import { useToast } from "@/components/toast"
import { Button, Card, CardHeader, cx, Empty, ErrorBox, PageHeader, Spinner } from "@/components/ui"
import { kindLabel, languageName } from "@/i18n"
import { titleHref } from "@/lib/links"
import { profileOptions, rootOptions } from "@/lib/pickers"

export function AddPage() {
  const [query, setQuery] = useState("")
  const [submitted, setSubmitted] = useState("")
  const [picked, setPicked] = useState<APIDiscoverySearchResult | null>(null)
  const results = useQuery({
    queryKey: ["discovery", submitted], queryFn: ({ signal }) => searchMetadata(submitted, 1, signal), enabled: submitted.length >= 2, staleTime: 300_000,
    // Missing TMDB is a setup step, not a hiccup: say so at once instead of retrying.
    retry: (count, error) => !(error instanceof ApiError && error.code === "metadata_unavailable") && count < 2,
  })
  const needsTMDB = results.error instanceof ApiError && results.error.code === "metadata_unavailable"
  const onSubmit = (event: FormEvent) => {
    event.preventDefault()
    setPicked(null)
    setSubmitted(query.trim())
  }
  return (
    <>
      <PageHeader title="Tilføj film eller serie" description="Søg i TMDB. Når du vælger en titel, henter Media Manager de præcise oplysninger, før den bliver tilføjet." />
      <form onSubmit={onSubmit} className="mb-6 flex flex-col gap-2 sm:flex-row">
        <label htmlFor="add-search" className="sr-only">Titel</label>
        <div className="relative flex-1">
          <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-text-3" />
          <input id="add-search" value={query} onChange={(event) => setQuery(event.target.value)} minLength={2} maxLength={200} required placeholder="Fx Matador, Borgen eller The Bear" className="h-11 w-full rounded-xl border border-line bg-surface pl-9 pr-3" />
        </div>
        <Button type="submit" variant="primary" className="h-11 px-5" busy={results.isFetching}>Søg</Button>
      </form>
      {needsTMDB ? (
        <Card>
          <div role="alert" className="p-5 text-sm">
            <p className="font-medium">Søgning kræver TMDB</p>
            <p className="mt-1 text-text-2">Media Manager finder film og serier i TMDB. Opret en gratis TMDB-konto, og tilføj dens "API Read Access Token" under <Link className="underline" to="/indstillinger/forbindelser">Indstillinger → Forbindelser</Link>. Så virker søgningen med det samme.</p>
          </div>
        </Card>
      ) : results.error ? <ErrorBox error={results.error} /> : null}
      {picked ? <AddForm result={picked} onBack={() => setPicked(null)} /> : null}
      {!picked && results.data ? (
        results.data.results.length === 0 ? <Card><Empty title="Ingen titler fundet">Prøv den danske eller den originale titel.</Empty></Card> : (
          <ul className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-4">
            {results.data.results.slice(0, 30).map((result) => {
              const name = result.mediaType === "movie" ? result.title : result.name
              const date = result.mediaType === "movie" ? result.releaseDate : result.firstAirDate
              return (
                <li key={`${result.mediaType}-${result.id}`}>
                  <button type="button" onClick={() => setPicked(result)} className="group block w-full text-left">
                    <Poster src={result.posterPath?.startsWith("/") ? `https://image.tmdb.org/t/p/w342${result.posterPath}` : undefined} title={name ?? ""} kind={result.mediaType === "tv" ? "series" : "movie"} />
                    <div className="mt-2 line-clamp-2 text-sm font-medium group-hover:underline">{name}</div>
                    <div className="text-xs text-text-3">{[date?.slice(0, 4), result.mediaType === "tv" ? "Serie" : "Film"].filter(Boolean).join(" · ")}</div>
                  </button>
                </li>
              )
            })}
          </ul>
        )
      ) : null}
    </>
  )
}

function AddForm({ result, onBack }: { result: APIDiscoverySearchResult; onBack: () => void }) {
  const detail = useQuery({ queryKey: ["discovery", result.mediaType, result.id], queryFn: ({ signal }) => loadDiscoveryMetadata(result.mediaType, result.id, undefined, signal), staleTime: 300_000 })
  if (detail.error) return <ErrorBox error={detail.error} retry={() => detail.refetch()} />
  if (!detail.data) return <Spinner label="Henter præcise oplysninger…" />
  return <AddFormBody detail={detail.data} onBack={onBack} />
}

function addErrorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === "media_exists") return "Titlen er allerede i biblioteket."
    if (error.code === "media_removed") return "Titlen er tidligere fjernet fra biblioteket og kan ikke tilføjes som ny."
    if (error.code === "no_commissioned_indexer") return "Der er ingen indexer, der kan søge efter titlen. Tilføj en under Indstillinger → Forbindelser, eller fjern fluebenet \"Begynd at søge med det samme\"."
    if (error.code === "invalid_configuration") return "Der mangler en standardmappe eller standardprofil. Tjek Indstillinger → Biblioteker og Kvalitetsprofiler."
  }
  return error instanceof Error ? error.message : String(error)
}

function AddFormBody({ detail, onBack }: { detail: APIDiscoveryDetail; onBack: () => void }) {
  const navigate = useNavigate()
  const toast = useToast()
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const profiles = useQuery({ queryKey: ["settings", "profiles"], queryFn: ({ signal }) => loadQualityProfiles(signal) })
  const kind = detail.kind
  const seasonCount = detail.metadata?.season_count ?? 0
  const [seasons, setSeasons] = useState<number[]>(() => Array.from({ length: seasonCount }, (_, index) => index + 1))
  const [rootId, setRootId] = useState("")
  const [profileId, setProfileId] = useState("")
  const [searchNow, setSearchNow] = useState(true)
  const mutation = useMutation({
    mutationFn: async () => {
      const input: CreateMediaInput = {
        kind,
        title: detail.title,
        year: detail.year,
        identities: detail.identities.map((identity) => ({ ...identity, state: "verified" as const, source: identity.source || "seerr_metadata" })),
        ...(detail.metadata ? { metadata: detail.metadata } : {}),
        search_immediately: searchNow,
        ...(rootId ? { root_id: rootId } : {}),
        ...(profileId ? { quality_profile_id: profileId } : {}),
      }
      if (kind === "series") {
        if (seasons.length === 0) throw new Error("Vælg mindst én sæson.")
        input.seasons = []
        for (const number of [...seasons].sort((a, b) => a - b)) {
          // Each season is frozen from an exact episode list, never a guessed count.
          const snapshot = await loadDiscoveryMetadata("tv", detail.tmdb_id, number)
          if (!snapshot.season || snapshot.season.episodes.length === 0) throw new Error(`TMDB har ingen afsnitsliste for sæson ${number}.`)
          input.seasons.push({
            number, monitored: true, search_immediately: searchNow, prefer_season_pack: true,
            episodes: snapshot.season.episodes.map((episode) => ({ number: episode.number, provider_episode_id: episode.provider_id, title: episode.title, air_date: episode.air_date })),
          })
        }
      }
      return await createMedia(input)
    },
    onSuccess: (created) => {
      toast("good", `${detail.title} er tilføjet${searchNow ? ", og søgningen er sat i gang" : ""}.`)
      navigate(titleHref(created.media.id))
    },
    onError: (error) => {
      const existing = error instanceof ApiError && error.code === "media_exists" ? error.hint?.match(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i)?.[0] : undefined
      if (existing) {
        toast("good", `${detail.title} er allerede i biblioteket. Her er den.`)
        navigate(titleHref(existing))
        return
      }
      toast("bad", addErrorText(error))
    },
  })
  const meta = detail.metadata
  const toggle = (number: number) => setSeasons((current) => (current.includes(number) ? current.filter((entry) => entry !== number) : [...current, number]))
  return (
    <Card>
      <CardHeader title={`${detail.title}${detail.year ? ` (${detail.year})` : ""}`} description={[kindLabel(kind), meta?.original_language ? `Original: ${languageName(meta.original_language)}` : null].filter(Boolean).join(" · ")} action={<Button variant="ghost" onClick={onBack}>Tilbage til resultater</Button>} />
      <div className="grid grid-cols-1 gap-6 p-5 md:grid-cols-[10rem_minmax(0,1fr)]">
        <Poster src={sizedArtwork(meta?.poster_url, "w342")} title={detail.title} kind={kind} className="w-32 md:w-40" />
        <form onSubmit={(event) => { event.preventDefault(); mutation.mutate() }} className="flex min-w-0 flex-col gap-5">
          {meta?.overview ? <p className="text-text-2">{meta.overview}</p> : null}
          {kind === "series" ? (
            <fieldset>
              <legend className="mb-2 text-sm font-medium">Sæsoner</legend>
              {seasonCount === 0 ? <p className="text-sm text-warn">TMDB kender ingen sæsoner for denne serie endnu.</p> : (
                <div className="flex flex-wrap gap-2">
                  {Array.from({ length: seasonCount }, (_, index) => index + 1).map((number) => (
                    <button key={number} type="button" aria-pressed={seasons.includes(number)} onClick={() => toggle(number)} className={cx("h-9 min-w-12 rounded-lg border px-3 text-sm font-medium", seasons.includes(number) ? "border-accent bg-accent-soft text-text" : "border-line bg-surface text-text-2")}>
                      {number}
                    </button>
                  ))}
                </div>
              )}
            </fieldset>
          ) : null}
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <OptionPicker
              label="Bibliotek"
              value={rootId}
              onChange={setRootId}
              options={rootOptions(roots.data ?? [], kind, { label: "Automatisk", description: "Standardmappen, medmindre en Seerr-regel vælger en anden." })}
            />
            <OptionPicker
              label="Kvalitetsprofil"
              value={profileId}
              onChange={setProfileId}
              options={profileOptions(profiles.data ?? [], kind, { label: "Automatisk", description: "Standardprofilen, eller den profil der passer til titlens originalsprog." })}
            />
          </div>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={searchNow} onChange={(event) => setSearchNow(event.target.checked)} className="size-4 accent-[var(--accent)]" />
            Begynd at søge med det samme
          </label>
          <div>
            <Button type="submit" variant="primary" busy={mutation.isPending} disabled={kind === "series" && seasons.length === 0}>Tilføj {kind === "movie" ? "film" : "serie"}</Button>
          </div>
        </form>
      </div>
    </Card>
  )
}
