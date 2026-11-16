import type { APIQueueSnapshot } from "@/api/client"
import { formatDateTime, formatNumber } from "@/format"
import { queueEventLabel } from "@/i18n"

function duration(milliseconds: number): string {
  if (milliseconds < 1000) return `${formatNumber(milliseconds)} ms`
  if (milliseconds < 60_000) return `${formatNumber(Math.round(milliseconds / 1000))} sek.`
  if (milliseconds < 3_600_000) return `${formatNumber(Math.round(milliseconds / 60_000))} min.`
  return `${formatNumber(Math.round(milliseconds / 360_000) / 10)} timer`
}

export function QueueTimings({ queue }: { queue: APIQueueSnapshot }) {
  if (!queue.timing_started_at) return null
  return (
    <div className="border-t border-line px-5 py-4">
      <h2 className="font-semibold">Hvor går tiden hen?</h2>
      <p className="mt-1 text-xs text-text-3">Afsluttede opgaver fra det seneste døgn, oprettet siden {formatDateTime(queue.timing_started_at)}. Hvert felt viser median / p90: halvdelen / ni ud af ti opgaver var hurtigere.</p>
      <p className="mt-2 text-xs text-text-3">Opgaver, der stadig venter eller er i gang, står i køoversigten ovenfor. {formatNumber(queue.legacy_pending ?? 0)} af dem er oprettet før målingens start og indgår ikke i tiderne.</p>
      {queue.timings_last_24h?.length ? (
        <div className="mt-3 overflow-x-auto" tabIndex={0} role="region" aria-label="Tidsmålinger af afsluttede opgaver">
          <table className="w-full min-w-[48rem] text-left text-xs">
            <thead><tr className="border-b border-line text-text-3">{["Opgave", "Antal", "Planlagt ventetid", "Køtid", "Behandling", "Pacing og genforsøg"].map((label) => <th key={label} className="px-2 py-2 font-medium">{label}</th>)}</tr></thead>
            <tbody className="divide-y divide-line">{queue.timings_last_24h.map((row) => (
              <tr key={row.event_type}>
                <td className="px-2 py-2">{queueEventLabel(row.event_type)}</td><td className="px-2 py-2 tabular">{formatNumber(row.events)}</td>
                {[[row.planned_p50_ms, row.planned_p90_ms], [row.ready_wait_p50_ms, row.ready_wait_p90_ms], [row.active_p50_ms, row.active_p90_ms], [row.retry_p50_ms, row.retry_p90_ms]].map(([median, p90], i) => <td key={i} className="whitespace-nowrap px-2 py-2 tabular">{duration(median)} / {duration(p90)}</td>)}
              </tr>
            ))}</tbody>
          </table>
        </div>
      ) : <p className="mt-3 text-sm text-text-2">Ingen afsluttede opgaver er målt endnu.</p>}
    </div>
  )
}
