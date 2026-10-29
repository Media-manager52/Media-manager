"use client"

import * as React from "react"
import Link from "next/link"
import { ChevronRight, Film } from "lucide-react"

import { relativeTime } from "@/lib/app-presentation"
import { useAppData, type AppTitle } from "./app-data"
import { AppShell } from "./shell"
import { LoadState, NeedsCard, PageTitle, PosterRow, Segments, Thumb, titleHref } from "./parts"

const movingStages = new Set(["finding", "downloading", "checking", "adding"])
const homeNeedsLimit = 3
const rowLimit = 12

/** Latest persisted activity for a title: a confirmed file or a workflow change. */
function lastActivity(title: AppTitle) {
  const times = [...title.files.map((file) => file.verified_at ?? ""), ...title.workflows.map((workflow) => workflow.updated_at)]
  return times.reduce((latest, value) => (value > latest ? value : latest), "")
}

function lastConfirmed(title: AppTitle) {
  return title.files.filter((file) => file.plex_confirmed && file.state === "current").reduce((latest, file) => ((file.verified_at ?? "") > latest ? file.verified_at ?? "" : latest), "")
}

function greeting(hour: number) {
  if (hour < 5) return "Good night"
  if (hour < 12) return "Good morning"
  if (hour < 18) return "Good afternoon"
  return "Good evening"
}

const subscribeNever = () => () => undefined

function plural(count: number, one: string, many: string) {
  return `${count} ${count === 1 ? one : many}`
}

function MovingCard({ title }: { title: AppTitle }) {
  const workflow = title.status.workflow
  const scope = workflow && /^season:0*(\d+)/.exec(workflow.variant)
  const detail = scope ? `Season ${scope[1]}` : workflow ? `Updated ${relativeTime(workflow.updated_at)}` : ""
  return <Link href={titleHref(title.id)} className="mm-moving" aria-label={`${title.title}, ${title.status.label}`}>
    <Thumb title={title.title} src={title.metadata?.poster_url} />
    <span className="mm-moving-body">
      <span className="mm-ellipsis" style={{ fontWeight: 600 }}>{title.title}</span>
      <span className="mm-small" data-tone="info">{title.status.label}</span>
      <Segments status={title.status} />
      <span className="mm-small mm-muted mm-ellipsis">{detail}</span>
    </span>
  </Link>
}

function HomeContent() {
  const { data, error } = useAppData()
  // The server cannot know the owner's local hour, so it renders a neutral greeting.
  const hello = React.useSyncExternalStore(subscribeNever, () => greeting(new Date().getHours()), () => "Welcome back")
  const titles = data?.titles ?? []
  const needs = data?.needs ?? []
  const moving = titles.filter((title) => movingStages.has(title.status.stage)).sort((a, b) => b.status.step - a.status.step)
  const waiting = titles.filter((title) => title.status.stage === "waiting")
  const inPlex = titles.filter((title) => title.status.stage === "in_plex")
  const recent = inPlex.filter((title) => lastConfirmed(title)).sort((a, b) => lastConfirmed(b).localeCompare(lastConfirmed(a))).slice(0, rowLimit)
  const byActivity = (kind: "movie" | "series") => titles.filter((title) => title.kind === kind).sort((a, b) => lastActivity(b).localeCompare(lastActivity(a)) || a.title.localeCompare(b.title)).slice(0, rowLimit)
  const summary = data ? [
    needs.length ? plural(needs.length, "thing needs you", "things need you") : "Nothing needs you",
    `${moving.length} on the way`,
    `${plural(inPlex.length, "title", "titles")} in Plex`,
  ].join(" · ") : undefined

  return <div className="mm-stack">
    <PageTitle title={hello} lede={summary} />
    <LoadState error={error} loading={!data && !error} />
    {data && !titles.length && !needs.length ? <div className="mm-empty"><Film size={36} aria-hidden /><strong>Your library is empty</strong><p style={{ margin: 0 }}>Add a movie or show and Media Manager takes it from there.</p><div className="mm-actions"><Link href="/add" className="mm-btn mm-btn-primary">Add a title</Link><Link href="/settings" className="mm-btn">Check settings</Link></div></div> : null}
    {needs.length ? <section className="mm-section" aria-labelledby="mm-needs-heading">
      <div className="mm-section-head"><h2 id="mm-needs-heading" className="mm-h2" data-tone="warn">Needs you</h2>{needs.length > homeNeedsLimit ? <Link href="/needs-you" className="mm-see-all">See all {needs.length}<ChevronRight size={16} aria-hidden /></Link> : null}</div>
      {needs.slice(0, homeNeedsLimit).map((item) => <NeedsCard key={item.key} item={item} />)}
    </section> : null}
    {moving.length ? <section className="mm-section" aria-labelledby="mm-moving-heading">
      <h2 id="mm-moving-heading" className="mm-h2">On the way</h2>
      <div className="mm-row">{moving.map((title) => <MovingCard key={title.id} title={title} />)}</div>
    </section> : null}
    <PosterRow heading="Just added to Plex" titles={recent} />
    <PosterRow heading="Movies" titles={byActivity("movie")} href="/library" />
    <PosterRow heading="TV shows" titles={byActivity("series")} href="/library?type=series" />
    {waiting.length ? <p className="mm-small mm-muted">{plural(waiting.length, "title is", "titles are")} waiting for a release. Media Manager keeps looking on its own. <Link href="/library?show=waiting" className="mm-inline-link">See which</Link></p> : null}
  </div>
}

export function AppHome() {
  return <AppShell section="home"><HomeContent /></AppShell>
}
