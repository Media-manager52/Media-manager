import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useMemo, useState } from "react"
import { useSearchParams } from "react-router"

import {
  approveAdoptionBatch, createAdoptionBatch, executeAdoptionBatch, loadAdoptionBatch, loadAdoptionBatches, loadCatalogMediaBounded, loadLibraryRoots, loadQualityProfiles,
  reviewAdoptionItem, type APIAdoptionBatch, type APIAdoptionItem, type APIMedia,
} from "@/api/client"
import { OptionPicker } from "@/components/option-picker"
import { fieldClass } from "@/components/setup-forms"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, CardHeader, Empty, ErrorBox } from "@/components/ui"
import { errorMessage, humanize } from "@/i18n"
import { adoptionBatchLimit, adoptionPhrase, guessMedia, proposedName, seasonVariant, validSeasonVariant } from "@/lib/adoption"
import { profileOptions, rootOptions } from "@/lib/pickers"
import { ListSkeleton } from "@/pages/overview"

const pageSize = 25
const batchStates: Record<string, string> = { scanning: "Leder efter filer", review: "Gennemgås", approved: "Klar til overtagelse", executing: "Overtages nu", complete: "Færdig", failed: "Fejlet" }
const itemStates: Record<string, string> = { pending: "Venter", queued: "I kø", planned: "Planlagt", filesystem_verified: "Flyttet, venter på Plex", complete: "Færdig", skipped: "Sprunget over", failed: "Fejlet" }
/** Every scanned file starts as needs_attention; the owner has to decide it. */
const undecided = (item: APIAdoptionItem) => item.review_state === "pending" || item.review_state === "needs_attention"

function useRefreshAdoption(id?: string) {
  const queryClient = useQueryClient()
  return async () => {
    await queryClient.invalidateQueries({ queryKey: ["adoption"] })
    if (id) await queryClient.invalidateQueries({ queryKey: ["adoption", id] })
  }
}

export function AdoptionGuide() {
  const [params, setParams] = useSearchParams()
  const selected = params.get("kladde") ?? ""
  const batches = useQuery({ queryKey: ["adoption"], queryFn: ({ signal }) => loadAdoptionBatches(signal) })
  const open = (id: string) => setParams(id ? { kladde: id } : {})
  if (selected) return <BatchView id={selected} onBack={() => open("")} />
  return (
    <div className="flex flex-col gap-6">
      <p className="text-sm text-text-2">Har du allerede film og serier i Plex? Her kan Media Manager overtage dem. Du ser præcist, hvad der vil ske, før noget bliver ændret, og intet flyttes, før du selv har bekræftet det.</p>
      <NewScan onCreated={open} />
      <Card>
        <CardHeader title="Dine overtagelser" />
        {batches.error ? <div className="p-4"><ErrorBox error={batches.error} retry={() => batches.refetch()} /></div> : null}
        {batches.isPending ? <ListSkeleton rows={2} /> : null}
        {batches.data?.length === 0 ? <Empty title="Ingen overtagelser endnu" /> : null}
        <ul className="divide-y divide-line">
          {(batches.data ?? []).map((batch) => (
            <li key={batch.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
              <div className="min-w-0 flex-1">
                <div className="break-all font-medium">{batch.source_root}</div>
                <div className="text-text-3">{batch.approved_items} godkendt · {batch.pending_items + batch.attention_items} mangler gennemgang · {batch.rejected_items} sprunget over</div>
              </div>
              <Badge>{batchStates[batch.state] ?? humanize(batch.state)}</Badge>
              <Button className="h-8 text-xs" onClick={() => open(batch.id)}>Åbn</Button>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  )
}

/** Step 1: a read-only scan. It finds files and changes nothing. */
function NewScan({ onCreated }: { onCreated: (id: string) => void }) {
  const toast = useToast()
  const refresh = useRefreshAdoption()
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const profiles = useQuery({ queryKey: ["settings", "profiles"], queryFn: ({ signal }) => loadQualityProfiles(signal) })
  const [rootId, setRootId] = useState("")
  const [profileId, setProfileId] = useState("")
  const [source, setSource] = useState("")
  const [monitoring, setMonitoring] = useState(false)
  const standardRoots = (roots.data ?? []).filter((root) => root.enabled && root.edition !== "4k")
  const root = standardRoots.find((entry) => entry.id === rootId)
  const folder = source.trim() || root?.destination_root || ""
  const scan = useMutation({
    mutationFn: () => createAdoptionBatch({ source_root: folder, root_id: rootId, quality_profile_id: profileId, monitoring_enabled: monitoring, search_immediately: false }),
    onSuccess: async (batch) => { await refresh(); onCreated(batch.id) },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  return (
    <Card>
      <CardHeader title="1. Find dine filer" description="Media Manager læser kun mappen. Der bliver ikke ændret noget i dette trin." />
      <div className="grid grid-cols-1 gap-4 p-5 sm:grid-cols-2">
        <OptionPicker label="Bibliotek" value={rootId} onChange={(value) => { setRootId(value); setSource(""); setProfileId("") }} options={[...rootOptions(standardRoots, "movie"), ...rootOptions(standardRoots, "series")]} />
        <OptionPicker label="Kvalitetsprofil" value={profileId} onChange={setProfileId} options={root ? profileOptions(profiles.data ?? [], root.media_kind) : []} />
        <label className="text-sm sm:col-span-2">Mappe
          <input value={folder} onChange={(event) => setSource(event.target.value)} className={fieldClass} placeholder="Vælg først et bibliotek" />
          <span className="mt-1 block text-xs text-text-3">Skal være bibliotekets mappe eller en mappe inde i den. En undermappe er en god måde at starte i det små på.</span>
        </label>
        <label className="flex items-center gap-2 text-sm sm:col-span-2">
          <input type="checkbox" checked={monitoring} onChange={(event) => setMonitoring(event.target.checked)} className="size-4 accent-[var(--accent)]" />
          Hold øje med titlerne efter overtagelsen
        </label>
        <div className="sm:col-span-2"><Button variant="primary" busy={scan.isPending} disabled={!rootId || !profileId || !folder} onClick={() => scan.mutate()}>Find filer</Button></div>
      </div>
    </Card>
  )
}

function BatchView({ id, onBack }: { id: string; onBack: () => void }) {
  const query = useQuery({ queryKey: ["adoption", id], queryFn: ({ signal }) => loadAdoptionBatch(id, signal), refetchInterval: (state) => (state.state.data?.state === "executing" || state.state.data?.state === "scanning" ? 10_000 : false) })
  const [reopened, setReopened] = useState<ReadonlySet<string>>(new Set())
  const reopen = (itemId: string) => setReopened((current) => new Set(current).add(itemId))
  const decided = (itemId: string) => setReopened((current) => { const next = new Set(current); next.delete(itemId); return next })
  if (query.error) return <ErrorBox error={query.error} retry={() => query.refetch()} />
  if (!query.data) return <Card><ListSkeleton rows={4} /></Card>
  const batch = query.data
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="ghost" onClick={onBack}>← Tilbage</Button>
        <span className="min-w-0 flex-1 break-all text-sm text-text-2">{batch.source_root}</span>
        <Badge>{batchStates[batch.state] ?? humanize(batch.state)}</Badge>
      </div>
      {batch.state === "review" ? <Review batch={batch} reopened={reopened} onDecided={decided} /> : null}
      {batch.state === "review" || batch.state === "approved" ? <Preview batch={batch} reopened={reopened} onReopen={reopen} /> : null}
      {batch.state === "approved" ? <Execute batch={batch} /> : null}
      {batch.state !== "review" && batch.state !== "approved" ? <Progress batch={batch} /> : null}
    </div>
  )
}

/** Step 2: the owner decides every file. Nothing is approved automatically. */
function Review({ batch, reopened, onDecided }: { batch: APIAdoptionBatch; reopened: ReadonlySet<string>; onDecided: (itemId: string) => void }) {
  const toast = useToast()
  const refresh = useRefreshAdoption(batch.id)
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const libraryRoot = roots.data?.find((root) => root.id === batch.default_root_id)?.destination_root
  const catalog = useQuery({ queryKey: ["media", "catalog"], queryFn: ({ signal }) => loadCatalogMediaBounded(signal), staleTime: 60_000 })
  const media = useMemo(() => (catalog.data?.items ?? []).filter((item) => item.kind === batch.media_kind).sort((a, b) => a.title.localeCompare(b.title, "da")), [catalog.data, batch.media_kind])
  const [visible, setVisible] = useState(pageSize)
  const pending = (batch.items ?? []).filter((item) => undecided(item) || reopened.has(item.id))
  const remaining = pending.filter(undecided)
  const skipRest = useMutation({
    mutationFn: async () => { for (const item of remaining) await reviewAdoptionItem(batch.id, item.id, { review_state: "rejected" }) },
    onSettled: refresh,
    onError: (error) => toast("bad", errorMessage(error)),
  })
  const full = batch.approved_items >= adoptionBatchLimit
  return (
    <Card>
      <CardHeader title="2. Gennemgå filerne" description={`Vælg titel og nyt navn for hver fil. Højst ${adoptionBatchLimit} filer ad gangen: ${batch.approved_items} godkendt indtil nu.`} action={remaining.length > 1 ? <Button className="h-8 text-xs" busy={skipRest.isPending} onClick={() => skipRest.mutate()}>Spring resten over</Button> : undefined} />
      {catalog.error ? <div className="p-4"><ErrorBox error={catalog.error} retry={() => catalog.refetch()} /></div> : null}
      {catalog.data?.truncated ? <p className="px-5 pt-3 text-sm text-warn">Biblioteket er større end det, listen kan vise, så nogle titler mangler i valget.</p> : null}
      {pending.length === 0 ? <Empty title="Alle filer er gennemgået" /> : null}
      <ul className="divide-y divide-line">
        {libraryRoot === undefined ? null : pending.slice(0, visible).map((item) => <ReviewItem key={item.id} batch={batch} item={item} media={media} full={full} libraryRoot={libraryRoot} onDecided={onDecided} />)}
      </ul>
      {pending.length > visible ? <div className="flex justify-center p-4"><Button onClick={() => setVisible((count) => count + pageSize)}>Vis flere ({pending.length - visible} tilbage)</Button></div> : null}
    </Card>
  )
}

function ReviewItem({ batch, item, media, full, libraryRoot, onDecided }: { batch: APIAdoptionBatch; item: APIAdoptionItem; media: APIMedia[]; full: boolean; libraryRoot: string; onDecided: (itemId: string) => void }) {
  const toast = useToast()
  const refresh = useRefreshAdoption(batch.id)
  const guess = useMemo(() => guessMedia(item.original_relative, media), [item.original_relative, media])
  const [chosen, setChosen] = useState<string>()
  const mediaId = chosen ?? item.media_id ?? guess?.id ?? ""
  const [name, setName] = useState(item.proposed_relative ?? proposedName(batch.source_root, libraryRoot, item.original_relative))
  const [season, setSeason] = useState(item.variant ?? seasonVariant(item.original_relative))
  const series = batch.media_kind === "series"
  const review = useMutation({
    mutationFn: (state: "approved" | "rejected") => reviewAdoptionItem(batch.id, item.id, state === "rejected" ? { review_state: "rejected" } : {
      review_state: "approved", media_id: mediaId, root_id: batch.default_root_id, quality_profile_id: batch.default_quality_profile_id, proposed_relative: name.trim(),
      variant: series ? season : "default", monitoring_enabled: batch.monitoring_enabled, search_immediately: false,
    }),
    onSuccess: async () => { await refresh(); onDecided(item.id) },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  const ready = mediaId && name.trim() && (!series || validSeasonVariant(season)) && (!full || item.review_state === "approved")
  return (
    <li className="space-y-2 px-5 py-4 text-sm">
      <div className="break-all font-medium">{item.original_relative}</div>
      <Companions item={item} />
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label>Titel
          <select aria-label={`Titel for ${item.original_relative}`} value={mediaId} onChange={(event) => setChosen(event.target.value)} className={fieldClass}>
            <option value="">Vælg titel</option>
            {media.map((entry) => <option key={entry.id} value={entry.id}>{entry.title}{entry.year ? ` (${entry.year})` : ""}</option>)}
          </select>
          {!mediaId ? <span className="mt-1 block text-xs text-text-3">Mangler titlen? Tilføj den først under Tilføj titel, uden at søge.</span> : null}
        </label>
        {series ? (
          <label>Sæson
            <input aria-label={`Sæson for ${item.original_relative}`} value={season} onChange={(event) => setSeason(event.target.value.trim())} placeholder="season:01" className={fieldClass} />
          </label>
        ) : null}
        <label className="sm:col-span-2">Navn i biblioteket
          <input aria-label={`Nyt navn for ${item.original_relative}`} value={name} onChange={(event) => setName(event.target.value)} className={fieldClass} />
        </label>
      </div>
      <div className="flex gap-2">
        <Button className="h-8 text-xs" busy={review.isPending} disabled={!ready} onClick={() => review.mutate("approved")}>Godkend</Button>
        <Button variant="ghost" className="h-8 text-xs" busy={review.isPending} onClick={() => review.mutate("rejected")}>Spring over</Button>
      </div>
    </li>
  )
}

function Companions({ item }: { item: APIAdoptionItem }) {
  if (item.recognized_companions.length === 0 && item.unknown_companions.length === 0) return <div className="text-xs text-text-3">Ingen tekstfiler eller andre filer ved siden af.</div>
  return (
    <div className="text-xs text-text-2">
      {item.recognized_companions.length > 0 ? <div className="break-all">Følger med: {item.recognized_companions.join(", ")}</div> : null}
      {item.unknown_companions.length > 0 ? <div className="break-all text-warn">Ukendte filer, som bliver liggende uændret: {item.unknown_companions.join(", ")}</div> : null}
    </div>
  )
}

/** Step 3: exactly what will change, before the plan is locked. */
function Preview({ batch, reopened, onReopen }: { batch: APIAdoptionBatch; reopened: ReadonlySet<string>; onReopen: (itemId: string) => void }) {
  const toast = useToast()
  const refresh = useRefreshAdoption(batch.id)
  const approved = (batch.items ?? []).filter((item) => item.review_state === "approved" && !reopened.has(item.id))
  const skipped = (batch.items ?? []).filter((item) => item.review_state === "rejected" && !reopened.has(item.id))
  const unresolved = batch.pending_items + batch.attention_items
  const lock = useMutation({ mutationFn: () => approveAdoptionBatch(batch.id), onSuccess: refresh, onError: (error) => toast("bad", errorMessage(error)) })
  const reviewing = batch.state === "review"
  return (
    <Card>
      <CardHeader title="3. Se, hvad der vil ske" description={`${approved.length} filer overtages. Ingen er flyttet endnu.`} action={reviewing ? <Button variant="primary" busy={lock.isPending} disabled={approved.length === 0 || unresolved > 0 || reopened.size > 0 || approved.length > adoptionBatchLimit} onClick={() => lock.mutate()}>Lås planen</Button> : undefined} />
      {reviewing && unresolved > 0 ? <p className="px-5 pt-3 text-sm text-text-2">Filer, der mangler gennemgang: {unresolved}. Godkend dem eller spring dem over, før planen kan låses.</p> : null}
      {approved.length === 0 ? <Empty title="Du har ikke godkendt nogen filer endnu" /> : null}
      <ul className="divide-y divide-line">
        {approved.map((item) => (
          <li key={item.id} className="flex flex-wrap items-start gap-3 px-5 py-3 text-sm">
            <div className="min-w-0 flex-1">
              <div className="break-all text-text-3">{item.original_relative}</div>
              <div className="break-all font-medium">→ {item.proposed_relative}</div>
              <Companions item={item} />
            </div>
            {reviewing ? <Button variant="ghost" className="h-8 text-xs" aria-label={`Ret ${item.original_relative}`} onClick={() => onReopen(item.id)}>Ret</Button> : null}
          </li>
        ))}
      </ul>
      {reviewing && skipped.length > 0 ? <Skipped items={skipped} onReopen={onReopen} /> : null}
    </Card>
  )
}

function Skipped({ items, onReopen }: { items: APIAdoptionItem[]; onReopen: (itemId: string) => void }) {
  const [visible, setVisible] = useState(pageSize)
  return (
    <details className="border-t border-line px-5 py-3 text-sm">
      <summary className="cursor-pointer">Sprunget over ({items.length})</summary>
      <ul className="mt-2 space-y-2">
        {items.slice(0, visible).map((item) => (
          <li key={item.id} className="flex items-center gap-3">
            <span className="min-w-0 flex-1 break-all text-text-3">{item.original_relative}</span>
            <Button variant="ghost" className="h-8 text-xs" aria-label={`Ret ${item.original_relative}`} onClick={() => onReopen(item.id)}>Ret</Button>
          </li>
        ))}
      </ul>
      {items.length > visible ? <Button className="mt-2 h-8 text-xs" onClick={() => setVisible((count) => count + pageSize)}>Vis flere</Button> : null}
    </details>
  )
}

/** Step 4: the only step that changes files, behind the server's exact phrase. */
function Execute({ batch }: { batch: APIAdoptionBatch }) {
  const toast = useToast()
  const refresh = useRefreshAdoption(batch.id)
  const [phrase, setPhrase] = useState("")
  const run = useMutation({ mutationFn: () => executeAdoptionBatch(batch.id, phrase), onSuccess: refresh, onError: (error) => toast("bad", errorMessage(error)) })
  return (
    <Card>
      <CardHeader title="4. Overtag filerne" description="Nu bliver filerne flyttet eller omdøbt som vist ovenfor." />
      <div className="space-y-3 p-5 text-sm">
        <p>Hver fil kontrolleres, før den flyttes. Går noget galt undervejs, forsøger Media Manager at rulle ændringerne tilbage, og resultatet vises her. Plex skal bekræfte hver fil, før den tæller som overtaget.</p>
        <label className="block">Skriv <code className="font-mono">{adoptionPhrase}</code> for at fortsætte
          <input aria-label="Bekræftelse" value={phrase} onChange={(event) => setPhrase(event.target.value)} autoComplete="off" className={fieldClass} />
        </label>
        <Button variant="danger" busy={run.isPending} disabled={phrase !== adoptionPhrase} onClick={() => run.mutate()}>Overtag nu</Button>
      </div>
    </Card>
  )
}

function Progress({ batch }: { batch: APIAdoptionBatch }) {
  const items = (batch.items ?? []).filter((item) => item.review_state === "approved")
  return (
    <Card>
      <CardHeader title="Overtagelsen" description={batch.state === "executing" ? "Siden opdaterer sig selv." : undefined} />
      <ul className="divide-y divide-line">
        {items.map((item) => (
          <li key={item.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
            <span className="min-w-0 flex-1 break-all">{item.proposed_relative}</span>
            <Badge>{itemStates[item.execution_state] ?? humanize(item.execution_state)}</Badge>
          </li>
        ))}
      </ul>
    </Card>
  )
}
