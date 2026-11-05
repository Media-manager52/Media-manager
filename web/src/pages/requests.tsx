import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useMemo, useState } from "react"
import { Link } from "react-router"

import { decideRequest, loadRequestsPage, type APIRequest } from "@/api/client"
import { Poster, sizedArtwork } from "@/components/poster"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, Dialog, Empty, ErrorBox, PageHeader, SearchField, Segmented } from "@/components/ui"
import { formatRelative } from "@/format"
import { kindLabel, stateLabel, translateDetail } from "@/i18n"
import { titleHref } from "@/lib/links"
import { requestLifecycleTone, requestMatchesFilter, type RequestFilter } from "@/lib/media-manager-requests"
import { ListSkeleton } from "@/pages/overview"

async function loadAllRequests(signal: AbortSignal): Promise<APIRequest[]> {
  const all: APIRequest[] = []
  for (let page = 0; page < 20; page += 1) {
    const result = await loadRequestsPage({ limit: 300, offset: page * 300, signal })
    all.push(...result.items)
    if (!result.hasMore) break
  }
  return all
}

export function RequestsPage() {
  const query = useQuery({ queryKey: ["requests"], queryFn: ({ signal }) => loadAllRequests(signal), refetchInterval: 60_000 })
  const [chosen, setFilter] = useState<RequestFilter | "pending">("active")
  const [search, setSearch] = useState("")
  const all = useMemo(() => query.data ?? [], [query.data])
  const pending = all.filter((item) => item.lifecycle === "not_approved")
  // Once the last request is decided the "Skal godkendes" tab disappears;
  // fall back to the active list instead of an empty, unselectable tab.
  const filter = chosen === "pending" && pending.length === 0 && query.data ? "active" : chosen
  const items = useMemo(() => {
    const needle = search.trim().toLocaleLowerCase("da")
    return all
      .filter((item) => (filter === "pending" ? item.lifecycle === "not_approved" : requestMatchesFilter(item, filter)))
      .filter((item) => !needle || item.title.toLocaleLowerCase("da").includes(needle) || (item.requester ?? "").toLocaleLowerCase("da").includes(needle))
      .sort((a, b) => (b.requested_at ?? b.updated_at).localeCompare(a.requested_at ?? a.updated_at))
  }, [all, filter, search])
  const count = (value: RequestFilter) => all.filter((item) => requestMatchesFilter(item, value)).length
  return (
    <>
      <PageHeader title="Anmodninger" description="Ønsker fra Seerr, og hvor langt Media Manager er nået med hver af dem." />
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <Segmented
          label="Filter"
          value={filter}
          onChange={setFilter}
          options={[
            ...(pending.length ? [{ value: "pending" as const, label: "Skal godkendes", count: pending.length }] : []),
            { value: "active", label: "I gang", count: count("active") },
            { value: "attention", label: "Kræver handling", count: count("attention") },
            { value: "completed", label: "Færdige", count: count("completed") },
            { value: "all", label: "Alle", count: all.length },
          ]}
        />
        <SearchField value={search} onChange={setSearch} placeholder="Søg titel eller person" label="Søg i anmodninger" />
      </div>
      {query.error ? <ErrorBox error={query.error} retry={() => query.refetch()} /> : null}
      <Card>
        {query.isPending ? <ListSkeleton rows={6} /> : null}
        {!query.isPending && items.length === 0 && !query.error ? <Empty title="Ingen anmodninger her" /> : null}
        <ul className="divide-y divide-line">
          {items.map((item) => <RequestRow key={item.id} item={item} />)}
        </ul>
      </Card>
    </>
  )
}

function RequestRow({ item }: { item: APIRequest }) {
  const title = (
    <>
      {item.title}
      {item.year ? <span className="font-normal text-text-3"> ({item.year})</span> : null}
    </>
  )
  const seasons = item.media_kind === "series" && item.season_count > 0 ? ` · ${item.available_seasons} af ${item.season_count} sæsoner klar` : ""
  const progress = item.workflow_count > 0 ? ` · ${item.confirmed_count} af ${item.workflow_count} dele i Plex` : ""
  return (
    <li className="flex gap-4 px-5 py-4">
      <Poster src={sizedArtwork(item.poster_url, "w185")} title={item.title} kind={item.media_kind} className="w-12 shrink-0 rounded-lg" />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-start gap-2">
          <div className="min-w-0 flex-1 font-medium">{item.media_id ? <Link to={titleHref(item.media_id)} className="hover:underline">{title}</Link> : title}</div>
          <Badge tone={requestLifecycleTone(item.lifecycle)}>{stateLabel(item.lifecycle)}</Badge>
        </div>
        <div className="mt-0.5 text-sm text-text-2">
          {kindLabel(item.media_kind || undefined)}{item.requester ? ` · ønsket af ${item.requester}` : ""} · {formatRelative(item.requested_at ?? item.updated_at)}{seasons}{progress}
        </div>
        {item.attention_detail || item.intake_error ? <p className={`mt-1 break-words text-sm ${item.attention_count > 0 || item.intake_error ? "text-warn" : "text-text-3"}`}>{translateDetail(item.attention_detail || item.intake_error)}</p> : null}
        {!item.attention_detail && item.waiting_reason ? <p className="mt-1 text-sm text-text-3">{translateDetail(item.waiting_reason)}{item.next_search_at ? ` · næste søgning ${formatRelative(item.next_search_at)}` : ""}</p> : null}
        {item.lifecycle === "not_approved" ? <Decide item={item} /> : null}
      </div>
    </li>
  )
}

function Decide({ item }: { item: APIRequest }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const [confirm, setConfirm] = useState<"approve" | "decline" | null>(null)
  const mutation = useMutation({
    mutationFn: (decision: "approve" | "decline") => decideRequest(item.id, decision),
    onSuccess: async (_, decision) => {
      toast("good", decision === "approve" ? `${item.title} er godkendt.` : `${item.title} er afvist.`)
      setConfirm(null)
      await queryClient.invalidateQueries({ queryKey: ["requests"] })
    },
    onError: (error) => toast("bad", error instanceof Error ? error.message : String(error)),
  })
  return (
    <div className="mt-3 flex gap-2">
      <Button variant="primary" className="h-8 text-xs" onClick={() => setConfirm("approve")}>Godkend</Button>
      <Button variant="ghost" className="h-8 text-xs" onClick={() => setConfirm("decline")}>Afvis</Button>
      <Dialog
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        title={confirm === "approve" ? `Godkend ${item.title}?` : `Afvis ${item.title}?`}
        footer={
          <>
            <Button variant="ghost" onClick={() => setConfirm(null)}>Fortryd</Button>
            <Button variant={confirm === "approve" ? "primary" : "danger"} busy={mutation.isPending} onClick={() => confirm && mutation.mutate(confirm)}>
              {confirm === "approve" ? "Godkend" : "Afvis"}
            </Button>
          </>
        }
      >
        {confirm === "approve" ? "Beslutningen sendes til Seerr. Media Manager begynder derefter at søge." : "Beslutningen sendes til Seerr, og der hentes intet."}
      </Dialog>
    </div>
  )
}
