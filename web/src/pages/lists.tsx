import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ExternalLink } from "lucide-react"
import { useState } from "react"
import { Link } from "react-router"

import { ApiError, createMDBList, deleteImportList, loadImportListEntries, loadImportLists, loadLibraryRoots, loadQualityProfiles, syncImportList, updateImportList, type APIImportList, type MDBListInput } from "@/api/client"
import { OptionPicker } from "@/components/option-picker"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, Dialog, Empty, ErrorBox, PageHeader } from "@/components/ui"
import { formatNumber, formatRelative } from "@/format"
import { errorMessage, listEntryDetail, listEntryLabel, listEntryTone, listSyncDetail } from "@/i18n"
import { titleHref } from "@/lib/links"
import { profileOptions, rootOptions } from "@/lib/pickers"
import { ListSkeleton } from "@/pages/overview"

export function ListsPage() {
  const query = useQuery({ queryKey: ["import-lists"], queryFn: ({ signal }) => loadImportLists(signal), refetchInterval: 30_000 })
  const lists = (query.data ?? []).filter((list) => list.provider === "mdblist").sort((a, b) => Number(b.enabled) - Number(a.enabled) || a.name.localeCompare(b.name, "da"))
  const [adding, setAdding] = useState(false)
  return (
    <>
      <PageHeader
        title="Lister"
        description="Følg offentlige lister fra MDBList, som i Radarr og Sonarr. Nye titler på en liste bliver tilføjet og søgt efter af sig selv, serier med alle sæsoner. Titler, du allerede har, springes over, og titler, du selv har fjernet, kommer ikke igen. Forsvinder en titel fra listen, bliver den i biblioteket. Listerne læses hver 6. time."
        actions={adding ? null : <Button variant="primary" onClick={() => setAdding(true)}>Tilføj liste</Button>}
      />
      {adding ? <ListForm onDone={() => setAdding(false)} /> : null}
      {query.error ? <ErrorBox error={query.error} retry={() => query.refetch()} /> : null}
      {query.isPending ? <Card><ListSkeleton rows={3} /></Card> : null}
      {!query.isPending && !query.error && lists.length === 0 && !adding ? (
        <Card><Empty title="Ingen lister endnu">Find en liste på mdblist.com, kopiér adressen fra browseren, og tryk "Tilføj liste".</Empty></Card>
      ) : null}
      <div className="space-y-4">
        {lists.map((list) => <ListCard key={list.id} list={list} />)}
      </div>
    </>
  )
}

function ListCard({ list }: { list: APIImportList }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [showTitles, setShowTitles] = useState(false)
  const counts = list.entries ?? { total: 0, pending: 0, added: 0, in_library: 0, removed_earlier: 0, failed: 0 }
  const sync = useMutation({
    mutationFn: () => syncImportList(list.id),
    onSuccess: async (result) => {
      toast("good", result.already_waiting ? `${list.name} bliver allerede læst.` : `${list.name} bliver læst nu.`)
      await queryClient.invalidateQueries({ queryKey: ["import-lists"] })
    },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  const remove = useMutation({
    mutationFn: () => deleteImportList(list.id),
    onSuccess: async () => {
      toast("good", `${list.name} følges ikke længere. Titlerne er stadig i biblioteket.`)
      setDeleting(false)
      await queryClient.invalidateQueries({ queryKey: ["import-lists"] })
    },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  const status = !list.enabled
    ? <Badge>Slået fra</Badge>
    : list.last_sync_state === "failed" || list.last_sync_state === "needs_configuration"
      ? <Badge tone="warn">Kunne ikke læses</Badge>
      : list.last_synced_at ? <Badge tone="good">Følges</Badge> : <Badge tone="info">Læses snart</Badge>
  const folder = (root: string | undefined, profile: string | undefined) => `${root ?? "Standardmappen"} · ${profile ?? "standardprofilen"}`
  const problem = list.enabled ? listSyncDetail(list.last_sync_detail) : ""
  const tally = [
    counts.added ? `${formatNumber(counts.added)} tilføjet` : "",
    counts.in_library ? `${formatNumber(counts.in_library)} fandtes allerede` : "",
    counts.pending ? `${formatNumber(counts.pending)} venter` : "",
    counts.failed ? `${formatNumber(counts.failed)} prøves igen` : "",
    counts.removed_earlier ? `${formatNumber(counts.removed_earlier)} fjernet af dig før` : "",
  ].filter(Boolean).join(" · ")
  return (
    <Card>
      <div className="flex flex-wrap items-start justify-between gap-3 px-5 pt-4">
        <div className="min-w-0">
          <h2 className="font-semibold break-words">{list.name}</h2>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            {status}
            {list.source_url ? <a href={list.source_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-xs text-text-3 hover:text-text">MDBList <ExternalLink aria-hidden className="size-3.5" /></a> : null}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {list.enabled ? <Button variant="ghost" className="h-8 text-xs" busy={sync.isPending} onClick={() => sync.mutate()}>Læs nu</Button> : null}
          <Button variant="ghost" className="h-8 text-xs" onClick={() => setEditing((value) => !value)}>Rediger</Button>
          <Button variant="ghost" className="h-8 text-xs" aria-label={`Slet ${list.name}`} onClick={() => setDeleting(true)}>Slet</Button>
        </div>
      </div>
      <div className="space-y-2 px-5 py-4 text-sm">
        <p className="text-text-2">{counts.total ? `${formatNumber(counts.total)} titler på listen${tally ? `: ${tally}` : ""}.` : "Listen er ikke læst endnu."}</p>
        {problem ? <p role="alert" className="text-warn">{problem}</p> : null}
        <div className="grid gap-x-4 gap-y-1 text-xs text-text-3 sm:grid-cols-2">
          <span>Film: {folder(list.movie_root_name, list.movie_quality_profile)}</span>
          <span>Serier: {folder(list.series_root_name, list.series_quality_profile)}</span>
          {list.last_synced_at ? <span>Læst {formatRelative(list.last_synced_at)}</span> : null}
          {list.enabled && list.next_sync_at ? <span>Næste læsning {isFuture(list.next_sync_at) ? formatRelative(list.next_sync_at) : "snart"}</span> : null}
        </div>
        {counts.total ? <Button variant="ghost" className="h-8 px-0 text-xs" aria-expanded={showTitles} onClick={() => setShowTitles((value) => !value)}>{showTitles ? "Skjul titler" : "Vis titler"}</Button> : null}
      </div>
      {showTitles ? <ListTitles list={list} /> : null}
      {editing ? <ListForm list={list} onDone={() => setEditing(false)} /> : null}
      <Dialog
        open={deleting}
        onClose={() => setDeleting(false)}
        title={`Stop med at følge ${list.name}?`}
        footer={<><Button variant="ghost" onClick={() => setDeleting(false)}>Fortryd</Button><Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>Stop med at følge</Button></>}
      >
        <p>Listen bliver ikke læst mere. Alle titler, den har tilføjet, bliver i biblioteket præcis som nu.</p>
      </Dialog>
    </Card>
  )
}

function ListTitles({ list }: { list: APIImportList }) {
  const entries = useQuery({ queryKey: ["import-lists", list.id, "entries"], queryFn: ({ signal }) => loadImportListEntries(list.id, signal) })
  if (entries.error) return <div className="px-5 pb-4"><ErrorBox error={entries.error} retry={() => entries.refetch()} /></div>
  if (!entries.data) return <ListSkeleton rows={4} />
  return (
    <ul className="divide-y divide-line border-t border-line text-sm">
      {entries.data.map((entry) => (
        <li key={`${entry.media_kind}-${entry.tmdb_id}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-5 py-2">
          <span className="min-w-0 flex-1 break-words">
            {entry.media_id ? <Link to={titleHref(entry.media_id)} className="font-medium hover:underline">{entry.title}</Link> : <span className="font-medium">{entry.title}</span>}
            <span className="text-text-3">{entry.year ? ` (${entry.year})` : ""} · {entry.media_kind === "movie" ? "Film" : "Serie"}</span>
            {entry.state === "failed" && entry.detail ? <span className="block text-xs text-text-3">{listEntryDetail(entry.detail)}</span> : null}
          </span>
          <Badge tone={listEntryTone(entry.state)}>{listEntryLabel(entry.state)}</Badge>
        </li>
      ))}
    </ul>
  )
}

function createErrorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === "invalid_list_url") return "Adressen er ikke en liste på MDBList. Kopiér adressen fra listens side, f.eks. https://mdblist.com/lists/navn/liste."
    if (error.code === "import_list_exists") return "Du følger allerede en liste med den adresse eller det navn."
  }
  return errorMessage(error)
}

function ListForm({ list, onDone }: { list?: APIImportList; onDone: () => void }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const profiles = useQuery({ queryKey: ["settings", "profiles"], queryFn: ({ signal }) => loadQualityProfiles(signal) })
  const [url, setUrl] = useState("")
  const [values, setValues] = useState<MDBListInput>({
    name: list?.name ?? "",
    enabled: list?.enabled ?? true,
    movie_root_id: list?.movie_root_id ?? "",
    movie_quality_profile_id: list?.movie_quality_profile_id ?? "",
    series_root_id: list?.series_root_id ?? "",
    series_quality_profile_id: list?.series_quality_profile_id ?? "",
  })
  const save = useMutation({
    mutationFn: () => (list ? updateImportList(list.id, values) : createMDBList({ ...values, url })),
    onSuccess: async (saved) => {
      toast("good", list ? `${saved.name} er gemt.` : `${saved.name} følges nu. Listen bliver læst om lidt.`)
      onDone()
      await queryClient.invalidateQueries({ queryKey: ["import-lists"] })
    },
    onError: (error) => toast("bad", createErrorText(error)),
  })
  const field = "mt-1 h-9 w-full rounded-lg border border-line bg-surface px-3 text-text"
  const automatic = (what: string) => ({ label: "Standard", description: `Standard${what} for nye titler.` })
  return (
    <Card className={list ? "rounded-none border-x-0 border-b-0" : "mb-4"}>
      <form onSubmit={(event) => { event.preventDefault(); save.mutate() }} className="grid grid-cols-1 gap-4 px-5 py-4 text-sm sm:grid-cols-2">
        <h2 className="font-semibold sm:col-span-2">{list ? "Rediger liste" : "Følg en liste fra MDBList"}</h2>
        {list ? null : (
          <label className="block sm:col-span-2">Listens adresse
            <input required inputMode="url" autoComplete="off" placeholder="https://mdblist.com/lists/navn/liste" value={url} onChange={(event) => setUrl(event.target.value)} className={field} />
            <span className="mt-1 block text-xs text-text-3">Listen skal være offentlig. Der skal ikke bruges konto eller nøgle.</span>
          </label>
        )}
        <label className="block sm:col-span-2">Navn{list ? "" : " (valgfrit)"}
          <input value={values.name} required={Boolean(list)} placeholder={list ? undefined : "Listens navn fra adressen"} onChange={(event) => setValues({ ...values, name: event.target.value })} className={field} />
        </label>
        <OptionPicker label="Film: bibliotek" value={values.movie_root_id} onChange={(value) => setValues({ ...values, movie_root_id: value })} options={rootOptions(roots.data ?? [], "movie", automatic("mappen"))} />
        <OptionPicker label="Film: kvalitetsprofil" value={values.movie_quality_profile_id} onChange={(value) => setValues({ ...values, movie_quality_profile_id: value })} options={profileOptions(profiles.data ?? [], "movie", automatic("profilen"))} />
        <OptionPicker label="Serier: bibliotek" value={values.series_root_id} onChange={(value) => setValues({ ...values, series_root_id: value })} options={rootOptions(roots.data ?? [], "series", automatic("mappen"))} />
        <OptionPicker label="Serier: kvalitetsprofil" value={values.series_quality_profile_id} onChange={(value) => setValues({ ...values, series_quality_profile_id: value })} options={profileOptions(profiles.data ?? [], "series", automatic("profilen"))} />
        <p className="text-xs text-text-3 sm:col-span-2">Profilen bestemmer sprog- og kvalitetskravene, præcis som når du tilføjer en titel selv. Serier hentes med alle sæsoner. {list ? "Titler, der allerede er tilføjet, beholder deres mappe og profil." : ""}</p>
        <label className="flex items-center gap-2 sm:col-span-2">
          <input type="checkbox" checked={values.enabled} onChange={(event) => setValues({ ...values, enabled: event.target.checked })} className="size-4 accent-[var(--accent)]" />
          Følg listen (tilføj nye titler af sig selv)
        </label>
        <div className="flex gap-2 sm:col-span-2">
          <Button variant="ghost" onClick={onDone}>Fortryd</Button>
          <Button type="submit" variant="primary" busy={save.isPending} disabled={!list && !url.trim()}>{list ? "Gem" : "Følg liste"}</Button>
        </div>
      </form>
    </Card>
  )
}

function isFuture(value: string): boolean {
  return new Date(value).getTime() > Date.now()
}
