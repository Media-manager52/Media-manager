import { ChevronRight, ExternalLink, UserRound } from "lucide-react"
import { useState, type ReactNode } from "react"
import { Link } from "react-router"

import type { APIMedia, APIMetadataCredit } from "@/api/client"
import { Poster, sizedArtwork } from "@/components/poster"
import { cx } from "@/components/ui"
import { kindLabel, languageName } from "@/i18n"
import { castList, creditNames, titleFacts } from "@/lib/title-facts"

/**
 * The top of a title page: the wide artwork with the name and the key facts
 * on it, like a streaming service, with the poster beside it on wider screens.
 * Without wide artwork the poster, blurred, fills the picture instead.
 */
export function TitleHero({ media }: { media: APIMedia }) {
  const meta = media.metadata
  const backdrop = sizedArtwork(meta?.backdrop_url, "w1280")
  const poster = sizedArtwork(meta?.poster_url, "w342")
  const facts = titleFacts(media)
  const libraryKind = media.kind === "series" ? "serier" : "film"
  return (
    <>
      <nav aria-label="Brødkrumme" className="mb-3 flex items-center gap-1 text-sm text-text-3">
        <Link to="/bibliotek" className="hover:text-text hover:underline">Bibliotek</Link>
        <ChevronRight aria-hidden className="size-3.5" />
        <Link to={`/bibliotek?type=${libraryKind}`} className="hover:text-text hover:underline">{media.kind === "series" ? "Serier" : "Film"}</Link>
      </nav>
      <section aria-label={media.title} className="relative flex min-h-60 items-end overflow-hidden rounded-2xl bg-[#1d1d1f] shadow-card sm:min-h-[21rem] lg:min-h-[25rem]">
        {backdrop ? <img src={backdrop} alt="" className="absolute inset-0 size-full object-cover" /> : poster ? <img src={poster} alt="" className="absolute inset-0 size-full scale-110 object-cover opacity-60 blur-2xl" /> : null}
        <div aria-hidden className="absolute inset-0 bg-gradient-to-t from-black/85 via-black/35 to-black/5" />
        <div aria-hidden className="absolute inset-0 hidden bg-gradient-to-r from-black/55 via-black/10 to-transparent sm:block" />
        <div className="relative flex w-full items-end gap-6 p-5 sm:p-7">
          <Poster src={poster} title={media.title} kind={media.kind} className="hidden w-32 shrink-0 shadow-lift ring-1 ring-white/15 sm:block lg:w-40" />
          <div className="min-w-0 text-white">
            <h1 className="break-words text-3xl font-bold leading-tight tracking-tight sm:text-5xl">{media.title}</h1>
            {meta?.original_title && meta.original_title !== media.title ? <div className="mt-1 text-white/75">{meta.original_title}</div> : null}
            {meta?.tagline ? <p className="mt-2 max-w-2xl text-[15px] italic text-white/85">{meta.tagline}</p> : null}
            <ul aria-label="Fakta" className="mt-3 flex flex-wrap gap-1.5">
              {[kindLabel(media.kind), ...facts].map((fact) => <li key={fact} className="rounded-full bg-white/15 px-2.5 py-1 text-xs font-medium text-white backdrop-blur">{fact}</li>)}
            </ul>
          </div>
        </div>
      </section>
    </>
  )
}

/** The plot with genres, and beside it who made the title, who is in it and where to read more. */
export function TitleAbout({ media, overview }: { media: APIMedia; overview?: ReactNode }) {
  const meta = media.metadata
  const credits = meta?.credits ?? []
  const directors = creditNames(credits, ["Director"], 3)
  const creators = creditNames(credits, ["Creator", "Showrunner"], 3)
  const starring = castList(credits).slice(0, 4).map((credit) => credit.name)
  const networks = [...new Set((meta?.networks ?? []).map((network) => network.name).filter(Boolean))].slice(0, 3)
  const sources = uniqueSources(meta?.sources)
  const rows: Array<[string, string]> = [
    ...(directors.length ? [["Instrueret af", directors.join(", ")] as [string, string]] : []),
    ...(creators.length ? [["Skabt af", creators.join(", ")] as [string, string]] : []),
    ...(starring.length ? [["Medvirkende", starring.join(", ")] as [string, string]] : []),
    ...(networks.length ? [[media.kind === "series" ? "Kanal" : "Udgivet af", networks.join(", ")] as [string, string]] : []),
    ...(meta?.original_language ? [["Originalsprog", languageName(meta.original_language)] as [string, string]] : []),
  ]
  if (!overview && !rows.length && !sources.length && !meta?.genres?.length) return null
  return (
    <div className="mb-8 mt-6 grid gap-x-10 gap-y-5 lg:grid-cols-[minmax(0,1fr)_19rem]">
      <div className="min-w-0">
        {overview}
        {meta?.genres?.length ? <ul aria-label="Genrer" className="mt-4 flex flex-wrap gap-1.5">{meta.genres.map((genre) => <li key={genre} className="rounded-full border border-line px-2.5 py-0.5 text-xs text-text-2">{genre}</li>)}</ul> : null}
      </div>
      {rows.length || sources.length ? (
        <div className="min-w-0 text-sm">
          {rows.length ? (
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2">
              {rows.map(([label, value]) => <div key={label} className="contents"><dt className="text-text-3">{label}</dt><dd className="min-w-0 font-medium">{value}</dd></div>)}
            </dl>
          ) : null}
          {sources.length ? (
            <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-text-3">
              {sources.map((source) => (
                <a key={`${source.provider}-${source.provider_id}`} href={source.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 hover:text-text">
                  {providerName(source.provider)} <ExternalLink aria-hidden className="size-3.5" />
                </a>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

/** A row of the cast with their photos, scrolling sideways. */
export function CastRow({ credits }: { credits?: APIMetadataCredit[] }) {
  const cast = castList(credits ?? []).slice(0, 16)
  if (cast.length < 2) return null
  return (
    <section aria-labelledby="cast-heading" className="mb-8">
      <h2 id="cast-heading" className="mb-3 text-base font-semibold">Medvirkende</h2>
      <ul className="-mx-1 flex gap-4 overflow-x-auto px-1 pb-2">
        {cast.map((credit) => <CastMember key={`${credit.name}-${credit.character ?? ""}`} credit={credit} />)}
      </ul>
    </section>
  )
}

function CastMember({ credit }: { credit: APIMetadataCredit }) {
  const [failed, setFailed] = useState(false)
  const photo = credit.profile_url && !failed ? sizedArtwork(credit.profile_url, "w185") : undefined
  return (
    <li className="w-24 shrink-0 text-center">
      <div className="mx-auto size-20 overflow-hidden rounded-full bg-surface-3 shadow-card">
        {photo ? <img src={photo} alt="" loading="lazy" onError={() => setFailed(true)} className="size-full object-cover" /> : <div className="flex size-full items-center justify-center text-text-3"><UserRound aria-hidden className="size-7" /></div>}
      </div>
      <div className="mt-2 line-clamp-2 text-xs font-semibold leading-snug">{credit.name}</div>
      {credit.character ? <div className={cx("line-clamp-2 text-[11px] leading-snug text-text-3")}>{credit.character}</div> : null}
    </li>
  )
}

function providerName(provider: string): string {
  return { tmdb: "TMDB", tvdb: "TheTVDB", imdb: "IMDb", simkl: "Simkl" }[provider] ?? provider.toUpperCase()
}

function uniqueSources<T extends { provider: string; url?: string }>(sources: T[] | undefined): T[] {
  const seen = new Set<string>()
  return (sources ?? []).filter((source) => {
    if (!source.url || seen.has(source.provider)) return false
    seen.add(source.provider)
    return true
  })
}
