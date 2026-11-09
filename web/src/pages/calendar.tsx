import { useQuery } from "@tanstack/react-query"
import { Link } from "react-router"

import { loadCalendar, type APICalendarEntry } from "@/api/client"
import { Poster, sizedArtwork } from "@/components/poster"
import { Badge, Card, Empty, ErrorBox, PageHeader } from "@/components/ui"
import { formatWeekday, pluralize } from "@/format"
import { stateLabel, stateTone } from "@/i18n"
import { titleHref } from "@/lib/links"
import { ListSkeleton } from "@/pages/overview"

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
      {query.isPending ? <Card><ListSkeleton rows={6} /></Card> : null}
      {!query.isPending && groups.size === 0 && !query.error ? <Card><Empty title="Ingen afsnit i kalenderen" /></Card> : null}
      <div className="flex flex-col gap-4">
        {[...groups.entries()].map(([day, entries]) => (
          <Card key={day} className={day === todayKey ? "border-accent" : undefined}>
            <div className="flex items-center gap-2 border-b border-line px-5 py-3">
              <h2 className="font-semibold first-letter:uppercase">{formatWeekday(`${day}T12:00:00`)}</h2>
              {day === todayKey ? <Badge tone="info">I dag</Badge> : null}
              <span className="ml-auto text-sm text-text-3">{pluralize(entries.length, "afsnit", "afsnit")}</span>
            </div>
            <ul className="divide-y divide-line">
              {entries.map((entry) => (
                <li key={`${entry.workflow_id}-${entry.season}-${entry.episode}`}>
                  <Link to={titleHref(entry.media_id)} className="flex items-center gap-3 px-5 py-3 hover:bg-surface-2">
                    <Poster compact src={sizedArtwork(entry.poster_url, "w185")} title={entry.media_title} kind={entry.media_kind} className="w-10 shrink-0 rounded-md" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-medium">{entry.media_title}</div>
                      <div className="truncate text-sm text-text-2">S{String(entry.season).padStart(2, "0")}E{String(entry.episode).padStart(2, "0")}{entry.episode_title ? ` · ${entry.episode_title}` : ""}</div>
                    </div>
                    <EpisodeBadge entry={entry} upcoming={day > todayKey} />
                  </Link>
                </li>
              ))}
            </ul>
          </Card>
        ))}
      </div>
    </>
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

function EpisodeBadge({ entry, upcoming }: { entry: APICalendarEntry; upcoming: boolean }) {
  const badge = calendarBadge(entry, upcoming)
  return <Badge tone={badge.tone}>{badge.text}</Badge>
}

function shiftDay(day: string, delta: number): string {
  const date = new Date(`${day}T12:00:00`)
  date.setDate(date.getDate() + delta)
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`
}
