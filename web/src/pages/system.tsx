import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useMemo, useState, type ReactNode } from "react"
import { HardDriveDownload, Inbox, Play, Radar } from "lucide-react"
import { Link, useParams } from "react-router"

import { createBackup, loadActivity, loadBackups, loadQueue, loadRecycle, loadRepairs, loadSupportBundle, loadSystemHealth, loadSystemInfo, restoreRecycle, type APIRecycleEntry } from "@/api/client"
import { AltMountStatusSection } from "@/components/altmount-status"
import { chipColor, IconChip, initials } from "@/components/icon-chip"
import { PlexAccessFix } from "@/components/plex-access"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, CardHeader, cx, Dot, Empty, ErrorBox, KeyValue, PageHeader, PillNav, ShowMore } from "@/components/ui"
import { formatBytes, formatDateTime, formatNumber, formatRelative, pluralize } from "@/format"
import { activityActorLabel, activityLabel, backupStateLabel, componentKindLabel, componentOutcomeLabel, errorMessage, externalCopyLabel, humanize, queueEventLabel, repairReasonLabel, translateDetail } from "@/i18n"
import { plexAltMountGuide, plexAltMountLine } from "@/lib/altmount-status"
import { confirmedExternalCopy, externalCopyState, latestExternalCopy, latestRestoreTest } from "@/lib/backup-evidence"
import { scopeLabel, titleHref, workflowHref } from "@/lib/links"
import { useShowMore } from "@/lib/show-more"
import type { Tone } from "@/lib/tone"
import { ListSkeleton } from "@/pages/overview"

const sections = [
  { id: "", label: "Status" },
  { id: "altmount", label: "AltMount" },
  { id: "koe", label: "Baggrundskø" },
  { id: "backup", label: "Backup" },
  { id: "reparationer", label: "Reparationer" },
  { id: "papirkurv", label: "Papirkurv" },
  { id: "aktivitet", label: "Aktivitet" },
]

/** The coloured icon each kind of connection gets on the status page. */
const kindChip: Record<string, { color: string; icon: ReactNode }> = {
  altmount: { color: "#0d9488", icon: <HardDriveDownload className="size-4" /> },
  newznab: { color: "#2563eb", icon: <Radar className="size-4" /> },
  oldboys: { color: "#2563eb", icon: <Radar className="size-4" /> },
  plex: { color: "#d97706", icon: <Play className="size-4" /> },
  seerr: { color: "#7c3aed", icon: <Inbox className="size-4" /> },
}

export function SystemPage() {
  const { section = "" } = useParams()
  return (
    <>
      <PageHeader title="System" description="Hvordan Media Manager og forbindelserne har det." />
      <PillNav label="System" items={sections.map((entry) => ({ to: entry.id ? `/system/${entry.id}` : "/system", label: entry.label, end: true }))} />
      {section === "" ? <Status /> : null}
      {section === "altmount" ? <AltMountStatusSection /> : null}
      {section === "koe" ? <Queue /> : null}
      {section === "backup" ? <Backups /> : null}
      {section === "reparationer" ? <Repairs /> : null}
      {section === "papirkurv" ? <RecycleBin /> : null}
      {section === "aktivitet" ? <ActivityLog /> : null}
    </>
  )
}

function Status() {
  const health = useQuery({ queryKey: ["health"], queryFn: ({ signal }) => loadSystemHealth(signal), refetchInterval: 30_000 })
  const info = useQuery({ queryKey: ["system", "info"], queryFn: ({ signal }) => loadSystemInfo(signal) })
  if (health.error) return <ErrorBox error={health.error} retry={() => health.refetch()} />
  if (!health.data) return <Card><ListSkeleton /></Card>
  const h = health.data
  return (
    <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
      <Card>
        <CardHeader title="Forbindelser" description={`Tjekket ${formatDateTime(h.checked_at)}`} />
        <ul className="divide-y divide-line">
          {[...h.components].sort((a, b) => a.kind.localeCompare(b.kind) || a.name.localeCompare(b.name, "da")).map((component) => {
            const tone = !component.enabled ? "neutral" : component.consecutive_failure > 0 ? "bad" : component.configuration_state === "awaiting_commission" ? "warn" : "good"
            return (
              <li key={component.id} className="flex items-center gap-3 px-5 py-3 text-sm">
                <IconChip color={kindChip[component.kind]?.color ?? chipColor(component.name)} className="size-8 rounded-lg">{kindChip[component.kind]?.icon ?? initials(component.name)}</IconChip>
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{component.name.replace(/\s*\{DK\}\s*/i, "")}</span>
                  <span className="block text-xs text-text-3">{componentKindLabel(component.kind)}</span>
                </span>
                <Dot tone={tone} />
                <span className="shrink-0 text-right text-text-2">
                  {!component.enabled ? "Slået fra" : component.consecutive_failure > 0 ? `${component.consecutive_failure} fejl i træk` : component.last_outcome ? componentOutcomeLabel(component.last_outcome) : "OK"}
                </span>
              </li>
            )
          })}
        </ul>
      </Card>
      <div className="flex flex-col gap-6">
        <Card>
          <CardHeader title="Drev og database" />
          <ul className="divide-y divide-line text-sm">
            <li className="flex items-center gap-3 px-5 py-3">
              <Dot tone={h.database_state === "ready" && h.schema_version === h.expected_schema ? "good" : "bad"} />
              <span className="flex-1 font-medium">Database</span>
              <span className="text-text-2">Skema {h.schema_version}/{h.expected_schema} · {h.database_latency_ms} ms</span>
            </li>
            {(h.mounts ?? []).map((mount) => (
              <li key={mount.integration_id} className="flex items-center gap-3 px-5 py-3">
                <Dot tone={mount.state === "available" ? "good" : "bad"} />
                <span className="flex-1 font-medium">{mount.name}</span>
                <span className="break-all text-text-2">{mount.state === "available" ? mount.path : mount.detail || "Utilgængeligt"}</span>
              </li>
            ))}
            {(h.plex_altmount ?? []).map((view) => {
              const line = plexAltMountLine(view)
              return (
                <li key={`${view.plex_id}-${view.path}`} className="flex flex-col gap-1 px-5 py-3">
                  <div className="flex items-center gap-3">
                    <Dot tone={line.tone} />
                    <span className="shrink-0 font-medium">Plex ser AltMount</span>
                    <span className="min-w-0 flex-1 text-right text-text-2 [overflow-wrap:anywhere]">{line.label}</span>
                  </div>
                  {line.detail ? (
                    <p className="pl-5 text-xs text-text-3">
                      {line.detail}{line.tone === "bad" ? <> <a href={plexAltMountGuide} target="_blank" rel="noreferrer" className="font-medium text-accent-strong hover:underline">Se vejledningen</a></> : null}
                    </p>
                  ) : null}
                </li>
              )
            })}
          </ul>
          {(h.plex_altmount ?? []).some((view) => view.state === "missing" || view.state === "unreadable") ? <div className="px-5 pb-5"><PlexAccessFix /></div> : null}
        </Card>
        {info.data ? (
          <Card>
            <CardHeader title="Version" />
            <div className="px-5 py-4">
              <KeyValue items={[
                ["Version", info.data.version],
                ["Startet", formatDateTime(info.data.started_at)],
                ["Login", info.data.browser_auth === "gateway_basic_auth" ? "Via login foran Media Manager (fx Cosmos)" : info.data.browser_auth],
                ["Papirkurv", `${info.data.recycle_retention_days} dage`],
              ]} />
            </div>
          </Card>
        ) : null}
      </div>
      <SupportFile />
    </div>
  )
}

function SupportFile() {
  const toast = useToast()
  const download = useMutation({
    mutationFn: loadSupportBundle,
    onSuccess: (text) => {
      const link = document.createElement("a")
      link.href = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }))
      link.download = "media-manager-support.txt"
      link.click()
      URL.revokeObjectURL(link.href)
    },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  return (
    <Card className="xl:col-span-2">
      <CardHeader title="Supportfil" description="En tekstfil med problemer, version og forbindelsers tilstand, til når du beder om hjælp. Nøgler og adgangskoder fjernes automatisk, men læs filen igennem, før du deler den." action={<Button busy={download.isPending} onClick={() => download.mutate()}>Hent supportfil</Button>} />
    </Card>
  )
}

function Queue() {
  const query = useQuery({ queryKey: ["system", "queue"], queryFn: ({ signal }) => loadQueue(signal), refetchInterval: 15_000 })
  if (query.error) return <ErrorBox error={query.error} />
  if (!query.data) return <Card><ListSkeleton /></Card>
  const q = query.data
  return (
    <Card>
      <CardHeader title="Baggrundskø" description={`${pluralize(q.completed_last_hour, "opgave", "opgaver")} løst den seneste time · ${formatNumber(q.due)} klar · ${formatNumber(q.scheduled)} planlagt · ${formatNumber(q.retrying)} prøver igen`} />
      <div className="overflow-x-auto" tabIndex={0} role="region" aria-label="Køen pr. opgavetype">
        <table className="w-full text-sm sm:min-w-[36rem]">
          <thead className="text-left text-text-3">
            <tr className="border-b border-line">
              <th className="py-2 pl-5 pr-2 font-medium sm:px-5">Opgave</th>
              <th className="px-2 py-2 text-right align-bottom font-medium sm:px-3">Klar</th>
              <th className="px-2 py-2 text-right align-bottom font-medium sm:px-3">I gang</th>
              <th className="px-2 py-2 text-right align-bottom font-medium sm:px-3">Prøver igen</th>
              <th className="px-2 py-2 text-right align-bottom font-medium sm:px-3">Planlagt</th>
              <th className="hidden px-5 py-2 font-medium sm:table-cell">Ældste klar</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {q.by_type.map((row) => (
              <tr key={row.event_type}>
                <td className="py-2 pl-5 pr-2 sm:px-5" title={row.event_type}>{queueEventLabel(row.event_type)}</td>
                <td className="tabular px-2 py-2 text-right sm:px-3">{formatNumber(row.due)}</td>
                <td className="tabular px-2 py-2 text-right sm:px-3">{formatNumber(row.leased)}</td>
                <td className={cx("tabular px-2 py-2 text-right sm:px-3", row.retrying > 0 && "text-warn")}>{formatNumber(row.retrying)}</td>
                <td className="tabular px-2 py-2 text-right sm:px-3">{formatNumber(row.scheduled)}</td>
                <td className="hidden px-5 py-2 text-text-2 sm:table-cell">{row.oldest_due_at ? formatRelative(row.oldest_due_at) : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  )
}

function BackupFact({ tone, label, children }: { tone: Tone; label: string; children: ReactNode }) {
  return (
    <div className="rounded-xl border border-line px-4 py-3">
      <div className="flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-text-3"><Dot tone={tone} />{label}</div>
      <div className="mt-1 text-sm text-text">{children}</div>
    </div>
  )
}

function Backups() {
  const [showDeleted, setShowDeleted] = useState(false)
  const queryClient = useQueryClient()
  const toast = useToast()
  const query = useQuery({ queryKey: ["backups"], queryFn: ({ signal }) => loadBackups(signal), refetchInterval: 30_000 })
  const create = useMutation({
    mutationFn: createBackup,
    onSuccess: async () => { toast("good", "Backup er sat i kø. Den bliver kontrolleret med en prøve-gendannelse."); await queryClient.invalidateQueries({ queryKey: ["backups"] }) },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  if (query.error) return <ErrorBox error={query.error} />
  if (!query.data) return <Card><ListSkeleton /></Card>
  const b = query.data
  const copiedAt = latestExternalCopy(b.records)
  const restoredAt = latestRestoreTest(b.records)
  const records = b.records.slice(0, 30)
  const kept = records.filter((record) => !record.local_file_deleted || confirmedExternalCopy(record))
  const latestKept = kept[0]
  // Deleted backups say little; they fold away while kept ones exist.
  const visibleRecords = showDeleted || kept.length === 0 ? records : kept
  const hiddenDeleted = records.length - visibleRecords.length
  return (
    <Card>
      <CardHeader
        title="Backup"
        description="Krypterede databasebackups med prøvegendannelse i en isoleret database."
        action={<Button variant="primary" busy={create.isPending} disabled={!b.configured} onClick={() => create.mutate()}>Tag backup nu</Button>}
      />
      {!b.configured ? <p className="px-5 pb-3 text-sm text-warn">Backups er slået fra, fordi serveren ikke har en backup-nøgle (BACKUP_ENCRYPTION_KEY).</p> : null}
      <div className="grid gap-3 px-5 py-4 sm:grid-cols-3">
        <BackupFact tone={latestKept ? (latestKept.state === "verified" ? "good" : latestKept.state === "failed" ? "bad" : "info") : "warn"} label="Seneste backup">
          {latestKept ? `${formatDateTime(latestKept.completed_at ?? latestKept.created_at)} · ${formatBytes(latestKept.size_bytes)}` : "Ingen backup gemt på serveren"}
        </BackupFact>
        <BackupFact tone={restoredAt ? "good" : "warn"} label="Prøvegendannelse">
          {restoredAt ? `Seneste prøvegendannelse af databasen: ${formatDateTime(restoredAt)}.` : "Ingen prøvegendannelse af databasen er gennemført endnu."}
        </BackupFact>
        <BackupFact tone={copiedAt ? "good" : b.offsite.configured ? "info" : "neutral"} label="Ekstern kopi">
          <span className="block">{b.offsite.configured ? "Ekstern kopiering er sat op." : "Ekstern kopiering er ikke sat op."}</span>
          <span className="block">{copiedAt ? `Senest bekræftede eksterne kopi: ${formatDateTime(copiedAt)}.` : "Ingen gennemført ekstern kopi er dokumenteret i den viste backupliste."}</span>
        </BackupFact>
      </div>
      <div className="mx-5 mb-4 space-y-1 rounded-xl bg-surface-2 px-4 py-3 text-xs text-text-2">
        <p>Prøvegendannelsen kontrollerer kun databasen. En samlet gendannelse af installation, nøgler, mapper og medier er ikke dokumenteret her.</p>
        <p>Gem backup-nøglen og nøglen til gemte forbindelser sikkert uden for serveren. Backup-nøglen åbner backupfilen; forbindelsernes nøgle åbner de gemte adgangsoplysninger.</p>
      </div>
      {b.configured && b.key_file ? <p className="px-5 pb-3 text-sm text-text-2">Backup-nøglen blev lavet automatisk og ligger i <code className="break-all">{b.key_file}</code> i API-containeren. Gem en kopi et sikkert sted: uden den kan en backup ikke gendannes på en anden server.</p> : null}
      {b.records.length === 0 ? <Empty title="Ingen backups endnu" /> : null}
      <ul className="divide-y divide-line border-t border-line">
        {visibleRecords.map((record) => (
          <li key={record.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
            <Badge tone={record.local_file_deleted ? "neutral" : record.state === "verified" ? "good" : record.state === "failed" ? "bad" : "info"}>{backupStateLabel(record.state, record.local_file_deleted)}</Badge>
            <span className="flex-1">{formatDateTime(record.completed_at ?? record.created_at)}</span>
            <span className="tabular text-text-3">{formatBytes(record.size_bytes)}</span>
            {(!record.local_file_deleted || confirmedExternalCopy(record)) && externalCopyState(record, b.offsite.configured) !== "not_configured" ? <span className="w-full text-text-2">{externalCopyLabel(externalCopyState(record, b.offsite.configured))}</span> : null}
            {record.last_error ? <span className="w-full break-words text-bad">{record.last_error}</span> : null}
          </li>
        ))}
      </ul>
      {hiddenDeleted > 0 ? (
        <div className="border-t border-line px-5 py-3">
          <button type="button" className="text-sm font-medium text-accent-strong hover:underline" onClick={() => setShowDeleted(true)}>Vis {formatNumber(hiddenDeleted)} ældre backups, hvor filen er slettet</button>
        </div>
      ) : null}
    </Card>
  )
}

const repairState: Record<string, string> = { open: "Åben", searching: "Søger", resolved: "Repareret", repaired: "Repareret", needs_attention: "Kræver handling", not_found: "Intet fundet", skipped: "Sprunget over", cancelled: "Annulleret" }
const repairDone = new Set(["resolved", "repaired", "not_found", "skipped", "cancelled"])

function repairTone(state: string): Tone {
  if (state === "resolved" || state === "repaired") return "good"
  if (state === "not_found" || state === "needs_attention") return "warn"
  return state === "skipped" || state === "cancelled" ? "neutral" : "info"
}

function Repairs() {
  const query = useQuery({ queryKey: ["repairs"], queryFn: ({ signal }) => loadRepairs(signal), refetchInterval: 60_000 })
  // Cases still being worked on first; the API already orders each part newest first.
  const repairs = useMemo(() => [...(query.data ?? [])].sort((a, b) => Number(repairDone.has(a.state)) - Number(repairDone.has(b.state))), [query.data])
  const { shown, remaining, showMore } = useShowMore(repairs, 50)
  if (query.error) return <ErrorBox error={query.error} />
  if (!query.data) return <Card><ListSkeleton /></Card>
  return (
    <Card>
      <CardHeader title="Reparationer" description="Filer, som AltMount har meldt ødelagte eller har fjernet, eller som mangler dansk tale. Media Manager finder selv en erstatning." />
      {query.data.length === 0 ? <Empty title="Ingen reparationer" /> : null}
      <ul className="divide-y divide-line">
        {shown.map((repair) => (
          <li key={repair.id} className="px-5 py-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <span className="min-w-0 font-medium [overflow-wrap:anywhere]">{repair.title || repair.altmount_file_path.split("/").pop()}{repair.title && scopeLabel(repair.variant) ? <span className="font-normal text-text-3"> · {scopeLabel(repair.variant)}</span> : null}</span>
              <Badge tone={repairTone(repair.state)}>{repairState[repair.state] ?? humanize(repair.state)}</Badge>
              <Badge>{repairReasonLabel(repair.reason)}</Badge>
              {repair.repair_workflow_id ? <Link to={workflowHref({ id: repair.repair_workflow_id })} className="ml-auto font-medium text-accent-strong hover:underline">Arbejde</Link> : null}
            </div>
            <div className="mt-0.5 text-xs text-text-3 [overflow-wrap:anywhere]">Oprettet {formatRelative(repair.created_at)}{repair.detail ? ` · ${translateDetail(repair.detail)}` : ""}</div>
          </li>
        ))}
      </ul>
      <ShowMore remaining={remaining} onClick={showMore} />
    </Card>
  )
}

const recycleState: Record<string, string> = { planned: "Flyttes til papirkurven", retained: "I papirkurven", restore_planned: "Gendannes", purge_planned: "Slettes endeligt", awaiting_confirmation: "Venter på dig", failed: "Fejlet" }

/** Deleted titles, one row per delete, newest first. */
function RecycleBin() {
  const queryClient = useQueryClient()
  const toast = useToast()
  const query = useQuery({ queryKey: ["recycle"], queryFn: ({ signal }) => loadRecycle(signal), refetchInterval: 30_000 })
  const restore = useMutation({
    mutationFn: (entry: APIRecycleEntry) => restoreRecycle(entry.id),
    onSuccess: async (_, entry) => {
      toast("good", `${entry.media_title} bliver gendannet. Den er tilbage i Plex om lidt.`)
      await queryClient.invalidateQueries()
    },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  if (query.error) return <ErrorBox error={query.error} retry={() => query.refetch()} />
  if (!query.data) return <Card><ListSkeleton /></Card>
  const cycles = new Map<string, APIRecycleEntry[]>()
  for (const entry of query.data) {
    if (entry.purpose !== "media_delete" || entry.state === "restored" || entry.state === "purged") continue
    cycles.set(entry.cycle_id, [...(cycles.get(entry.cycle_id) ?? []), entry])
  }
  const groups = [...cycles.values()].sort((a, b) => b[0].created_at.localeCompare(a[0].created_at))
  return (
    <Card>
      <CardHeader title="Papirkurv" description="Slettede titler. De kan gendannes i 30 dage; derefter fjernes linkene for altid. Selve filerne i AltMount bliver aldrig slettet." />
      {groups.length === 0 ? <Empty title="Papirkurven er tom" /> : null}
      <ul className="divide-y divide-line">
        {groups.map((entries) => {
          const first = entries[0]
          const restorable = entries.every((entry) => entry.entry_kind === "link" && entry.state === "retained")
          const state = entries.every((entry) => entry.state === first.state) ? recycleState[first.state] ?? humanize(first.state) : "Flere tilstande"
          return (
            <li key={first.cycle_id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
              <div className="min-w-0 flex-1">
                <div className="font-medium">{first.media_title}</div>
                <div className="text-xs text-text-3">Slettet {formatRelative(first.created_at)} · {pluralize(entries.length, "fil", "filer")} · gemmes til {formatDateTime(first.purge_after)}</div>
              </div>
              <Badge tone={first.state === "failed" ? "bad" : "neutral"}>{state}</Badge>
              {restorable ? <Button className="h-8 text-xs" busy={restore.isPending && restore.variables?.cycle_id === first.cycle_id} onClick={() => restore.mutate(first)}>Gendan</Button> : null}
            </li>
          )
        })}
      </ul>
    </Card>
  )
}

function ActivityLog() {
  const query = useQuery({ queryKey: ["activity"], queryFn: ({ signal }) => loadActivity(signal), refetchInterval: 30_000 })
  const { shown, remaining, showMore } = useShowMore(query.data ?? [], 50)
  if (query.error) return <ErrorBox error={query.error} />
  if (!query.data) return <Card><ListSkeleton /></Card>
  return (
    <Card>
      <CardHeader title="Aktivitet" description="De seneste ændringer og handlinger, nyeste først." />
      {query.data.length === 0 ? <Empty title="Ingen aktivitet endnu" /> : null}
      <ul className="divide-y divide-line">
        {shown.map((entry) => (
          <li key={entry.id} className="flex items-start gap-3 px-5 py-2.5 text-sm">
            <div className="min-w-0 flex-1">
              <div className="[overflow-wrap:anywhere]">
                <span title={entry.action}>{activityLabel(entry.action)}</span>
                {entry.title ? (
                  <>
                    <span className="text-text-3"> · </span>
                    {entry.media_id ? <Link to={titleHref(entry.media_id)} className="font-medium hover:underline">{entry.title}</Link> : <span className="font-medium">{entry.title}</span>}
                    {scopeLabel(entry.variant) ? <span className="text-text-3"> · {scopeLabel(entry.variant)}</span> : null}
                  </>
                ) : null}
              </div>
              <div className="mt-0.5 text-xs text-text-3">{formatRelative(entry.occurred_at)} · {activityActorLabel(entry.actor)}</div>
            </div>
            {entry.aggregate_type === "workflow" && entry.aggregate_id ? <Link to={workflowHref({ id: entry.aggregate_id })} className="shrink-0 text-accent-strong hover:underline">Arbejde</Link> : null}
          </li>
        ))}
      </ul>
      <ShowMore remaining={remaining} onClick={showMore} />
    </Card>
  )
}
