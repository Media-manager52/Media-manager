import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { NavLink, Link, useParams } from "react-router"

import { createBackup, loadActivity, loadBackups, loadQueue, loadRepairs, loadSystemHealth, loadSystemInfo } from "@/api/client"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, CardHeader, cx, Dot, Empty, ErrorBox, KeyValue, PageHeader } from "@/components/ui"
import { formatBytes, formatDateTime, formatNumber, formatRelative } from "@/format"
import { humanize } from "@/i18n"
import { workflowHref } from "@/lib/links"
import { ListSkeleton } from "@/pages/overview"

const sections = [
  { id: "", label: "Status" },
  { id: "koe", label: "Baggrundskø" },
  { id: "backup", label: "Backup" },
  { id: "reparationer", label: "Reparationer" },
  { id: "aktivitet", label: "Aktivitet" },
]

export function SystemPage() {
  const { section = "" } = useParams()
  return (
    <>
      <PageHeader title="System" description="Hvordan Media Manager og forbindelserne har det." />
      <nav aria-label="System" className="mb-6 flex gap-1 overflow-x-auto border-b border-line">
        {sections.map((entry) => (
          <NavLink key={entry.id} end to={entry.id ? `/system/${entry.id}` : "/system"} className={({ isActive }) => cx("-mb-px shrink-0 border-b-2 px-3 py-2 text-sm font-medium", isActive ? "border-accent text-text" : "border-transparent text-text-2 hover:text-text")}>
            {entry.label}
          </NavLink>
        ))}
      </nav>
      {section === "" ? <Status /> : null}
      {section === "koe" ? <Queue /> : null}
      {section === "backup" ? <Backups /> : null}
      {section === "reparationer" ? <Repairs /> : null}
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
    <div className="grid gap-6 xl:grid-cols-2">
      <Card>
        <CardHeader title="Forbindelser" description={`Tjekket ${formatDateTime(h.checked_at)}`} />
        <ul className="divide-y divide-line">
          {[...h.components].sort((a, b) => a.kind.localeCompare(b.kind) || a.name.localeCompare(b.name, "da")).map((component) => {
            const tone = !component.enabled ? "neutral" : component.consecutive_failure > 0 ? "bad" : component.configuration_state === "awaiting_commission" ? "warn" : "good"
            return (
              <li key={component.id} className="flex items-center gap-3 px-5 py-3 text-sm">
                <Dot tone={tone} />
                <span className="min-w-0 flex-1 truncate font-medium">{component.name.replace(/\s*\{DK\}\s*/i, "")}</span>
                <span className="text-text-3">{component.kind}</span>
                <span className="w-36 text-right text-text-2">
                  {!component.enabled ? "Slået fra" : component.consecutive_failure > 0 ? `${component.consecutive_failure} fejl i træk` : component.last_outcome ? humanize(component.last_outcome) : "OK"}
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
          </ul>
        </Card>
        {info.data ? (
          <Card>
            <CardHeader title="Version" />
            <div className="px-5 py-4">
              <KeyValue items={[
                ["Version", info.data.version],
                ["Startet", formatDateTime(info.data.started_at)],
                ["Login", info.data.browser_auth],
                ["Papirkurv", `${info.data.recycle_retention_days} dage`],
              ]} />
            </div>
          </Card>
        ) : null}
      </div>
    </div>
  )
}

function Queue() {
  const query = useQuery({ queryKey: ["system", "queue"], queryFn: ({ signal }) => loadQueue(signal), refetchInterval: 15_000 })
  if (query.error) return <ErrorBox error={query.error} />
  if (!query.data) return <Card><ListSkeleton /></Card>
  const q = query.data
  return (
    <Card>
      <CardHeader title="Baggrundskø" description={`${formatNumber(q.completed_last_hour)} opgaver løst den seneste time · ${formatNumber(q.due)} klar · ${formatNumber(q.scheduled)} planlagt · ${formatNumber(q.retrying)} prøver igen`} />
      <div className="overflow-x-auto" tabIndex={0} role="region" aria-label="Køen pr. opgavetype">
        <table className="w-full min-w-[36rem] text-sm">
          <thead className="text-left text-text-3">
            <tr className="border-b border-line">
              <th className="px-5 py-2 font-medium">Opgave</th>
              <th className="px-3 py-2 text-right font-medium">Klar</th>
              <th className="px-3 py-2 text-right font-medium">I gang</th>
              <th className="px-3 py-2 text-right font-medium">Prøver igen</th>
              <th className="px-3 py-2 text-right font-medium">Planlagt</th>
              <th className="px-5 py-2 font-medium">Ældste klar</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {q.by_type.map((row) => (
              <tr key={row.event_type}>
                <td className="px-5 py-2 font-mono text-xs">{row.event_type}</td>
                <td className="tabular px-3 py-2 text-right">{formatNumber(row.due)}</td>
                <td className="tabular px-3 py-2 text-right">{formatNumber(row.leased)}</td>
                <td className={cx("tabular px-3 py-2 text-right", row.retrying > 0 && "text-warn")}>{formatNumber(row.retrying)}</td>
                <td className="tabular px-3 py-2 text-right">{formatNumber(row.scheduled)}</td>
                <td className="px-5 py-2 text-text-2">{row.oldest_due_at ? formatRelative(row.oldest_due_at) : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  )
}

function Backups() {
  const queryClient = useQueryClient()
  const toast = useToast()
  const query = useQuery({ queryKey: ["backups"], queryFn: ({ signal }) => loadBackups(signal), refetchInterval: 30_000 })
  const create = useMutation({
    mutationFn: createBackup,
    onSuccess: async () => { toast("good", "Backup er sat i kø. Den bliver kontrolleret med en prøve-gendannelse."); await queryClient.invalidateQueries({ queryKey: ["backups"] }) },
    onError: (error) => toast("bad", error instanceof Error ? error.message : String(error)),
  })
  if (query.error) return <ErrorBox error={query.error} />
  if (!query.data) return <Card><ListSkeleton /></Card>
  const b = query.data
  return (
    <Card>
      <CardHeader
        title="Backup"
        description={b.offsite.configured ? "Krypterede backups, også kopieret uden for serveren." : "Krypterede backups. Der er ingen kopi uden for serveren endnu."}
        action={<Button variant="primary" busy={create.isPending} disabled={!b.configured} onClick={() => create.mutate()}>Tag backup nu</Button>}
      />
      {b.records.length === 0 ? <Empty title="Ingen backups endnu" /> : null}
      <ul className="divide-y divide-line">
        {b.records.slice(0, 30).map((record) => (
          <li key={record.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
            <Badge tone={record.state === "verified" ? "good" : record.state === "failed" ? "bad" : "info"}>{{ verified: "Kontrolleret", failed: "Fejlet", queued: "I kø", running: "Kører" }[record.state]}</Badge>
            <span className="flex-1">{formatDateTime(record.completed_at ?? record.created_at)}</span>
            <span className="tabular text-text-3">{formatBytes(record.size_bytes)}</span>
            {record.last_error ? <span className="w-full break-words text-bad">{record.last_error}</span> : null}
          </li>
        ))}
      </ul>
    </Card>
  )
}

const repairState: Record<string, string> = { open: "Åben", searching: "Søger", resolved: "Repareret", not_found: "Intet fundet", cancelled: "Annulleret" }

function Repairs() {
  const query = useQuery({ queryKey: ["repairs"], queryFn: ({ signal }) => loadRepairs(signal), refetchInterval: 60_000 })
  if (query.error) return <ErrorBox error={query.error} />
  if (!query.data) return <Card><ListSkeleton /></Card>
  return (
    <Card>
      <CardHeader title="Reparationer" description="Filer, som AltMount har meldt ødelagte, eller som mangler dansk tale. Media Manager finder selv en erstatning." />
      {query.data.length === 0 ? <Empty title="Ingen reparationer" /> : null}
      <ul className="divide-y divide-line">
        {query.data.slice(0, 200).map((repair) => (
          <li key={repair.id} className="px-5 py-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{repair.title || repair.altmount_file_path.split("/").pop()}</span>
              <Badge tone={repair.state === "resolved" ? "good" : repair.state === "not_found" ? "warn" : "info"}>{repairState[repair.state] ?? humanize(repair.state)}</Badge>
              <Badge>{repair.reason === "missing_required_audio" ? "Mangler dansk tale" : "Ødelagt fil"}</Badge>
              {repair.repair_workflow_id ? <Link to={workflowHref({ id: repair.repair_workflow_id })} className="ml-auto font-medium text-accent-strong hover:underline">Arbejde</Link> : null}
            </div>
            <div className="mt-0.5 text-xs text-text-3">Oprettet {formatRelative(repair.created_at)}{repair.detail ? ` · ${repair.detail}` : ""}</div>
          </li>
        ))}
      </ul>
    </Card>
  )
}

function ActivityLog() {
  const query = useQuery({ queryKey: ["activity"], queryFn: ({ signal }) => loadActivity(signal), refetchInterval: 30_000 })
  if (query.error) return <ErrorBox error={query.error} />
  if (!query.data) return <Card><ListSkeleton /></Card>
  return (
    <Card>
      <CardHeader title="Aktivitet" description="De seneste ændringer og handlinger, nyeste først." />
      <ul className="divide-y divide-line">
        {query.data.slice(0, 200).map((entry) => (
          <li key={entry.id} className="flex flex-wrap items-center gap-x-3 gap-y-0.5 px-5 py-2.5 text-sm">
            <span className="w-28 shrink-0 text-text-3">{formatRelative(entry.occurred_at)}</span>
            <span className="font-mono text-xs">{entry.action}</span>
            <span className="text-text-3">{entry.actor}</span>
            {entry.aggregate_type === "workflow" && entry.aggregate_id ? <Link to={workflowHref({ id: entry.aggregate_id })} className="ml-auto text-accent-strong hover:underline">Arbejde</Link> : null}
          </li>
        ))}
      </ul>
    </Card>
  )
}
