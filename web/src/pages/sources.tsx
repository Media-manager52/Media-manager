import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"

import { loadIndexers, loadOldBoysCredentialStatus, saveOldBoysCredentials, updateIndexer, type APIIndexerUsage } from "@/api/client"
import { AddIndexerDialog, IndexerSetupActions } from "@/components/setup-forms"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, CardHeader, Dialog, Empty, ErrorBox, PageHeader, Progress } from "@/components/ui"
import { formatDateTime, formatNumber, formatRelative } from "@/format"
import { ListSkeleton } from "@/pages/overview"
import { errorMessage } from "@/i18n"

export function SourcesPage() {
  const query = useQuery({ queryKey: ["indexers"], queryFn: ({ signal }) => loadIndexers(signal), refetchInterval: 60_000 })
  const items = [...(query.data ?? [])].sort((a, b) => Number(b.enabled) - Number(a.enabled) || a.name.localeCompare(b.name, "da"))
  const [adding, setAdding] = useState(false)
  return (
    <>
      <PageHeader title="Kilder" description="Indexerne, Media Manager søger hos, hvor meget af dagens grænse der er brugt, og hvor mange downloads fra hver der blev godkendt. Grænserne gælder et rullende døgn." actions={<Button variant="primary" onClick={() => setAdding(true)}>Tilføj indexer</Button>} />
      {adding ? <AddIndexerDialog onClose={() => setAdding(false)} /> : null}
      {query.error ? <ErrorBox error={query.error} retry={() => query.refetch()} /> : null}
      {query.isPending ? <Card><ListSkeleton rows={6} /></Card> : null}
      {!query.isPending && !query.error && items.length === 0 ? <Card className="mb-4"><Empty title="Ingen indexere endnu">Tryk "Tilføj indexer" for at vælge en fra listen. Media Manager søger kun hos indexere, der er sat op her.</Empty></Card> : null}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {items.map((indexer) => <IndexerCard key={indexer.id} indexer={indexer} />)}
      </div>
      <OldBoysCard />
    </>
  )
}

function IndexerCard({ indexer }: { indexer: APIIndexerUsage }) {
  const [editing, setEditing] = useState(false)
  const { budget, usage, pacing } = indexer
  const usedShare = budget.query_limit ? usage.queries_used / budget.query_limit : 0
  const failing = pacing.consecutive_failures > 0
  const cooling = isFuture(pacing.cooldown_until)
  const outcomes = indexer.search_outcomes_24h ?? []
  const countOf = (state: string) => outcomes.filter((entry) => entry.state === state).reduce((sum, entry) => sum + entry.count, 0)
  const succeeded = countOf("succeeded")
  const deferred = countOf("deferred")
  const failed = countOf("failed")
  const downloads = indexer.downloads_30d
  return (
    <Card>
      <div className="flex flex-wrap items-start justify-between gap-3 px-5 pt-4">
        <div className="min-w-0">
          <h2 className="font-semibold">{indexer.name.replace(/\s*\{DK\}\s*/i, "")}</h2>
          <div className="mt-1 flex flex-wrap gap-1.5">
            {!indexer.enabled ? <Badge>Slået fra</Badge> : failing ? <Badge tone="bad">{pacing.consecutive_failures} fejl i træk</Badge> : cooling ? <Badge tone="warn">Holder pause</Badge> : <Badge tone="good">Kører</Badge>}
            {!indexer.commissioned && indexer.enabled ? <Badge tone="warn">Ikke godkendt</Badge> : null}
            <Badge>{indexer.kind === "oldboys" ? "OldBoys" : "Newznab"}</Badge>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <IndexerSetupActions integration={indexer} commissioned={indexer.commissioned} />
          <Button variant="ghost" className="h-8 text-xs" onClick={() => setEditing(true)}>Rediger</Button>
        </div>
      </div>
      <div className="space-y-3 px-5 py-4 text-sm">
        <div>
          <div className="mb-1 flex justify-between"><span className="text-text-2">Søgninger</span><span className="tabular">{formatNumber(usage.queries_used)} / {formatNumber(budget.query_limit)}</span></div>
          <Progress value={usage.queries_used} max={budget.query_limit} tone={usedShare > 0.9 ? "bad" : usedShare > 0.7 ? "warn" : "info"} label="Søgninger brugt" />
        </div>
        <div>
          <div className="mb-1 flex justify-between"><span className="text-text-2">Hentninger (NZB)</span><span className="tabular">{formatNumber(usage.grabs_used)} / {formatNumber(budget.grab_limit)}</span></div>
          <Progress value={usage.grabs_used} max={budget.grab_limit} tone="info" label="Hentninger brugt" />
        </div>
        {downloads ? (
          <div>
            <div className="mb-1 flex justify-between gap-3"><span className="text-text-2">Godkendte downloads, 30 dage</span><span className="tabular">{downloads.finished ? `${formatNumber(downloads.verified)} af ${formatNumber(downloads.finished)} (${formatNumber(Math.round((downloads.verified / downloads.finished) * 100))} %)` : "Ingen downloads"}</span></div>
            {downloads.finished ? <Progress value={downloads.verified} max={downloads.finished} tone="good" label="Godkendte downloads de seneste 30 dage" /> : null}
            <p className="mt-1 text-xs text-text-3">Godkendt betyder, at filen blev hentet og bestod kontrollen af sprog og kvalitet.</p>
          </div>
        ) : null}
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-text-3">
          <span>Automatiske søgninger tilbage: {formatNumber(usage.background_queries_remaining)}</span>
          {outcomes.length ? <span>Sidste døgn: {formatNumber(succeeded)} svar · {formatNumber(deferred)} udsat (grænse/pause){failed ? ` · ${formatNumber(failed)} fejl` : ""}</span> : null}
          {pacing.last_outcome_at ? <span>Seneste svar {formatRelative(pacing.last_outcome_at)}</span> : null}
          {cooling ? <span>Pause til {formatDateTime(pacing.cooldown_until)}</span> : null}
        </div>
      </div>
      {editing ? <EditIndexer indexer={indexer} onClose={() => setEditing(false)} /> : null}
    </Card>
  )
}

function EditIndexer({ indexer, onClose }: { indexer: APIIndexerUsage; onClose: () => void }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const [enabled, setEnabled] = useState(indexer.enabled)
  const [queryLimit, setQueryLimit] = useState(String(indexer.budget.query_limit))
  const [grabLimit, setGrabLimit] = useState(String(indexer.budget.grab_limit))
  const [interval, setIntervalMs] = useState(String(indexer.budget.minimum_interval_ms))
  const mutation = useMutation({
    mutationFn: () => updateIndexer(indexer.id, {
      ...(enabled !== indexer.enabled ? { enabled } : {}),
      ...(Number(queryLimit) !== indexer.budget.query_limit ? { query_limit: Number(queryLimit) } : {}),
      ...(Number(grabLimit) !== indexer.budget.grab_limit ? { grab_limit: Number(grabLimit) } : {}),
      ...(Number(interval) !== indexer.budget.minimum_interval_ms ? { minimum_interval_ms: Number(interval) } : {}),
    }),
    onSuccess: async () => {
      toast("good", `${indexer.name.replace(/\s*\{DK\}\s*/i, "")} er opdateret.`)
      onClose()
      await queryClient.invalidateQueries({ queryKey: ["indexers"] })
    },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  const field = "mt-1 h-9 w-full rounded-lg border border-line bg-surface px-3 text-text"
  // Whole numbers above 0, as when the indexer was added; an empty or
  // mistyped field must never be sent.
  const valid = [queryLimit, grabLimit, interval].every((value) => /^\d+$/.test(value.trim()) && Number(value) > 0)
  return (
    <Dialog open onClose={onClose} title={`Rediger ${indexer.name.replace(/\s*\{DK\}\s*/i, "")}`} footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!valid} onClick={() => mutation.mutate()}>Gem</Button></>}>
      <div className="space-y-4">
        <label className="flex items-center gap-2 text-text"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} className="size-4" />Brug denne indexer</label>
        <label className="block">Søgninger pr. døgn<input inputMode="numeric" value={queryLimit} onChange={(event) => setQueryLimit(event.target.value)} className={field} /></label>
        <label className="block">Hentninger pr. døgn<input inputMode="numeric" value={grabLimit} onChange={(event) => setGrabLimit(event.target.value)} className={field} /></label>
        <label className="block">Mindste pause mellem kald (ms)<input inputMode="numeric" value={interval} onChange={(event) => setIntervalMs(event.target.value)} className={field} /></label>
        {!valid ? <p role="alert" className="text-xs text-bad">Skriv et helt tal over 0 i hvert felt.</p> : null}
        <p className="text-xs text-text-3">Sæt kun grænser, din konto hos indexeren faktisk tillader. Baggrundssøgning må højst bruge 20 % af søgningerne.</p>
      </div>
    </Dialog>
  )
}

function OldBoysCard() {
  const status = useQuery({ queryKey: ["oldboys", "credentials"], queryFn: ({ signal }) => loadOldBoysCredentialStatus(signal) })
  const queryClient = useQueryClient()
  const toast = useToast()
  const [open, setOpen] = useState(false)
  const [values, setValues] = useState({ api_token: "", pass_key: "", rss_key: "" })
  const mutation = useMutation({
    mutationFn: () => saveOldBoysCredentials(values),
    onSuccess: async () => {
      toast("good", "OldBoys-nøglerne er gemt krypteret. OldBoys er slået fra, indtil den er godkendt igen.")
      setOpen(false)
      setValues({ api_token: "", pass_key: "", rss_key: "" })
      await queryClient.invalidateQueries()
    },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  if (!status.data?.available) return null
  const field = "mt-1 h-9 w-full rounded-lg border border-line bg-surface px-3 font-mono text-text"
  return (
    <Card className="mt-6">
      <CardHeader
        title="OldBoys-nøgler"
        description={status.data.configured ? `Gemt${status.data.updated_at ? ` ${formatDateTime(status.data.updated_at)}` : ""}. Nøglerne vises aldrig igen.` : "Ingen nøgler gemt."}
        action={<Button onClick={() => setOpen(true)}>{status.data.configured ? "Udskift nøgler" : "Tilføj nøgler"}</Button>}
      />
      <Dialog open={open} onClose={() => setOpen(false)} title="OldBoys-nøgler" footer={<><Button variant="ghost" onClick={() => setOpen(false)}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!values.api_token || !values.pass_key || !values.rss_key} onClick={() => mutation.mutate()}>Gem krypteret</Button></>}>
        <div className="space-y-3">
          <label className="block">API Token<input type="password" autoComplete="off" value={values.api_token} onChange={(event) => setValues({ ...values, api_token: event.target.value })} className={field} /></label>
          <label className="block">Pass Key (PID)<input type="password" autoComplete="off" value={values.pass_key} onChange={(event) => setValues({ ...values, pass_key: event.target.value })} className={field} /></label>
          <label className="block">RSS Key (RID)<input type="password" autoComplete="off" value={values.rss_key} onChange={(event) => setValues({ ...values, rss_key: event.target.value })} className={field} /></label>
        </div>
      </Dialog>
    </Card>
  )
}

function isFuture(value: string | undefined): boolean {
  return Boolean(value) && new Date(value!).getTime() > Date.now()
}
