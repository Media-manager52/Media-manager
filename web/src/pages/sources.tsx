import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"

import { loadIndexers, loadOldBoysCredentialStatus, saveOldBoysCredentials, updateIndexer, type APIIndexerUsage } from "@/api/client"
import { AddIndexerDialog, IndexerSetupActions } from "@/components/setup-forms"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, CardHeader, Dialog, ErrorBox, PageHeader, Progress } from "@/components/ui"
import { formatDateTime, formatNumber, formatRelative } from "@/format"
import { ListSkeleton } from "@/pages/overview"

export function SourcesPage() {
  const query = useQuery({ queryKey: ["indexers"], queryFn: ({ signal }) => loadIndexers(signal), refetchInterval: 60_000 })
  const items = [...(query.data ?? [])].sort((a, b) => Number(b.enabled) - Number(a.enabled) || a.name.localeCompare(b.name, "da"))
  const [adding, setAdding] = useState(false)
  return (
    <>
      <PageHeader title="Kilder" description="Indexerne, Media Manager søger hos, og hvor meget af dagens grænse der er brugt. Grænserne gælder et rullende døgn." actions={<Button variant="primary" onClick={() => setAdding(true)}>Tilføj indexer</Button>} />
      {adding ? <AddIndexerDialog onClose={() => setAdding(false)} /> : null}
      {query.error ? <ErrorBox error={query.error} retry={() => query.refetch()} /> : null}
      {query.isPending ? <Card><ListSkeleton rows={6} /></Card> : null}
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
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-text-3">
          <span>Baggrund tilbage: {formatNumber(usage.background_queries_remaining)}</span>
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
      toast("good", `${indexer.name} er opdateret.`)
      onClose()
      await queryClient.invalidateQueries({ queryKey: ["indexers"] })
    },
    onError: (error) => toast("bad", error instanceof Error ? error.message : String(error)),
  })
  const field = "mt-1 h-9 w-full rounded-lg border border-line bg-surface px-3 text-text"
  return (
    <Dialog open onClose={onClose} title={`Rediger ${indexer.name}`} footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} onClick={() => mutation.mutate()}>Gem</Button></>}>
      <div className="space-y-4">
        <label className="flex items-center gap-2 text-text"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} className="size-4" />Brug denne indexer</label>
        <label className="block">Søgninger pr. døgn<input inputMode="numeric" value={queryLimit} onChange={(event) => setQueryLimit(event.target.value)} className={field} /></label>
        <label className="block">Hentninger pr. døgn<input inputMode="numeric" value={grabLimit} onChange={(event) => setGrabLimit(event.target.value)} className={field} /></label>
        <label className="block">Mindste pause mellem kald (ms)<input inputMode="numeric" value={interval} onChange={(event) => setIntervalMs(event.target.value)} className={field} /></label>
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
    onError: (error) => toast("bad", error instanceof Error ? error.message : String(error)),
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
