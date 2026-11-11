import { useQuery } from "@tanstack/react-query"
import { Link } from "react-router"

import { loadCalendar, type APICalendarEntry } from "@/api/client"
import { CardGridSkeleton } from "@/components/media-card"
import { sizedArtwork } from "@/components/poster"
import { Badge, Card, Dot, Empty, ErrorBox, PageHeader } from "@/components/ui"
import { formatWeekday, pluralize } from "@/format"
import { stateLabel, stateTone } from "@/i18n"
import { groupCalendarDay, type CalendarGroup } from "@/lib/calendar-groups"
import { titleHref } from "@/lib/links"

function localDay(value: string): string {
  return value.slice(0, 10)
}

export function CalendarPage() {
  const query = useQuery({ queryKey: ["calendar", "all"], queryFn: ({ signal }) => loadCalendar(signal) })
  const today = new Date()
  const todayKey = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, "0")}-${String(today.getDate()).padStart(2, "0")}`
  const groups = new Map<string, APICalendarEntry[]>()
  for (const entry of [...(query.data ?? [])].sort((a, b) => a.air_date.localeCompare(b.air_date))) {
    const key = localDay(entry.air_date)
    if (key < shiftDay(todayKey, -7)) continue
    groups.set(key, [...(groups.get(key) ?? []), entry])
  }
  return (
    <>
      <PageHeader title="Kalender" description="Afsnit fra den seneste uge og dem, der kommer. Media Manager søger automatisk, når de er sendt." />
      {query.error ? <ErrorBox error={query.error} retry={() => query.refetch()} /> : null}
      {query.isPending ? <CardGridSkeleton /> : null}
      {!query.isPending && groups.size === 0 && !query.error ? <Card><Empty title="Ingen afsnit i kalenderen" /></Card> : null}
      <div className="flex flex-col gap-9">
        {[...groups.entries()].map(([day, entries]) => (
          <section key={day} aria-label={formatWeekday(`${day}T12:00:00`)}>
            <div className="mb-3 flex items-center gap-2">
              <h2 className={day < todayKey ? "text-lg font-semibold tracking-tight text-text-2 first-letter:uppercase" : "text-lg font-semibold tracking-tight first-letter:uppercase"}>{formatWeekday(`${day}T12:00:00`)}</h2>
              {day === todayKey ? <Badge tone="info">I dag</Badge> : null}
              <span className="ml-auto text-sm text-text-3">{pluralize(entries.length, "afsnit", "afsnit")}</span>
            </div>
            <ul className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
              {groupCalendarDay(entries, (entry) => calendarBadge(entry, day > todayKey)).map((group) => <EpisodeTile key={group.key} group={group} />)}
            </ul>
          </section>
        ))}
      </div>
    </>
  )
}

/** One episode, or a show's episodes of the same day, as a picture with the state on it. */
function EpisodeTile({ group }: { group: CalendarGroup }) {
  const entry = group.first
  const wide = sizedArtwork(entry.backdrop_url, "w780")
  const tall = sizedArtwork(entry.poster_url, "w342")
  return (
    <li>
      <Link to={titleHref(entry.media_id)} className="group relative block h-36 overflow-hidden rounded-2xl bg-[#1d1d1f] shadow-card transition-shadow hover:shadow-lift">
        {wide ? <img src={wide} alt="" loading="lazy" className="absolute inset-0 size-full object-cover transition-transform duration-500 group-hover:scale-[1.03]" /> : tall ? <img src={tall} alt="" loading="lazy" className="absolute inset-0 size-full scale-110 object-cover opacity-70 blur-xl" /> : null}
        <div aria-hidden className="absolute inset-0 bg-gradient-to-t from-black/85 via-black/30 to-black/0" />
        <span className="absolute left-3 right-3 top-3 inline-flex max-w-max items-center gap-1.5 truncate rounded-full bg-black/55 px-2.5 py-1 text-[11px] font-semibold text-white backdrop-blur"><Dot tone={group.badge.tone} /><span className="truncate">{group.badge.text}</span></span>
        <div className="absolute inset-x-4 bottom-3 text-white">
          <div className="truncate text-base font-bold tracking-tight">{entry.media_title}</div>
          <div className="truncate text-xs text-white/85">{group.label}</div>
        </div>
      </Link>
    </li>
  )
}

/** States where nothing is happening yet because the episode has not been sent. */
const idleStates = new Set(["", "monitored", "waiting", "planned"])

/**
 * An episode whose day is ahead and that only waits is "Kommer". Any real
 * progress or problem keeps its own state, so nothing is hidden.
 */
export function calendarBadge(entry: Pick<APICalendarEntry, "workflow_state">, upcoming: boolean): { tone: ReturnType<typeof stateTone>; text: string } {
  if (upcoming && idleStates.has(entry.workflow_state ?? "")) return { tone: "neutral", text: "Kommer" }
  return { tone: stateTone(entry.workflow_state), text: stateLabel(entry.workflow_state) }
}

function shiftDay(day: string, delta: number): string {
  const date = new Date(`${day}T12:00:00`)
  date.setDate(date.getDate() + delta)
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`
}
