import { useQuery } from "@tanstack/react-query"
import { Plus } from "lucide-react"
import { useDeferredValue, useMemo, useState } from "react"
import { Link, useSearchParams } from "react-router"

import { loadCatalogMediaBounded, type APIMedia } from "@/api/client"
import { Poster, sizedArtwork } from "@/components/poster"
import { Button, Card, Empty, ErrorBox, PageHeader, SearchField, Segmented, Skeleton } from "@/components/ui"
import { formatNumber } from "@/format"
import { titleHref } from "@/lib/links"

type Kind = "alle" | "film" | "serier"
type Sort = "titel" | "aar-nyeste" | "aar-aeldste"
const pageSize = 120

export function LibraryPage() {
  const [params, setParams] = useSearchParams()
  const kind = (["alle", "film", "serier"].includes(params.get("type") ?? "") ? params.get("type") : "alle") as Kind
  const sort = (["titel", "aar-nyeste", "aar-aeldste"].includes(params.get("sort") ?? "") ? params.get("sort") : "titel") as Sort
  const [search, setSearch] = useState(params.get("q") ?? "")
  const deferredSearch = useDeferredValue(search)
  const [visible, setVisible] = useState(pageSize)
  const catalog = useQuery({ queryKey: ["media", "catalog"], queryFn: ({ signal }) => loadCatalogMediaBounded(signal), staleTime: 60_000 })
  const all = useMemo(() => catalog.data?.items ?? [], [catalog.data])
  const counts = useMemo(() => ({ film: all.filter((item) => item.kind === "movie").length, serier: all.filter((item) => item.kind === "series").length }), [all])
  const items = useMemo(() => sortMedia(filterMedia(all, kind, deferredSearch), sort), [all, kind, deferredSearch, sort])

  const update = (key: string, value: string, fallback: string) => {
    const next = new URLSearchParams(params)
    if (value === fallback) next.delete(key)
    else next.set(key, value)
    setParams(next, { replace: true })
    setVisible(pageSize)
  }

  return (
    <>
      <PageHeader
        title="Bibliotek"
        description="Alle film og serier, som Media Manager kender."
        actions={<Link to="/tilfoej"><Button variant="primary"><Plus aria-hidden className="size-4" />Tilføj titel</Button></Link>}
      />
      <div className="mb-5 flex flex-wrap items-center gap-3">
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
        <SearchField value={search} onChange={(value) => { setSearch(value); setVisible(pageSize) }} placeholder="Søg i titler og originaltitler" label="Søg i biblioteket" />
        <label className="ml-auto flex items-center gap-2 text-sm text-text-2">
          Sortér
          <select value={sort} onChange={(event) => update("sort", event.target.value, "titel")} className="h-9 rounded-lg border border-line bg-surface px-2 text-text">
            <option value="titel">Titel</option>
            <option value="aar-nyeste">År, nyeste først</option>
            <option value="aar-aeldste">År, ældste først</option>
          </select>
        </label>
      </div>
      {catalog.error ? <ErrorBox error={catalog.error} retry={() => catalog.refetch()} /> : null}
      {catalog.data?.truncated ? <p className="mb-4 text-sm text-warn">Biblioteket er større end det, siden kan vise på én gang. Brug søgningen.</p> : null}
      {catalog.isPending ? <PosterGridSkeleton /> : null}
      {!catalog.isPending && items.length === 0 && !catalog.error ? (
        <Card><Empty title={search ? "Ingen titler passer til søgningen" : "Biblioteket er tomt"}>{search ? "Prøv et andet ord, eller tilføj titlen." : "Tilføj din første film eller serie."}</Empty></Card>
      ) : null}
      <ul className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-x-4 gap-y-6 sm:grid-cols-[repeat(auto-fill,minmax(10rem,1fr))]">
        {items.slice(0, visible).map((item) => <PosterTile key={item.id} item={item} />)}
      </ul>
      {items.length > visible ? (
        <div className="mt-8 flex justify-center">
          <Button onClick={() => setVisible((count) => count + pageSize)}>Vis flere ({formatNumber(items.length - visible)} tilbage)</Button>
        </div>
      ) : null}
    </>
  )
}

function PosterTile({ item }: { item: APIMedia }) {
  return (
    <li className="[content-visibility:auto] [contain-intrinsic-size:auto_300px]">
      <Link to={titleHref(item.id)} className="group block">
        <Poster src={sizedArtwork(item.metadata?.poster_url, "w342")} title={item.title} kind={item.kind} className="transition-transform group-hover:-translate-y-0.5 group-hover:shadow-card" />
        <div className="mt-2 line-clamp-2 text-sm font-medium leading-snug group-hover:underline">{item.title}</div>
        <div className="text-xs text-text-3">{[item.year, item.kind === "series" ? "Serie" : "Film"].filter(Boolean).join(" · ")}</div>
      </Link>
    </li>
  )
}

function PosterGridSkeleton() {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-x-4 gap-y-6 sm:grid-cols-[repeat(auto-fill,minmax(10rem,1fr))]">
      {Array.from({ length: 18 }, (_, index) => (
        <div key={index}>
          <Skeleton className="aspect-[2/3] rounded-xl" />
          <Skeleton className="mt-2 h-4 w-3/4" />
        </div>
      ))}
    </div>
  )
}

const fold = (value: string) => value.toLocaleLowerCase("da").normalize("NFD").replace(/[̀-ͯ]/g, "")

export function filterMedia(items: APIMedia[], kind: Kind, search: string): APIMedia[] {
  const needle = fold(search.trim())
  return items.filter((item) => {
    if (kind === "film" && item.kind !== "movie") return false
    if (kind === "serier" && item.kind !== "series") return false
    if (!needle) return true
    const names = [item.title, item.metadata?.original_title ?? "", ...(item.metadata?.aliases ?? []).map((alias) => alias.name)]
    return names.some((name) => fold(name).includes(needle)) || String(item.year ?? "") === needle
  })
}

export function sortMedia(items: APIMedia[], sort: Sort): APIMedia[] {
  const collator = new Intl.Collator("da", { sensitivity: "base", numeric: true })
  const sorted = [...items]
  if (sort === "titel") sorted.sort((a, b) => collator.compare(a.title, b.title))
  if (sort === "aar-nyeste") sorted.sort((a, b) => (b.year ?? 0) - (a.year ?? 0) || collator.compare(a.title, b.title))
  if (sort === "aar-aeldste") sorted.sort((a, b) => (a.year ?? 9999) - (b.year ?? 9999) || collator.compare(a.title, b.title))
  return sorted
}
