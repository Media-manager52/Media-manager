import { useQuery } from "@tanstack/react-query"
import { CheckCircle2, ChevronRight, Plus, SearchCheck } from "lucide-react"
import { Link } from "react-router"

import { loadAttentionWorkflows, loadCalendar, loadDashboard, loadRecentAdditions, loadSetupStatus, loadSystemHealth, type APIWorkflow } from "@/api/client"
import { Poster, sizedArtwork } from "@/components/poster"
import { OverviewHero } from "@/components/overview-hero"
import { Badge, Card, CardHeader, Dot, Empty, ErrorBox, PageHeader, Skeleton } from "@/components/ui"
import { formatDateTime, formatNumber, formatRelative, formatWeekday } from "@/format"
import { attentionLabel, stateLabel, stateTone, workflowDisplayState } from "@/i18n"
import { plexAltMountSummary } from "@/lib/altmount-status"
import { describeVariant, titleHref, workflowHref } from "@/lib/links"
import { greeting } from "@/lib/overview-tiles"

export function OverviewPage() {
  const dashboard = useQuery({ queryKey: ["dashboard"], queryFn: ({ signal }) => loadDashboard(signal), refetchInterval: 60_000 })
  const counts = dashboard.data?.counts
  return (
    <>
      <PageHeader
        title="Oversigt"
        eyebrow={greeting(new Date())}
        description={counts ? summary(counts.needs_attention, counts.active_workflows) : "Hvad der sker i dit bibliotek lige nu, og hvad der kræver dig."}
        actions={(
          <>
            <Link to="/bibliotek/mangler" className="inline-flex h-9 items-center gap-2 rounded-lg border border-line bg-surface px-3.5 text-sm font-medium hover:bg-surface-2"><SearchCheck aria-hidden className="size-4" />Find manglende</Link>
            <Link to="/tilfoej" className="inline-flex h-9 items-center gap-2 rounded-lg bg-text px-3.5 text-sm font-semibold text-bg hover:opacity-90"><Plus aria-hidden className="size-4" />Tilføj titel</Link>
          </>
        )}
      />
      {dashboard.error ? <ErrorBox error={dashboard.error} retry={() => dashboard.refetch()} /> : null}
      <SetupBanner />
      <OverviewHero counts={counts} />
      <RecentlyAdded />
      <div className="mt-6 grid gap-6 xl:grid-cols-[1.4fr_1fr]">
        <div className="flex min-w-0 flex-col gap-6">
          <AttentionPreview />
          <RecentWork workflows={dashboard.data?.recent_workflows} loading={dashboard.isPending} />
        </div>
        <div className="flex min-w-0 flex-col gap-6">
          <HealthCard />
          <UpcomingCard />
        </div>
      </div>
    </>
  )
}

function summary(attention: number, active: number): string {
  const waiting = attention === 0 ? "Intet venter på dig." : attention === 1 ? "1 sag venter på dig." : `${formatNumber(attention)} sager venter på dig.`
  return `${waiting} ${formatNumber(active)} er i gang med at blive fundet, hentet eller tjekket.`
}

/** Shown only while a required setup step is missing. */
function SetupBanner() {
  const query = useQuery({ queryKey: ["setup", "status"], queryFn: ({ signal }) => loadSetupStatus(signal) })
  if (!query.data || query.data.complete) return null
  const required = query.data.steps.filter((step) => !step.optional)
  return (
    <Link to="/kom-i-gang" className="mb-6 flex items-center gap-3 rounded-xl border border-info/30 bg-info-soft p-4 text-sm hover:bg-surface-2">
      <div className="min-w-0 flex-1">
        <div className="font-medium">Opsætningen er ikke færdig</div>
        <div className="text-text-2">{required.filter((step) => step.done).length} af {required.length} trin er klaret. Se, hvad der mangler.</div>
      </div>
      <ChevronRight aria-hidden className="size-4 shrink-0" />
    </Link>
  )
}

/** The titles that most recently got a verified file in the library. */
function RecentlyAdded() {
  const query = useQuery({ queryKey: ["media", "recent"], queryFn: ({ signal }) => loadRecentAdditions(signal), refetchInterval: 60_000 })
  const items = query.data ?? []
  return (
    <Card className="mt-6">
      <CardHeader title="Senest tilføjet" description="De nyeste film og afsnit, der er hentet, kontrolleret og lagt i biblioteket." action={<Link to="/bibliotek?sort=senest-tilfoejet" className="text-sm font-medium text-accent-strong hover:underline">Se alle</Link>} />
      {query.error ? <div className="p-4"><ErrorBox error={query.error} retry={() => query.refetch()} /></div> : null}
      {query.isPending ? <div className="flex gap-3 overflow-hidden px-5 py-4">{Array.from({ length: 6 }, (_, index) => <Skeleton key={index} className="aspect-[2/3] w-28 shrink-0 rounded-xl" />)}</div> : null}
      {!query.isPending && !query.error && items.length === 0 ? <Empty title="Intet er hentet endnu">Når en film eller et afsnit er lagt i biblioteket, vises det her.</Empty> : null}
      {items.length > 0 ? (
        <ul aria-label="Senest tilføjet" className="flex gap-3.5 overflow-x-auto px-5 py-4">
          {items.map((item) => {
            const wide = sizedArtwork(item.backdrop_url, "w780")
            return (
              <li key={item.media_id} className={wide ? "w-60 shrink-0" : "w-28 shrink-0"}>
                <Link to={titleHref(item.media_id)} className="group block">
                  {wide
                    ? <img src={wide} alt="" loading="lazy" className="aspect-video w-full rounded-xl bg-surface-3 object-cover shadow-card" />
                    : <Poster src={sizedArtwork(item.poster_url, "w185")} title={item.title} kind={item.media_kind} />}
                  <div className="mt-2 truncate text-sm font-semibold group-hover:underline">{item.title}</div>
                  <div className="truncate text-xs text-text-2">{describeVariant(item)} · {formatRelative(item.added_at)}</div>
                </Link>
              </li>
            )
          })}
        </ul>
      ) : null}
    </Card>
  )
}

function AttentionPreview() {
  const query = useQuery({ queryKey: ["workflows", "attention"], queryFn: ({ signal }) => loadAttentionWorkflows(signal), refetchInterval: 60_000 })
  const items = query.data ?? []
  return (
    <Card>
      <CardHeader
        title="Kræver handling"
        description="Arbejde der er gået i stå og venter på en beslutning fra dig."
        action={items.length > 0 ? <Link to="/handling" className="text-sm font-medium text-accent-strong hover:underline">Se alle</Link> : null}
      />
      {query.error ? <div className="p-4"><ErrorBox error={query.error} retry={() => query.refetch()} /></div> : null}
      {query.isPending ? <ListSkeleton /> : null}
      {!query.isPending && items.length === 0 && !query.error ? (
        <Empty title="Alt kører af sig selv" icon={<CheckCircle2 className="size-8 text-good" />}>Intet venter på dig lige nu.</Empty>
      ) : null}
      <ul className="divide-y divide-line">
        {items.slice(0, 6).map((workflow) => (
          <li key={workflow.id}>
            <Link to={workflowHref(workflow)} className="flex items-center gap-3 px-5 py-3 hover:bg-surface-2">
              <Dot tone="warn" />
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium">{workflow.title}{workflow.year ? <span className="font-normal text-text-3"> ({workflow.year})</span> : null}</div>
                <div className="truncate text-sm text-text-2">{describeVariant(workflow)} · {attentionLabel(workflow.attention_code) || stateLabel(workflow.state)}</div>
              </div>
              <ChevronRight aria-hidden className="size-4 shrink-0 text-text-3" />
            </Link>
          </li>
        ))}
      </ul>
    </Card>
  )
}

function RecentWork({ workflows, loading }: { workflows?: APIWorkflow[]; loading: boolean }) {
  return (
    <Card>
      <CardHeader title="Senest ændret" description="De nyeste skridt i dit arbejde." action={<Link to="/arbejde" className="text-sm font-medium text-accent-strong hover:underline">Alt arbejde</Link>} />
      {loading ? <ListSkeleton /> : null}
      {!loading && workflows && workflows.length === 0 ? <Empty title="Intet arbejde endnu">Tilføj en film eller serie, så begynder Media Manager.</Empty> : null}
      <ul className="divide-y divide-line">
        {(workflows ?? []).slice(0, 8).map((workflow) => (
          <li key={workflow.id}>
            <Link to={workflowHref(workflow)} className="flex items-center gap-3 px-5 py-3 hover:bg-surface-2">
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium">{workflow.title}</div>
                <div className="truncate text-sm text-text-2">{describeVariant(workflow)} · {formatRelative(workflow.updated_at)}</div>
              </div>
              <Badge tone={stateTone(workflowDisplayState(workflow))}>{stateLabel(workflowDisplayState(workflow))}</Badge>
            </Link>
          </li>
        ))}
      </ul>
    </Card>
  )
}

function HealthCard() {
  const query = useQuery({ queryKey: ["health"], queryFn: ({ signal }) => loadSystemHealth(signal), refetchInterval: 60_000 })
  const health = query.data
  const failing = (health?.components ?? []).filter((component) => component.enabled && component.consecutive_failure > 0)
  const mountsDown = (health?.mounts ?? []).filter((mount) => mount.state !== "available")
  const databaseOk = health ? health.database_state === "ready" && health.schema_version === health.expected_schema : undefined
  const plexView = plexAltMountSummary(health?.plex_altmount)
  return (
    <Card>
      <CardHeader title="Systemstatus" action={<Link to="/system" className="text-sm font-medium text-accent-strong hover:underline">Detaljer</Link>} />
      <div className="space-y-3 px-5 py-4 text-sm">
        {query.error ? <ErrorBox error={query.error} /> : null}
        {!health && !query.error ? <Skeleton className="h-20" /> : null}
        {health ? (
          <>
            <HealthRow ok={databaseOk ?? false} label="Database" detail={databaseOk ? `Klar · ${health.database_latency_ms} ms` : `Skema ${health.schema_version} af ${health.expected_schema}`} />
            <HealthRow ok={mountsDown.length === 0} label="AltMount-drev" detail={mountsDown.length === 0 ? `${health.mounts?.length ?? 0} ${(health.mounts?.length ?? 0) === 1 ? "tilgængeligt" : "tilgængelige"}` : mountsDown.map((mount) => mount.name).join(", ") + (mountsDown.length === 1 ? " er utilgængeligt" : " er utilgængelige")} />
            {plexView ? <HealthRow ok={plexView.ok} label="Plex ser AltMount" detail={plexView.detail} /> : null}
            <HealthRow ok={failing.length === 0} label="Forbindelser" detail={failing.length === 0 ? "Ingen fejl" : failing.map((component) => component.name).join(", ")} />
            <HealthRow ok={health.outbox.retrying === 0} label="Baggrundskø" detail={`${formatNumber(health.outbox.due)} klar · ${formatNumber(health.outbox.retrying)} prøver igen`} />
            <div className="pt-1 text-xs text-text-3">Tjekket {formatDateTime(health.checked_at)}</div>
          </>
        ) : null}
      </div>
    </Card>
  )
}

function HealthRow({ ok, label, detail }: { ok: boolean; label: string; detail: string }) {
  return (
    <div className="flex items-center gap-3">
      <Dot tone={ok ? "good" : "warn"} />
      <span className="w-32 shrink-0 font-medium">{label}</span>
      <span className="min-w-0 flex-1 truncate text-text-2">{detail}</span>
    </div>
  )
}

function UpcomingCard() {
  const query = useQuery({ queryKey: ["calendar", "week"], queryFn: ({ signal }) => loadCalendar(signal) })
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const week = new Date(today.getTime() + 7 * 86400_000)
  const upcoming = (query.data ?? []).filter((entry) => {
    const date = new Date(entry.air_date)
    return date >= today && date < week
  })
  return (
    <Card>
      <CardHeader title="Den næste uge" description="Nye afsnit, som Media Manager holder øje med." action={<Link to="/kalender" className="text-sm font-medium text-accent-strong hover:underline">Kalender</Link>} />
      {query.isPending ? <ListSkeleton /> : null}
      {query.error ? <div className="p-4"><ErrorBox error={query.error} retry={() => query.refetch()} /></div> : null}
      {!query.isPending && !query.error && upcoming.length === 0 ? <Empty title="Ingen nye afsnit i den kommende uge" /> : null}
      <ul className="divide-y divide-line">
        {upcoming.slice(0, 8).map((entry) => (
          <li key={`${entry.workflow_id}-${entry.season}-${entry.episode}`} className="flex items-center gap-3 px-5 py-3">
            <Poster compact src={sizedArtwork(entry.poster_url, "w185")} title={entry.media_title} kind={entry.media_kind} className="w-9 shrink-0 rounded-md" />
            <div className="min-w-0 flex-1">
              <div className="truncate font-medium">{entry.media_title}</div>
              <div className="truncate text-sm text-text-2">S{String(entry.season).padStart(2, "0")}E{String(entry.episode).padStart(2, "0")}{entry.episode_title ? ` · ${entry.episode_title}` : ""}</div>
            </div>
            <span className="shrink-0 text-sm text-text-2">{formatWeekday(entry.air_date)}</span>
          </li>
        ))}
      </ul>
    </Card>
  )
}

export function ListSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div className="space-y-3 px-5 py-4">
      {Array.from({ length: rows }, (_, index) => <Skeleton key={index} className="h-10" />)}
    </div>
  )
}
