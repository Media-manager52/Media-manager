"use client"

import * as React from "react"
import { Search } from "lucide-react"

import type { AppStage } from "@/lib/app-presentation"
import { useAppData } from "./app-data"
import { AppShell } from "./shell"
import { LoadState, PageTitle, PosterCard } from "./parts"

type Kind = "movie" | "series"
type Show = "all" | "in_plex" | "moving" | "needs" | "waiting"

const showGroups: Record<Exclude<Show, "all">, AppStage[]> = {
  in_plex: ["in_plex"],
  moving: ["finding", "downloading", "checking", "adding"],
  needs: ["needs_you"],
  waiting: ["waiting", "not_started"],
}
const showLabels: Array<[Show, string]> = [["all", "All"], ["in_plex", "In Plex"], ["moving", "On the way"], ["needs", "Needs you"], ["waiting", "Not yet"]]
const pageSize = 60

function readParams(search: string): { kind: Kind; show: Show } {
  const params = new URLSearchParams(search)
  const kind = params.get("type") === "series" ? "series" : "movie"
  const show = params.get("show")
  return { kind, show: show && show in showGroups ? show as Show : "all" }
}

const subscribeNever = () => () => undefined

function inShow(stage: AppStage, show: Show) {
  return show === "all" || showGroups[show].includes(stage)
}

function LibraryContent() {
  const { data, error } = useAppData()
  // The address decides the first view (links such as "See all" carry it); choices made here win after that.
  const search = React.useSyncExternalStore(subscribeNever, () => window.location.search, () => "")
  const initial = readParams(search)
  const [chosen, setChosen] = React.useState<{ kind?: Kind; show?: Show }>({})
  const kind = chosen.kind ?? initial.kind
  const show = chosen.show ?? initial.show
  const [query, setQuery] = React.useState("")
  const [limit, setLimit] = React.useState(pageSize)
  function update(next: { kind?: Kind; show?: Show }) {
    const nextKind = next.kind ?? kind
    const nextShow = next.show ?? show
    setChosen({ kind: nextKind, show: nextShow }); setLimit(pageSize)
    const params = new URLSearchParams()
    if (nextKind === "series") params.set("type", "series")
    if (nextShow !== "all") params.set("show", nextShow)
    window.history.replaceState(window.history.state, "", `/library${params.size ? `?${params}` : ""}`)
  }
  const needle = query.trim().toLocaleLowerCase()
  const pool = (data?.titles ?? []).filter((title) => title.kind === kind && (!needle || title.searchText.includes(needle)))
  const shown = pool.filter((title) => inShow(title.status.stage, show))
  const counts = { movie: data?.titles.filter((title) => title.kind === "movie").length ?? 0, series: data?.titles.filter((title) => title.kind === "series").length ?? 0 }

  return <div className="mm-stack-tight">
    <PageTitle title="Library" />
    <div className="mm-controls">
      <div className="mm-segmented" role="group" aria-label="Movies or TV shows">
        <button type="button" aria-pressed={kind === "movie"} onClick={() => update({ kind: "movie" })}>Movies <span className="mm-muted">{counts.movie}</span></button>
        <button type="button" aria-pressed={kind === "series"} onClick={() => update({ kind: "series" })}>TV shows <span className="mm-muted">{counts.series}</span></button>
      </div>
      <label className="mm-search"><Search size={18} aria-hidden /><span className="mm-sr">Search your library</span><input type="search" value={query} onChange={(event) => { setQuery(event.target.value); setLimit(pageSize) }} placeholder="Search your library" /></label>
    </div>
    <div className="mm-chips" role="group" aria-label="Show">
      {showLabels.map(([value, label]) => <button key={value} type="button" className="mm-chip" aria-pressed={show === value} onClick={() => update({ show: value })}>{label}<span>{pool.filter((title) => inShow(title.status.stage, value)).length}</span></button>)}
    </div>
    <LoadState error={error} loading={!data && !error} />
    {data && !shown.length ? <p className="mm-state">{pool.length || needle ? "Nothing matches. Try another word or filter." : kind === "movie" ? "No movies yet." : "No TV shows yet."}</p> : null}
    <div className="mm-grid">{shown.slice(0, limit).map((title) => <PosterCard key={title.id} title={title} />)}</div>
    {shown.length > limit ? <div><button type="button" className="mm-btn" onClick={() => setLimit((current) => current + pageSize)}>Show more ({shown.length - limit} left)</button></div> : null}
  </div>
}

export function AppLibrary() {
  return <AppShell section="library"><LibraryContent /></AppShell>
}
