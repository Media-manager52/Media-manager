import { useQuery } from "@tanstack/react-query"
import { ChevronRight, Plus, SearchCheck } from "lucide-react"
import { useDeferredValue, useMemo, useState } from "react"
import { Link, useSearchParams } from "react-router"

import { loadCatalogMediaBounded, loadLibraryGaps, loadLibraryRoots, loadMediaStatus, type APIMedia, type APIMediaStatus } from "@/api/client"
import { Poster, sizedArtwork } from "@/components/poster"
import { Button, Card, cx, Dot, Empty, ErrorBox, PageHeader, SearchField, Segmented, Skeleton } from "@/components/ui"
import { formatDateTime, formatNumber } from "@/format"
import { filterMedia, librarySorts, sortMedia, type LibraryKind as Kind, type LibrarySort as Sort } from "@/lib/library-filter"
import { titleHref } from "@/lib/links"

type Status = "alle" | "mangler" | "handling" | "ikke-hentet"
const statuses: Status[] = ["alle", "mangler", "handling", "ikke-hentet"]
const pageSize = 120

/** A new `?q=` from the search field in the menu starts the page over with that search. */
export function LibraryPage() {
  const [params] = useSearchParams()
  return <LibraryView key={params.get("q") ?? ""} />
}

function LibraryView() {
  const [params, setParams] = useSearchParams()
  const kind = (["alle", "film", "serier"].includes(params.get("type") ?? "") ? params.get("type") : "alle") as Kind
  const sort = (librarySorts.includes(params.get("sort") as Sort) ? params.get("sort") : "titel") as Sort
  const folder = params.get("mappe") ?? ""
  const status = (statuses.includes(params.get("status") as Status) ? params.get("status") : "alle") as Status
  const [search, setSearch] = useState(params.get("q") ?? "")
  const deferredSearch = useDeferredValue(search)
  const [visible, setVisible] = useState(pageSize)
  const catalog = useQuery({ queryKey: ["media", "catalog"], queryFn: ({ signal }) => loadCatalogMediaBounded(signal), staleTime: 60_000 })
  const all = useMemo(() => catalog.data?.items ?? [], [catalog.data])
  const counts = useMemo(() => ({ film: all.filter((item) => item.kind === "movie").length, serier: all.filter((item) => item.kind === "series").length }), [all])
  // A failed/missing status is unknown, never a successful empty filter.
  const state = useQuery({ queryKey: ["media", "status"], queryFn: ({ signal }) => loadMediaStatus(signal), staleTime: 60_000, retry: false })
  const statusUnavailable = state.data === undefined || state.isError
  const byId = useMemo(() => new Map((state.isError ? [] : state.data ?? []).map((entry) => [entry.media_id, entry])), [state.data, state.isError])
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal), staleTime: 60_000 })
  const folders = useMemo(() => libraryFolders(roots.data ?? [], kind, state.isError ? [] : state.data ?? []), [roots.data, kind, state.data, state.isError])
  const addedAt = useMemo(() => new Map([...byId.values()].filter((entry) => entry.added_at).map((entry) => [entry.media_id, entry.added_at!])), [byId])
  const kindItems = useMemo(() => filterMedia(all, kind, ""), [all, kind])
  const unknownCount = kindItems.filter((item) => !byId.has(item.id)).length
  const statusCounts = useMemo(() => {
    const counts: Record<Status, number> = { alle: kindItems.length, mangler: 0, handling: 0, "ikke-hentet": 0 }
    for (const item of kindItems) {
      const entry = byId.get(item.id)
      for (const value of statuses.slice(1)) if (entry && matchesStatus(entry, value)) counts[value]++
    }
    return counts
  }, [kindItems, byId])
  const items = useMemo(() => {
    const filtered = filterMedia(all, kind, deferredSearch)
      .filter((item) => status === "alle" || (!statusUnavailable && byId.has(item.id) && matchesStatus(byId.get(item.id)!, status)))
      .filter((item) => !folder || inFolder(byId.get(item.id), folder))
    return sortMedia(filtered, sort, addedAt)
  }, [all, kind, deferredSearch, sort, status, folder, byId, addedAt, statusUnavailable])

  const update = (key: string, value: string, fallback: string) => {
    const next = new URLSearchParams(params)
    if (value === fallback) next.delete(key)
    else next.set(key, value)
    setParams(next, { replace: true })
    setVisible(pageSize)
  }

  const heading = kind === "film" ? "Film" : kind === "serier" ? "Serier" : "Bibliotek"
  const total = kind === "film" ? counts.film : kind === "serier" ? counts.serier : all.length
  return (
    <>
      <PageHeader
        title={heading}
        eyebrow={kind === "alle" ? undefined : <Link to="/bibliotek" className="hover:text-text hover:underline">Bibliotek</Link>}
        description={catalog.data ? `${formatNumber(total)} ${kind === "film" ? "film" : kind === "serier" ? (total === 1 ? "serie" : "serier") : "film og serier"}, som Media Manager kender.` : "Alle film og serier, som Media Manager kender."}
        actions={<><Link to="/bibliotek/mangler"><Button><SearchCheck aria-hidden className="size-4" />Find manglende</Button></Link><Link to="/tilfoej" className="inline-flex h-9 items-center gap-2 rounded-lg bg-text px-3.5 text-sm font-semibold text-bg hover:opacity-90"><Plus aria-hidden className="size-4" />Tilføj titel</Link></>}
      />
      <div className="mb-4 flex flex-wrap items-center gap-2.5">
        <div className="lg:hidden">
          <Segmented
            label="Type"
            value={kind}
            onChange={(value) => update("type", value, "alle")}
            options={[
              { value: "alle", label: "Alle", count: all.length || undefined },
              { value: "film", label: "Film", count: counts.film || undefined },
              { value: "serier", label: "Serier", count: counts.serier || undefined },
            ]}
          />
        </div>
        <Segmented
          label="Status"
          value={status}
          onChange={(value) => update("status", value, "alle")}
          options={[
            { value: "alle", label: "Alle" },
            { value: "mangler", label: "Mangler noget", count: statusCounts.mangler || undefined },
            { value: "handling", label: "Kræver handling", count: statusCounts.handling || undefined },
            { value: "ikke-hentet", label: "Ikke hentet", count: statusCounts["ikke-hentet"] || undefined },
          ]}
        />
        <div className="flex w-full flex-wrap items-center gap-2.5 xl:ml-auto xl:w-auto">
          <SearchField value={search} onChange={(value) => { setSearch(value); setVisible(pageSize) }} placeholder="Søg i titler og originaltitler" label="Søg i biblioteket" />
          {folders.length > 0 || folder ? (
            <label className="flex items-center gap-2 text-sm text-text-2">
              Mappe
              <select value={folder} onChange={(event) => update("mappe", event.target.value, "")} className="h-9 max-w-48 rounded-lg border border-line bg-surface px-2 text-text">
                <option value="">Alle mapper</option>
                {folder && !folders.some((entry) => entry.id === folder) ? <option value={folder}>Valgt mappe</option> : null}
                {folders.map((entry) => <option key={entry.id} value={entry.id}>{entry.name} ({formatNumber(entry.count)})</option>)}
              </select>
            </label>
          ) : null}
          <label className="flex items-center gap-2 text-sm text-text-2">
            Sortér
            <select value={sort} onChange={(event) => update("sort", event.target.value, "titel")} className="h-9 rounded-lg border border-line bg-surface px-2 text-text">
              <option value="titel">Titel</option>
              <option value="senest-tilfoejet">Senest tilføjet</option>
              <option value="aar-nyeste">År, nyeste først</option>
              <option value="aar-aeldste">År, ældste først</option>
            </select>
          </label>
        </div>
      </div>
      <GapsNote kind={kind} />
      {catalog.error ? <ErrorBox error={catalog.error} retry={() => catalog.refetch()} /> : null}
      {state.isPending ? <p role="status" className="mb-4 text-sm text-text-2">Henter status…</p> : null}
      {state.error ? <div className="mb-4 space-y-2"><p className="text-sm text-warn">Titlernes status er ukendt. Prøv igen for at se, hvad der mangler.</p><ErrorBox error={state.error} retry={() => state.refetch()} /></div> : null}
      {!statusUnavailable && state.dataUpdatedAt ? (
        <div className="mb-4 flex flex-wrap items-center gap-2 text-xs text-text-3">
          <span role="status">Status kontrolleret {formatDateTime(new Date(state.dataUpdatedAt).toISOString())}.</span>
          <Button variant="ghost" className="h-7 text-xs" busy={state.isFetching} onClick={() => state.refetch()}>Kontrollér igen</Button>
        </div>
      ) : null}
      {!statusUnavailable && unknownCount > 0 ? <p role="status" className="mb-4 text-sm text-warn">Status mangler for {formatNumber(unknownCount)} titler. Filteret viser kun titler med kendt status.</p> : null}
      {sort === "senest-tilfoejet" && !statusUnavailable ? <p className="mb-4 text-xs text-text-3">Titler med en fil står først, den nyeste øverst. Titler uden fil står til sidst.</p> : null}
      {(sort === "senest-tilfoejet" || folder) && statusUnavailable && !state.isPending ? <p role="status" className="mb-4 text-sm text-warn">Mappe og "Senest tilføjet" kræver titlernes status, som ikke kunne hentes.</p> : null}
      {catalog.data?.truncated ? <p className="mb-4 text-sm text-warn">Biblioteket er større end det, siden kan vise på én gang. Brug søgningen.</p> : null}
      {catalog.isPending ? <PosterGridSkeleton /> : null}
      {!catalog.isPending && items.length === 0 && !catalog.error && folder && !statusUnavailable ? (
        <Card><Empty title="Ingen titler i den mappe passer">Vælg en anden mappe, eller ryd de andre filtre.</Empty></Card>
      ) : null}
      {!catalog.isPending && items.length === 0 && !catalog.error && !folder && (status === "alle" || (!statusUnavailable && unknownCount === 0)) ? (
        <Card><Empty title={status !== "alle" && !search ? statusEmpty[status] : search ? "Ingen titler passer til søgningen" : all.length > 0 ? (kind === "film" ? "Ingen film endnu" : "Ingen serier endnu") : "Biblioteket er tomt"}>{search ? "Prøv et andet ord, eller tilføj titlen." : all.length > 0 ? "Tilføj en titel, eller vælg en anden type." : "Tilføj din første film eller serie."}</Empty></Card>
      ) : null}
      <ul className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-x-4 gap-y-7 sm:grid-cols-[repeat(auto-fill,minmax(10rem,1fr))] sm:gap-x-5">
        {items.slice(0, visible).map((item) => <PosterTile key={item.id} item={item} status={byId.get(item.id)} />)}
      </ul>
      {items.length > visible ? (
        <div className="mt-8 flex justify-center">
          <Button onClick={() => setVisible((count) => count + pageSize)}>Vis flere ({formatNumber(items.length - visible)} tilbage)</Button>
        </div>
      ) : null}
    </>
  )
}

const statusEmpty: Record<Exclude<Status, "alle">, string> = {
  mangler: "Intet mangler. Alt, der ventes på, ligger i Plex.",
  handling: "Intet kræver din handling.",
  "ikke-hentet": "Alle titler har noget i Plex eller er i gang.",
}

/**
 * Which library filter a title falls under: "Mangler noget" while a wanted
 * film, season or episode has no file yet, "Kræver handling" while open work
 * waits for the owner, and "Ikke hentet" when nothing is in Plex and nothing
 * is being fetched (for example after a cancel), so Hent is the next step.
 */
export function matchesStatus(entry: APIMediaStatus, status: Status): boolean {
  if (status === "mangler") return entry.missing > 0
  if (status === "handling") return entry.attention
  if (status === "ikke-hentet") return !entry.in_plex && entry.missing === 0
  return true
}

/** Whether a title has a file in the library folder; unknown status is never a match. */
export function inFolder(entry: APIMediaStatus | undefined, folder: string): boolean {
  return Boolean(entry?.root_ids?.includes(folder))
}

/**
 * The library folders that hold at least one title of the chosen type, with
 * how many titles each holds, in the order the folders are set up.
 */
export function libraryFolders(roots: Array<{ id: string; name: string; media_kind: "movie" | "series" }>, kind: Kind, statuses: APIMediaStatus[]): Array<{ id: string; name: string; count: number }> {
  const counts = new Map<string, number>()
  for (const entry of statuses) for (const id of entry.root_ids ?? []) counts.set(id, (counts.get(id) ?? 0) + 1)
  return roots
    .filter((root) => kind === "alle" || (kind === "film" ? root.media_kind === "movie" : root.media_kind === "series"))
    .filter((root) => counts.has(root.id))
    .map((root) => ({ id: root.id, name: root.name, count: counts.get(root.id)! }))
}

/** The status shown under a poster; a title that is complete in Plex shows none. */
export function tileStatus(item: APIMedia, entry?: APIMediaStatus): { tone: "warn" | "info" | "neutral"; text: string } | null {
  if (!entry) return null
  if (entry.attention) return { tone: "warn", text: "Kræver handling" }
  if (entry.missing > 0) return { tone: "info", text: item.kind === "series" && entry.in_plex ? "Mangler afsnit" : "Mangler" }
  if (!entry.in_plex) return { tone: "neutral", text: "Ikke hentet" }
  return null
}

function PosterTile({ item, status }: { item: APIMedia; status?: APIMediaStatus }) {
  const state = tileStatus(item, status)
  return (
    <li className="[content-visibility:auto] [contain-intrinsic-size:auto_300px]">
      <Link to={titleHref(item.id)} className="group block">
        <div className="rounded-xl shadow-poster transition duration-200 group-hover:-translate-y-1 group-hover:shadow-lift">
          <Poster src={sizedArtwork(item.metadata?.poster_url, "w342")} title={item.title} kind={item.kind} />
        </div>
        <div className="mt-2.5 line-clamp-2 text-sm font-semibold leading-snug tracking-tight">{item.title}</div>
        <div className="mt-0.5 text-xs text-text-3">{[item.year, item.kind === "series" ? "Serie" : "Film"].filter(Boolean).join(" · ")}</div>
        {state ? <div className={cx("mt-1 flex items-center gap-1.5 text-xs font-medium", state.tone === "warn" ? "text-warn" : "text-text-2")}><Dot tone={state.tone} />{state.text}</div> : null}
      </Link>
    </li>
  )
}

/**
 * A quiet note above the posters when the library has seasons or films that
 * are not followed yet; adding them stays a click on Find manglende.
 */
function GapsNote({ kind }: { kind: Kind }) {
  const gaps = useQuery({ queryKey: ["media", "gaps"], queryFn: ({ signal }) => loadLibraryGaps(signal), staleTime: 5 * 60_000, retry: false })
  if (!gaps.data) return null
  const series = kind === "film" ? 0 : gaps.data.series.filter((entry) => entry.new_seasons.length > 0).length
  const movies = kind === "serier" ? 0 : gaps.data.movies.length
  if (series === 0 && movies === 0) return null
  const parts = [
    series ? `${formatNumber(series)} ${series === 1 ? "serie har nye sæsoner" : "serier har nye sæsoner"}, som du ikke følger endnu` : "",
    movies ? `${formatNumber(movies)} film har ingen fil og intet arbejde i gang` : "",
  ].filter(Boolean)
  return (
    <div className="mb-6 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-2xl border border-line bg-surface-2 px-4 py-3 text-sm">
      <SearchCheck aria-hidden className="size-4 shrink-0 text-accent-strong" />
      <p className="min-w-0 flex-1 text-text-2">{parts.join(", og ")}. Du vælger selv, hvad der skal hentes.</p>
      <Link to={`/bibliotek/mangler${series === 0 && movies > 0 ? "?vis=film" : ""}`} className="inline-flex w-full items-center gap-1 pl-8 font-medium text-accent-strong hover:underline sm:w-auto sm:pl-0">Se hvad der mangler<ChevronRight aria-hidden className="size-4" /></Link>
    </div>
  )
}

function PosterGridSkeleton() {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-x-4 gap-y-6 sm:grid-cols-[repeat(auto-fill,minmax(10rem,1fr))]">
      {Array.from({ length: 18 }, (_, index) => (
        <div key={index}>
          <Skeleton className="aspect-[2/3] rounded-xl" />
          <Skeleton className="mt-2.5 h-4 w-3/4" />
          <Skeleton className="mt-1.5 h-3 w-1/2" />
        </div>
      ))}
    </div>
  )
}

export { filterMedia, sortMedia }
