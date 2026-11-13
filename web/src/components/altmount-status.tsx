import { useQuery } from "@tanstack/react-query"
import { Link } from "react-router"

import { loadAltMountStatus, type APIAltMountServerStatus, type APIAltMountStream } from "@/api/client"
import { AltMountSwitch } from "@/components/altmount-switch"
import { Badge, Card, CardHeader, Dot, Empty, ErrorBox, Progress, Stat } from "@/components/ui"
import { formatBytes, formatDateTime, formatRelative } from "@/format"
import { altMountBuildText } from "@/lib/altmount-switch"
import { quotaPercent, serverState, streamProgress, streamStatusLabel } from "@/lib/altmount-status"
import { scopeLabel, titleHref } from "@/lib/links"
import { ListSkeleton } from "@/pages/overview"

const unavailableLabels: Record<string, string> = { drive: "drevet", servers: "serverne", streams: "afspilningerne" }

/** AltMount's live status, so its own website is not needed: drive, servers with quota, and what is played now. */
export function AltMountStatusSection() {
  const query = useQuery({ queryKey: ["system", "altmount"], queryFn: ({ signal }) => loadAltMountStatus(signal), refetchInterval: 10_000 })
  if (query.error) return <ErrorBox error={query.error} retry={() => query.refetch()} />
  if (!query.data) return <Card><ListSkeleton /></Card>
  const status = query.data
  const drive = status.drive
  const active = status.servers.filter((server) => server.enabled)
  return (
    <div className="flex flex-col gap-6">
      {status.unavailable.length > 0 ? (
        <div role="status" className="rounded-xl border border-warn/40 bg-warn/10 px-4 py-3 text-sm">
          AltMount svarede ikke om {status.unavailable.map((part) => unavailableLabels[part] ?? part).join(" og ")}. Resten er vist.
        </div>
      ) : null}
      <p className="text-sm text-text-2">Udgave: {altMountBuildText(status)}</p>
      {status.can_switch ? <AltMountSwitch version={status.version} /> : null}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Stat label="Drev" tone={!drive ? "neutral" : drive.healthy ? "good" : "bad"} value={!drive ? "Ukendt" : drive.healthy ? "Kører" : "Fejler"} hint={drive?.path} />
        <Stat label="Hentet det seneste døgn" value={formatBytes(status.bytes_downloaded_24h)} hint={`${formatBytes(status.download_speed_bytes_per_sec)}/s lige nu`} />
        <Stat label="Usenet-servere slået til" value={active.length} hint={`${status.streams.length} ${status.streams.length === 1 ? "fil afspilles" : "filer afspilles"} nu`} />
      </div>
      <Card>
        <CardHeader title="Usenet-servere" description="I den rækkefølge AltMount bruger dem. Login og rækkefølge ændres under Indstillinger → Usenet-servere." action={<Link to="/indstillinger/usenet-servere" className="text-sm font-medium text-accent-strong hover:underline">Ret servere</Link>} />
        {status.servers.length === 0 ? (
          <Empty title="Ingen Usenet-servere">Tilføj dit login under Indstillinger → Usenet-servere.</Empty>
        ) : (
          <ul className="divide-y divide-line">
            {status.servers.map((server) => <ServerRow key={server.id} server={server} />)}
          </ul>
        )}
      </Card>
      <Card>
        <CardHeader title="Afspilles nu" description={`Filer, der læses fra AltMount lige nu. Opdateret ${formatDateTime(status.checked_at)}.`} />
        {status.streams.length === 0 ? (
          <Empty title="Intet afspilles lige nu" />
        ) : (
          <ul className="divide-y divide-line">
            {status.streams.map((stream, index) => <StreamRow key={`${stream.file}-${index}`} stream={stream} />)}
          </ul>
        )}
      </Card>
    </div>
  )
}

function ServerRow({ server }: { server: APIAltMountServerStatus }) {
  const state = serverState(server)
  const live = server.live
  const quota = quotaPercent(server)
  return (
    <li className="flex flex-col gap-2 px-5 py-3 text-sm">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <Dot tone={state.tone} />
        <span className="min-w-0 flex-1 basis-40 font-medium [overflow-wrap:anywhere]">{server.name || server.host}</span>
        {server.is_backup_provider ? <Badge>Reserve</Badge> : null}
        <span className="text-text-2">{state.label}</span>
      </div>
      {live ? (
        <div className="flex flex-wrap gap-x-4 gap-y-1 pl-5 text-xs text-text-3">
          {server.name ? <span className="[overflow-wrap:anywhere]">{server.host}</span> : null}
          <span>{live.used_connections} af {live.max_connections} forbindelser</span>
          <span>{formatBytes(live.byte_count_24h)} det seneste døgn</span>
          {live.ping_ms > 0 ? <span>{live.ping_ms} ms</span> : null}
          {live.failure_reason ? <span className="text-bad">{live.failure_reason}</span> : null}
        </div>
      ) : null}
      {quota !== undefined && live ? (
        <div className="flex flex-col gap-1 pl-5">
          <Progress value={quota} max={100} tone={live.quota_exceeded || quota >= 90 ? "bad" : quota >= 75 ? "warn" : "info"} label={`Kvote for ${server.name || server.host}`} />
          <span className="text-xs text-text-3">
            Kvote: {formatBytes(live.quota_used)} af {formatBytes(live.quota_bytes)} brugt ({quota} %){live.quota_reset_at ? ` · nulstilles ${formatDateTime(live.quota_reset_at)}` : ""}
          </span>
        </div>
      ) : null}
    </li>
  )
}

function StreamRow({ stream }: { stream: APIAltMountStream }) {
  const progress = streamProgress(stream)
  const scope = stream.title ? scopeLabel(stream.title.variant) : ""
  return (
    <li className="flex flex-col gap-2 px-5 py-3 text-sm">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="min-w-0 flex-1 basis-40 font-medium [overflow-wrap:anywhere]">
          {stream.title ? (
            <Link to={titleHref(stream.title.media_id)} className="hover:underline">
              {stream.title.title}{stream.title.year ? ` (${stream.title.year})` : ""}{scope ? <span className="font-normal text-text-3"> · {scope}</span> : null}
            </Link>
          ) : stream.file}
        </span>
        <span className="text-text-2">{streamStatusLabel(stream.status)}</span>
      </div>
      <Progress value={progress} max={100} label={`Afspillet af ${stream.title?.title ?? stream.file}`} />
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-text-3">
        <span>{progress} % af {formatBytes(stream.total_size)}</span>
        <span>{formatBytes(stream.bytes_per_second)}/s</span>
        <span>Startet {formatRelative(stream.started_at)}</span>
        {stream.title ? <span className="[overflow-wrap:anywhere]">{stream.file}</span> : null}
      </div>
    </li>
  )
}
