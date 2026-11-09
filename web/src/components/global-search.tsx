import { useQuery } from "@tanstack/react-query"
import { Search } from "lucide-react"
import { useId, useMemo, useState, type FormEvent } from "react"
import { Link, useNavigate } from "react-router"

import { loadCatalogMediaBounded } from "@/api/client"
import { Poster, sizedArtwork } from "@/components/poster"
import { searchTitles } from "@/lib/library-filter"
import { titleHref } from "@/lib/links"

/**
 * Search field in the menu: finds a title in the library from any page. The
 * library list is loaded the first time the field is used and then shared
 * with the library page.
 */
export function GlobalSearch({ onNavigate, autoFocus = false }: { onNavigate?: () => void; autoFocus?: boolean }) {
  const id = useId()
  const navigate = useNavigate()
  const [value, setValue] = useState("")
  const [open, setOpen] = useState(false)
  const [armed, setArmed] = useState(autoFocus)
  const catalog = useQuery({ queryKey: ["media", "catalog"], queryFn: ({ signal }) => loadCatalogMediaBounded(signal), staleTime: 60_000, enabled: armed })
  const needle = value.trim()
  const matches = useMemo(() => searchTitles(catalog.data?.items ?? [], needle, 6), [catalog.data, needle])
  const leave = () => {
    setOpen(false)
    setValue("")
    onNavigate?.()
  }
  const onSubmit = (event: FormEvent) => {
    event.preventDefault()
    if (needle.length < 2) return
    navigate(`/bibliotek?q=${encodeURIComponent(needle)}`)
    leave()
  }
  const showResults = open && needle.length >= 2
  return (
    <div
      className="px-1"
      onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false) }}
      onKeyDown={(event) => { if (event.key === "Escape") setOpen(false) }}
    >
      <form role="search" onSubmit={onSubmit} className="relative">
        <label htmlFor={id} className="sr-only">Søg i dine titler</label>
        <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-text-3" />
        <input
          id={id}
          type="search"
          autoFocus={autoFocus}
          value={value}
          onFocus={() => { setArmed(true); setOpen(true) }}
          onChange={(event) => { setValue(event.target.value); setOpen(true) }}
          placeholder="Søg i dine titler"
          autoComplete="off"
          className="h-10 w-full rounded-lg border border-line bg-surface pl-9 pr-3 text-sm placeholder:text-text-3 lg:h-9"
        />
      </form>
      {showResults ? (
        <div className="mt-2 rounded-lg border border-line bg-surface text-sm shadow-card">
          {catalog.isPending ? <p role="status" className="px-3 py-2 text-text-2">Søger…</p> : null}
          {catalog.error ? <p role="alert" className="px-3 py-2 text-bad">Biblioteket kunne ikke hentes. Prøv igen.</p> : null}
          {catalog.data && matches.length === 0 ? <p className="px-3 py-2 text-text-2">Ingen af dine titler passer.</p> : null}
          {matches.length > 0 ? (
            <ul aria-label="Fundne titler" className="divide-y divide-line">
              {matches.map((item) => (
                <li key={item.id}>
                  <Link to={titleHref(item.id)} onClick={leave} className="flex items-center gap-2.5 px-3 py-2 hover:bg-surface-2">
                    <Poster compact src={sizedArtwork(item.metadata?.poster_url, "w185")} title={item.title} kind={item.kind} className="w-8 shrink-0 rounded-md" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-medium">{item.title}</span>
                      <span className="block truncate text-xs text-text-3">{[item.year, item.kind === "series" ? "Serie" : "Film"].filter(Boolean).join(" · ")}</span>
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          ) : null}
          <div className="flex flex-col border-t border-line">
            {matches.length > 0 ? <Link to={`/bibliotek?q=${encodeURIComponent(needle)}`} onClick={leave} className="px-3 py-2 font-medium text-accent-strong hover:bg-surface-2">Se alle i biblioteket</Link> : null}
            <Link to={`/tilfoej?q=${encodeURIComponent(needle)}`} onClick={leave} className="px-3 py-2 font-medium text-accent-strong hover:bg-surface-2">Find "{needle}" som ny titel</Link>
          </div>
        </div>
      ) : null}
    </div>
  )
}
