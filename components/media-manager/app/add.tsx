"use client"

import * as React from "react"
import Link from "next/link"
import { Check, LoaderCircle, Search } from "lucide-react"

import { createMedia, loadDiscoveryMetadata, loadIntegrations, loadLibraryRoots, loadQualityProfiles, searchMetadata, type APIDiscoveryDetail, type APIDiscoverySearchResult, type APIIntegration, type APILibraryRoot, type APIQualityProfile } from "@/lib/media-manager-api"
import { useAppData } from "./app-data"
import { AppShell } from "./shell"
import { LoadState, PageTitle, Thumb, titleHref } from "./parts"

type Setup = { roots: APILibraryRoot[]; profiles: APIQualityProfile[]; integrations: APIIntegration[] }
type ResultState = { busy?: boolean; error?: string; detail?: APIDiscoveryDetail; picked: number[]; open?: boolean; addedID?: string }

const resultKey = (result: APIDiscoverySearchResult) => `${result.mediaType}:${result.id}`
const posterURL = (path?: string) => (path && path.startsWith("/") ? `https://image.tmdb.org/t/p/w342${path}` : undefined)

/** What is still missing before a title of this kind can be added and searched. */
function missingSetup(setup: Setup | null, kind: "movie" | "series", originalLanguage?: string): string {
  if (!setup) return ""
  if (!setup.roots.some((root) => root.media_kind === kind && root.enabled)) return kind === "movie" ? "a movie library folder" : "a TV library folder"
  if (!setup.profiles.some((profile) => profile.media_kind === kind && profile.enabled)) return "a quality setting"
  if (!setup.integrations.some((item) => item.kind === "altmount" && item.enabled)) return "the AltMount download service"
  const indexers = setup.integrations.filter((item) => item.enabled)
  if (!indexers.some((item) => item.kind === "newznab") && !(originalLanguage === "da" && indexers.some((item) => item.kind === "oldboys"))) return "an indexer to search"
  return ""
}

function AddContent() {
  const { data, notify, refresh } = useAppData()
  const [query, setQuery] = React.useState("")
  const [searched, setSearched] = React.useState("")
  const [results, setResults] = React.useState<APIDiscoverySearchResult[]>([])
  const [searching, setSearching] = React.useState(false)
  const [searchError, setSearchError] = React.useState("")
  const [states, setStates] = React.useState<Record<string, ResultState>>({})
  const [setup, setSetup] = React.useState<Setup | null>(null)
  const [setupError, setSetupError] = React.useState("")
  React.useEffect(() => {
    const controller = new AbortController()
    Promise.all([loadLibraryRoots(controller.signal), loadQualityProfiles(controller.signal), loadIntegrations(controller.signal)])
      .then(([roots, profiles, integrations]) => setSetup({ roots, profiles, integrations }))
      .catch((reason: unknown) => { if (!controller.signal.aborted) setSetupError(reason instanceof Error ? reason.message : "Could not read your settings") })
    return () => controller.abort()
  }, [])
  const patch = (key: string, next: Partial<ResultState>) => setStates((current) => ({ ...current, [key]: { ...(current[key] ?? { picked: [] }), ...next } }))

  async function search(event: React.FormEvent) {
    event.preventDefault()
    const text = query.trim()
    if (text.length < 2) return
    setSearching(true); setSearchError("")
    try {
      const page = await searchMetadata(text)
      setResults(page.results.filter((result) => result.mediaType === "movie" || result.mediaType === "tv").slice(0, 24)); setSearched(text)
    } catch (reason) {
      setResults([]); setSearchError(reason instanceof Error ? reason.message : "Search failed")
    } finally { setSearching(false) }
  }

  function inLibrary(result: APIDiscoverySearchResult) {
    const kind = result.mediaType === "movie" ? "movie" : "series"
    return data?.media.find((item) => item.kind === kind && item.identities.some((identity) => identity.namespace === "tmdb" && identity.value === String(result.id)))
  }

  async function detailFor(result: APIDiscoverySearchResult, key: string) {
    const existing = states[key]?.detail
    if (existing) return existing
    const detail = await loadDiscoveryMetadata(result.mediaType, result.id)
    patch(key, { detail })
    return detail
  }

  async function chooseSeasons(result: APIDiscoverySearchResult) {
    const key = resultKey(result)
    patch(key, { busy: true, error: "" })
    try { await detailFor(result, key); patch(key, { busy: false, open: true }) } catch (reason) { patch(key, { busy: false, error: reason instanceof Error ? reason.message : "Could not load the seasons" }) }
  }

  async function add(result: APIDiscoverySearchResult) {
    const key = resultKey(result)
    const picked = [...(states[key]?.picked ?? [])].sort((a, b) => a - b)
    patch(key, { busy: true, error: "" })
    try {
      const detail = await detailFor(result, key)
      const missing = missingSetup(setup, detail.kind, detail.metadata?.original_language)
      if (missing) throw new Error(`Set up ${missing} first.`)
      if (!detail.year) throw new Error("TMDB has no release year for this title yet.")
      let seasons
      if (detail.kind === "series") {
        if (!picked.length) throw new Error("Pick at least one season.")
        seasons = []
        for (const number of picked) {
          const snapshot = await loadDiscoveryMetadata("tv", detail.tmdb_id, number)
          if (!snapshot.season || snapshot.season.episodes.length === 0) throw new Error(`TMDB has no episode list for season ${number} yet.`)
          seasons.push({ number, monitored: true, search_immediately: true, prefer_season_pack: true, episodes: snapshot.season.episodes.map((episode) => ({ number: episode.number, provider_episode_id: episode.provider_id, title: episode.title, air_date: episode.air_date })) })
        }
      }
      const identities = detail.identities.map((identity) => ({ ...identity, state: "verified" as const, source: identity.source || "seerr_metadata" }))
      const created = await createMedia({ kind: detail.kind, title: detail.title, year: detail.year, identities, ...(detail.metadata ? { metadata: detail.metadata } : {}), search_immediately: true, ...(seasons ? { seasons } : {}) })
      patch(key, { busy: false, open: false, addedID: created.media.id })
      notify(`${detail.title} added. Media Manager is looking for a copy.`)
      refresh()
    } catch (reason) {
      patch(key, { busy: false, error: reason instanceof Error ? reason.message : "Could not add this title" })
    }
  }

  const blocked = setup ? [missingSetup(setup, "movie"), missingSetup(setup, "series")].filter(Boolean) : []

  return <div className="mm-stack-tight">
    <PageTitle title="Add a movie or show" lede="New titles use your default library folder and quality setting, and Media Manager starts looking right away." />
    {setupError ? <div role="alert" className="mm-notice mm-notice-error"><span>Couldn’t read your settings: {setupError}</span></div> : null}
    {blocked.length === 2 ? <div className="mm-notice"><span>Finish setting up first: Media Manager still needs {blocked[0]}.</span><Link className="mm-btn mm-btn-small" href="/settings">Open settings</Link></div> : null}
    <form onSubmit={search} role="search">
      <label className="mm-search mm-search-large"><Search size={22} aria-hidden /><span className="mm-sr">Search for a movie or show</span><input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search for a movie or show" minLength={2} maxLength={200} /><button type="submit" className="mm-btn mm-btn-primary mm-btn-small" disabled={searching || query.trim().length < 2}>{searching ? <LoaderCircle size={18} className="animate-spin" aria-hidden /> : null}Search</button></label>
    </form>
    {searchError ? <div role="alert" className="mm-notice mm-notice-error"><span>Search didn’t work: {searchError}</span></div> : null}
    {!data ? <LoadState error="" loading label="Checking your library…" /> : null}
    {searched ? <h2 className="mm-h2">{results.length ? `Results for “${searched}”` : `Nothing found for “${searched}”`}</h2> : <p className="mm-muted">Type a title and press Search. Results come from TMDB.</p>}
    <div className="mm-results">{results.map((result) => {
      const key = resultKey(result)
      const state = states[key] ?? { picked: [] }
      const name = (result.mediaType === "movie" ? result.title : result.name) || "Untitled"
      const year = (result.mediaType === "movie" ? result.releaseDate : result.firstAirDate)?.slice(0, 4)
      const existing = inLibrary(result)
      const libraryID = state.addedID ?? existing?.id
      const seasonCount = state.detail?.metadata?.season_count ?? 0
      return <article key={key} className="mm-result">
        <Thumb title={name} src={posterURL(result.posterPath)} />
        <div className="mm-result-body">
          <strong>{name}</strong>
          <span className="mm-small mm-muted">{year ? `${year} · ` : ""}{result.mediaType === "movie" ? "Movie" : "TV show"}</span>
          {result.overview ? <span className="mm-small mm-clamp" style={{ color: "var(--text-2)" }}>{result.overview}</span> : null}
          {libraryID ? <span className="mm-added"><Check size={16} aria-hidden />{state.addedID ? "Added" : "In your library"} · <Link href={titleHref(libraryID)} className="mm-inline-link">Open</Link></span>
            : result.mediaType === "movie" ? <div><button type="button" className="mm-btn mm-btn-primary mm-btn-small" disabled={state.busy} onClick={() => void add(result)} aria-label={`Add ${name}`}>{state.busy ? <LoaderCircle size={18} className="animate-spin" aria-hidden /> : null}Add</button></div>
            : !state.open ? <div><button type="button" className="mm-btn mm-btn-small" disabled={state.busy} onClick={() => void chooseSeasons(result)} aria-label={`Choose seasons of ${name}`}>{state.busy ? <LoaderCircle size={18} className="animate-spin" aria-hidden /> : null}Choose seasons</button></div>
            : <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
              {seasonCount ? <div className="mm-chips" role="group" aria-label={`Seasons of ${name}`}>{Array.from({ length: seasonCount }, (_, index) => index + 1).map((number) => <button key={number} type="button" className="mm-chip" aria-pressed={state.picked.includes(number)} onClick={() => patch(key, { picked: state.picked.includes(number) ? state.picked.filter((value) => value !== number) : [...state.picked, number] })}>Season {number}</button>)}</div> : <span className="mm-small mm-muted">TMDB lists no seasons for this show yet.</span>}
              <div><button type="button" className="mm-btn mm-btn-primary mm-btn-small" disabled={state.busy || !state.picked.length} onClick={() => void add(result)}>{state.busy ? <LoaderCircle size={18} className="animate-spin" aria-hidden /> : null}{state.picked.length ? `Add ${state.picked.length} ${state.picked.length === 1 ? "season" : "seasons"}` : "Pick a season"}</button></div>
            </div>}
          {state.error ? <span role="alert" className="mm-small" data-tone="warn">{state.error}</span> : null}
        </div>
      </article>
    })}</div>
  </div>
}

export function AppAdd() {
  return <AppShell section="add"><AddContent /></AppShell>
}
