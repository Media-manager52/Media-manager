import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Trash2 } from "lucide-react"
import { useState } from "react"
import { Link, useParams } from "react-router"

import {
  applyQualityProfilePolicy, ApiError, deleteLibraryRoot, deleteQualityProfile, deleteSeerrRoute, loadIntegrations, loadLibraryRoots, loadMediaStatus, loadNotificationDestinations, loadQualityProfiles, loadSeerrRouting, saveSeerrRoute,
  setNotificationDestinationEnabled, updateLibraryRoot,
  type APIIntegration, type APILibraryRoot, type APIQualityProfile,
} from "@/api/client"
import { AdoptionGuide } from "@/components/adoption-guide"
import { OptionPicker } from "@/components/option-picker"
import { AddNotificationDialog, AddProfileDialog, AddRootDialog, ConnectionsSection, DeleteSettingDialog, EditProfileDialog, fieldClass } from "@/components/setup-forms"
import { useToast } from "@/components/toast"
import { UsenetServersSection } from "@/components/usenet-servers"
import { Badge, Button, Card, CardHeader, Dialog, Empty, ErrorBox, PageHeader, PillNav } from "@/components/ui"
import { formatNumber, formatRelative, pluralize } from "@/format"
import { errorMessage, kindLabel, languageName } from "@/i18n"
import { profileOptions, rootOptions } from "@/lib/pickers"
import { audioLabel, resolutionLabel, type RuleRecord } from "@/lib/quality-profile-rules"
import { ListSkeleton } from "@/pages/overview"

const sections = [
  { id: "profiler", label: "Kvalitetsprofiler" },
  { id: "biblioteker", label: "Biblioteker" },
  { id: "seerr", label: "Seerr-regler" },
  { id: "notifikationer", label: "Notifikationer" },
  { id: "forbindelser", label: "Forbindelser" },
  { id: "usenet-servere", label: "Usenet-servere" },
  { id: "overtag", label: "Overtag bibliotek" },
]

export function SettingsPage() {
  const { section = "profiler" } = useParams()
  return (
    <>
      <PageHeader title="Indstillinger" description="Hvordan Media Manager vælger udgivelser, og hvor filerne havner." />
      <PillNav label="Indstillinger" items={sections.map((entry) => ({ to: `/indstillinger/${entry.id}`, label: entry.label }))} />
      {section === "profiler" ? <Profiles /> : null}
      {section === "biblioteker" ? <Roots /> : null}
      {section === "seerr" ? <SeerrRoutes /> : null}
      {section === "notifikationer" ? <Notifications /> : null}
      {section === "forbindelser" ? <ConnectionsSection /> : null}
      {section === "usenet-servere" ? <UsenetServersSection /> : null}
      {section === "overtag" ? <AdoptionGuide /> : null}
    </>
  )
}

function danishRuleLabel(label: string): string {
  return label
    .replace("Best available", "Bedst mulige")
    .replace("prefer best available", "ellers bedst mulige")
    .replace("Require", "Kræver")
    .replace("Custom preferences", "Egne præferencer")
    .replace(/^None$/, "Ingen krav")
}

function languages(values: unknown): string {
  return Array.isArray(values) && values.length ? values.map((value) => languageName(String(value))).join(", ") : ""
}

function Profiles() {
  const query = useQuery({ queryKey: ["settings", "profiles"], queryFn: ({ signal }) => loadQualityProfiles(signal) })
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<APIQualityProfile>()
  const [copying, setCopying] = useState<APIQualityProfile>()
  const [applying, setApplying] = useState<APIQualityProfile>()
  const [deleting, setDeleting] = useState<APIQualityProfile>()
  const queryClient = useQueryClient()
  const toast = useToast()
  const [replacement, setReplacement] = useState("")
  const remove = useMutation({
    mutationFn: ({ profile, replaceWith }: { profile: APIQualityProfile; replaceWith?: string }) => deleteQualityProfile(profile.id, replaceWith),
    onSuccess: async (result, { profile }) => {
      const moved = result?.moved
      toast("good", moved?.replaced_by ? `Profilen ${profile.name} er slettet. ${pluralize(moved.moved_titles, "titel", "titler")} bruger nu ${moved.replaced_by}.` : `Profilen ${profile.name} er slettet.`)
      setDeleting(undefined)
      await queryClient.invalidateQueries({ queryKey: ["settings", "profiles"] })
    },
  })
  if (query.error) return <ErrorBox error={query.error} retry={() => query.refetch()} />
  if (query.isPending) return <Card><ListSkeleton /></Card>
  const groups: Array<[string, APIQualityProfile[]]> = [["Film", query.data.filter((p) => p.media_kind === "movie")], ["Serier", query.data.filter((p) => p.media_kind === "series")]]
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center gap-3">
        <p className="min-w-0 flex-1 text-sm text-text-2">En profil afgør, hvilke udgivelser der er gode nok. Krav er faste; præferencer bruges kun til at vælge den bedste blandt de egnede.</p>
        <Button onClick={() => setAdding(true)}>Ny profil</Button>
      </div>
      {adding ? <AddProfileDialog onClose={() => setAdding(false)} /> : null}
      {editing ? <EditProfileDialog key={editing.id} profile={editing} onClose={() => setEditing(undefined)} /> : null}
      {copying ? <AddProfileDialog key={copying.id} copyFrom={copying} onClose={() => setCopying(undefined)} /> : null}
      {applying ? <ApplyProfileDialog key={applying.id} profile={applying} onClose={() => setApplying(undefined)} /> : null}
      {deleting ? (() => {
        // The server says the profile is in use; the owner may then name the profile that takes over.
        const inUse = remove.error instanceof ApiError && (remove.error.code === "quality_profile_in_use" || remove.error.code === "quality_profile_replacement_invalid")
        const candidates = query.data.filter((p) => p.media_kind === deleting.media_kind && p.enabled && p.id !== deleting.id).sort((a, b) => a.name.localeCompare(b.name, "da"))
        const chosen = candidates.find((p) => p.id === replacement)
        return (
          <Dialog
            open
            onClose={() => { if (!remove.isPending) setDeleting(undefined) }}
            title={`Slet profilen ${deleting.name}?`}
            footer={<><Button variant="ghost" disabled={remove.isPending} onClick={() => setDeleting(undefined)}>Fortryd</Button>{inUse
              ? <Button variant="danger" busy={remove.isPending} disabled={!chosen} onClick={() => remove.mutate({ profile: deleting, replaceWith: replacement })}>Flyt titler og slet</Button>
              : <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate({ profile: deleting })}>Slet profil</Button>}</>}
          >
            <div className="space-y-3">
              {!inUse ? <p>Profilen fjernes helt. Bruger titler eller en Seerr-regel den, bliver du først spurgt, hvilken profil de skal flyttes til.</p> : null}
              {remove.error && !inUse ? <p role="alert" className="text-sm text-bad">{errorMessage(remove.error)}</p> : null}
              {inUse ? (
                <>
                  <p role="alert">Profilen bruges af titler, filer eller en Seerr-regel. Vælg den profil, de skal flyttes til, før profilen slettes.</p>
                  {candidates.length === 0 ? <p className="text-sm text-bad">Der er ingen anden profil for {deleting.media_kind === "movie" ? "film" : "serier"}, der er slået til. Opret eller slå en til først.</p> : (
                    <label className="block text-sm font-medium">Flyt til
                      <select aria-label="Flyt til profil" value={replacement} onChange={(event) => setReplacement(event.target.value)} className={fieldClass}>
                        <option value="">Vælg profil</option>
                        {candidates.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                      </select>
                    </label>
                  )}
                  {chosen ? <p className="rounded-lg border border-line bg-surface-2 px-3 py-2 text-sm"><strong>Kravene ændrer sig.</strong> Titlerne skal fremover opfylde kravene i {chosen.name} (sprog, opløsning og lyd) i stedet for {deleting.name}. Filer, der allerede ligger i biblioteket, bliver liggende. Titler, der stadig venter, søger først igen, når du trykker &quot;Brug på ventende&quot; på {chosen.name}.</p> : null}
                  {remove.error instanceof ApiError && remove.error.code === "quality_profile_replacement_invalid" ? <p className="text-sm text-bad">{errorMessage(remove.error)}</p> : null}
                </>
              ) : null}
            </div>
          </Dialog>
        )
      })() : null}
      {groups.map(([title, profiles]) => (
        <Card key={title}>
          <CardHeader title={title} />
          <ul className="divide-y divide-line">
            {profiles.sort((a, b) => a.name.localeCompare(b.name, "da")).map((profile) => {
              const policy = profile.policy as { rules?: RuleRecord[]; required_subtitle_languages?: string[]; required_audio_languages?: string[]; preferred_subtitle_languages?: string[] }
              const rules = policy.rules ?? []
              const subs = languages(policy.required_subtitle_languages)
              const audio = languages(policy.required_audio_languages)
              const preferredSubs = languages(policy.preferred_subtitle_languages)
              return (
                <li key={profile.id} className="px-5 py-4">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{profile.name}</span>
                    {profile.is_default ? <Badge tone="info">Standard</Badge> : null}
                    {!profile.enabled ? <Badge>Slået fra</Badge> : null}
                    {profile.upgrades_enabled ? <Badge tone="good">Opgraderer</Badge> : null}
                    <div className="ml-auto flex flex-wrap gap-2">
                      <Button aria-label={`Rediger ${profile.name}`} className="h-8 text-xs" onClick={() => setEditing(profile)}>Rediger</Button>
                      <Button aria-label={`Kopier ${profile.name}`} variant="ghost" className="h-8 text-xs" onClick={() => setCopying(profile)}>Kopier</Button>
                      <Button aria-label={`Brug ${profile.name} på ventende opgaver`} variant="ghost" className="h-8 text-xs" onClick={() => setApplying(profile)}>Brug på ventende</Button>
                      {!profile.is_default ? <Button aria-label={`Slet ${profile.name}`} variant="ghost" className="h-8 text-xs" onClick={() => { remove.reset(); setReplacement(""); setDeleting(profile) }}>Slet</Button> :<span aria-hidden className="invisible inline-flex h-8 items-center px-3.5 text-xs font-medium">Slet</span>}
                    </div>
                  </div>
                  <dl className="mt-2 grid gap-x-6 gap-y-1 text-sm sm:grid-cols-2">
                    <div><dt className="inline text-text-3">Opløsning: </dt><dd className="inline">{danishRuleLabel(resolutionLabel(rules))}</dd></div>
                    <div><dt className="inline text-text-3">Lyd: </dt><dd className="inline">{danishRuleLabel(audioLabel(rules))}</dd></div>
                    <div><dt className="inline text-text-3">Kræver tekst eller tale: </dt><dd className="inline">{subs || "Ingen"}</dd></div>
                    <div><dt className="inline text-text-3">Krævet tale: </dt><dd className="inline">{audio || "Ingen"}</dd></div>
                    {preferredSubs ? <div><dt className="inline text-text-3">Foretrukne undertekster: </dt><dd className="inline">{preferredSubs}</dd></div> : null}
                  </dl>
                </li>
              )
            })}
          </ul>
        </Card>
      ))}
    </div>
  )
}

/** Moves waiting work onto the profile's current rules, only when asked. */
function ApplyProfileDialog({ profile, onClose }: { profile: APIQualityProfile; onClose: () => void }) {
  const mutation = useMutation({ mutationFn: () => applyQualityProfilePolicy(profile.id) })
  const result = mutation.data
  return (
    <Dialog open onClose={onClose} title={`Brug ${profile.name} på ventende opgaver?`} footer={result ? <Button variant="primary" onClick={onClose}>Luk</Button> : <><Button variant="ghost" disabled={mutation.isPending} onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} onClick={() => mutation.mutate()}>Brug på ventende</Button></>}>
      <div className="space-y-3">
        {result ? (
          <p role="status">{formatNumber(result.applied)} {result.applied === 1 ? "opgave bruger" : "opgaver bruger"} nu de nye regler. {result.skipped > 0 ? `${formatNumber(result.skipped)} blev sprunget over, fordi de er i gang med at hente eller udgive, eller kræver afklaring.` : "Ingen blev sprunget over."}</p>
        ) : (
          <>
            <p>Opgaver, der venter på denne profil, skifter til profilens nuværende regler. Det kan betyde nye søgninger.</p>
            <p>Opgaver, der er i gang med at hente eller udgive, bliver ikke ændret. Filer, du allerede har, bliver ikke rørt.</p>
          </>
        )}
        {mutation.error ? <p role="alert" className="text-sm text-bad">{errorMessage(mutation.error)}</p> : null}
      </div>
    </Dialog>
  )
}

function Roots() {
  const queryClient = useQueryClient()
  const toast = useToast()
  const query = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const status = useQuery({ queryKey: ["media", "status"], queryFn: ({ signal }) => loadMediaStatus(signal), staleTime: 60_000 })
  const [adding, setAdding] = useState(false)
  const [deleting, setDeleting] = useState<APILibraryRoot>()
  const [renaming, setRenaming] = useState<APILibraryRoot>()
  const [newName, setNewName] = useState("")
  const mutation = useMutation({
    mutationFn: ({ root, patch }: { root: APILibraryRoot; patch: { is_default?: boolean; enabled?: boolean } }) => updateLibraryRoot(root.id, patch),
    onSuccess: async (_, { root }) => {
      toast("good", `${root.name} er opdateret.`)
      await queryClient.invalidateQueries({ queryKey: ["settings", "roots"] })
    },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  const rename = useMutation({
    mutationFn: (root: APILibraryRoot) => updateLibraryRoot(root.id, { name: newName.trim() }),
    onSuccess: async () => {
      toast("good", `Mappen hedder nu ${newName.trim()}.`)
      setRenaming(undefined)
      await queryClient.invalidateQueries({ queryKey: ["settings"] })
    },
  })
  if (query.error) return <ErrorBox error={query.error} retry={() => query.refetch()} />
  if (query.isPending) return <Card><ListSkeleton /></Card>
  const sorted = [...query.data].sort((a, b) => a.media_kind.localeCompare(b.media_kind) || a.name.localeCompare(b.name, "da"))
  const titleCounts = new Map<string, number>()
  for (const entry of status.data ?? []) for (const id of entry.root_ids ?? []) titleCounts.set(id, (titleCounts.get(id) ?? 0) + 1)
  const groups = (["movie", "series"] as const).map((kind) => ({ kind, roots: sorted.filter((root) => root.media_kind === kind) })).filter((group) => group.roots.length > 0)
  return (
    <Card>
      <CardHeader title="Biblioteksmapper" description="Hver mappe svarer til et bibliotek i Plex. Standardmappen bruges, når hverken du eller en Seerr-regel vælger en anden. En 4K-mappe holder en ekstra 4K-udgave og bruges kun, når 4K bestilles." action={<Button onClick={() => setAdding(true)}>Tilføj mappe</Button>} />
      {adding ? <AddRootDialog onClose={() => setAdding(false)} /> : null}
      {deleting ? (
        <DeleteSettingDialog title={`Slet mappen ${deleting.name}?`} confirm="Slet mappe" done={`${deleting.name} er slettet.`} action={() => deleteLibraryRoot(deleting.id)} onClose={() => setDeleting(undefined)}>
          <p>Mappen fjernes kun fra Media Manager. Selve mappen på disken og Plex-biblioteket bliver ikke rørt. Det kan kun lade sig gøre, når mappen aldrig har haft titler, og ingen Seerr-regel peger på den.</p>
        </DeleteSettingDialog>
      ) : null}
      {renaming ? (
        <Dialog open onClose={() => { if (!rename.isPending) setRenaming(undefined) }} title={`Omdøb ${renaming.name}`} footer={<><Button variant="ghost" disabled={rename.isPending} onClick={() => setRenaming(undefined)}>Fortryd</Button><Button variant="primary" busy={rename.isPending} disabled={!newName.trim() || newName.trim() === renaming.name} onClick={() => rename.mutate(renaming)}>Gem navn</Button></>}>
          <div className="space-y-3">
            <label className="block text-sm font-medium">Navn
              <input aria-label="Mappens navn" value={newName} maxLength={120} onChange={(event) => setNewName(event.target.value)} className={fieldClass} />
            </label>
            <p className="text-sm text-text-2">Kun navnet her på siden ændres. Mapper, filnavne og Plex-biblioteket er de samme som før.</p>
            {rename.error ? <p role="alert" className="text-sm text-bad">{errorMessage(rename.error)}</p> : null}
          </div>
        </Dialog>
      ) : null}
      {sorted.length === 0 ? <Empty title="Ingen biblioteksmapper endnu">Tilføj en mappe for hvert Plex-bibliotek, Media Manager skal lægge film eller serier i.</Empty> : null}
      {groups.map((group) => (
        <div key={group.kind}>
          <h3 className="border-b border-line bg-surface-2 px-5 py-2 text-xs font-semibold uppercase tracking-wide text-text-3">{group.kind === "movie" ? "Film" : "Serier"} <span className="font-normal">· {formatNumber(group.roots.length)} mapper</span></h3>
      <ul className="divide-y divide-line">
        {group.roots.map((root) => (
          <li key={root.id} className="flex flex-col gap-2 px-5 py-3 sm:flex-row sm:flex-wrap sm:items-center sm:gap-3">
            <div className="min-w-0 sm:flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{root.name}</span>
                {root.edition === "4k" ? <Badge tone="info">4K</Badge> : null}
                {root.is_default ? <Badge tone="info">Standard</Badge> : null}
                {!root.enabled ? <Badge>Slået fra</Badge> : null}
              </div>
              <div className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-text-3">
                <span className="break-all">{root.plex_visible_root || root.destination_root}</span>
                {status.data ? <Link to={`/bibliotek?mappe=${encodeURIComponent(root.id)}`} className="text-text-2 hover:text-text hover:underline">{pluralize(titleCounts.get(root.id) ?? 0, "titel", "titler")}</Link> : null}
              </div>
            </div>
            <div className="flex flex-wrap gap-2">
              {!root.is_default && root.enabled && root.edition !== "4k" ? <Button className="h-8 text-xs" busy={mutation.isPending} onClick={() => mutation.mutate({ root, patch: { is_default: true } })}>Gør til standard</Button> : null}
              {!root.is_default ? <Button variant="ghost" className="h-8 text-xs" busy={mutation.isPending} onClick={() => mutation.mutate({ root, patch: { enabled: !root.enabled } })}>{root.enabled ? "Slå fra" : "Slå til"}</Button> :<span aria-hidden className="invisible inline-flex h-8 items-center px-3.5 text-xs font-medium">Slå fra</span>}
              <Button variant="ghost" className="h-8 text-xs" aria-label={`Omdøb ${root.name}`} onClick={() => { rename.reset(); setNewName(root.name); setRenaming(root) }}>Omdøb</Button>
              {!root.is_default ? <Button variant="ghost" className="h-8 text-xs" aria-label={`Slet ${root.name}`} onClick={() => setDeleting(root)}>Slet</Button> :<span aria-hidden className="invisible inline-flex h-8 items-center px-3.5 text-xs font-medium">Slet</span>}
            </div>
          </li>
        ))}
      </ul>
        </div>
      ))}
    </Card>
  )
}

function SeerrRoutes() {
  const integrations = useQuery({ queryKey: ["settings", "integrations"], queryFn: ({ signal }) => loadIntegrations(signal) })
  const seerr = (integrations.data ?? []).filter((item) => item.kind === "seerr" && item.enabled)
  if (integrations.error) return <ErrorBox error={integrations.error} />
  if (integrations.isPending) return <Card><ListSkeleton /></Card>
  if (seerr.length === 0) return <Card><Empty title="Ingen Seerr-forbindelse er slået til" /></Card>
  return <div className="flex flex-col gap-6">{seerr.map((instance) => <SeerrInstance key={instance.id} instance={instance} />)}</div>
}

function SeerrInstance({ instance }: { instance: APIIntegration }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const routing = useQuery({ queryKey: ["seerr-routes", instance.id], queryFn: () => loadSeerrRouting(instance.id) })
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const profiles = useQuery({ queryKey: ["settings", "profiles"], queryFn: ({ signal }) => loadQualityProfiles(signal) })
  const [draft, setDraft] = useState<{ folder: string; kind: "movie" | "series"; root: string; profile: string } | null>(null)
  const [removing, setRemoving] = useState<{ id: string; folder: string } | null>(null)
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["seerr-routes", instance.id] })
  const save = useMutation({
    mutationFn: () => saveSeerrRoute(instance.id, { media_kind: draft!.kind, root_folder: draft!.folder, library_root_id: draft!.root, ...(draft!.profile ? { quality_profile_id: draft!.profile } : {}) }),
    onSuccess: async () => { toast("good", "Reglen er gemt."); setDraft(null); await refresh() },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  const remove = useMutation({
    mutationFn: (routeId: string) => deleteSeerrRoute(instance.id, routeId),
    onSuccess: async () => { toast("good", "Reglen er slettet."); setRemoving(null); await refresh() },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  const rootName = (id: string) => roots.data?.find((root) => root.id === id)?.name ?? "Ukendt mappe"
  const profileName = (id?: string) => (id ? profiles.data?.find((profile) => profile.id === id)?.name ?? "Ukendt profil" : "Mappens standardprofil")
  const unrouted = (routing.data?.observed_folders ?? []).filter((folder) => !folder.route_id)
  return (
    <Card>
      <CardHeader title={`Seerr-regler · ${instance.name}`} description="Når en anmodning i Seerr peger på en mappe, bestemmer reglen, hvilket bibliotek og hvilken profil Media Manager bruger." />
      {routing.error ? <div className="p-4"><ErrorBox error={routing.error} /></div> : null}
      {routing.isPending ? <ListSkeleton /> : null}
      <ul className="divide-y divide-line">
        {[...(routing.data?.routes ?? [])].sort((a, b) => a.root_folder.localeCompare(b.root_folder, "da")).map((route) => (
          <li key={route.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
            <div className="min-w-0 flex-1">
              <div className="break-all font-medium">{route.root_folder}</div>
              <div className="text-text-2">{kindLabel(route.media_kind)} → {rootName(route.library_root_id)} · {profileName(route.quality_profile_id)}</div>
            </div>
            <Button variant="ghost" className="h-8" aria-label={`Slet reglen for ${route.root_folder}`} onClick={() => setRemoving({ id: route.id, folder: route.root_folder })}><Trash2 aria-hidden className="size-4" /></Button>
          </li>
        ))}
      </ul>
      {unrouted.length > 0 ? (
        <div className="border-t border-line px-5 py-4">
          <div className="mb-2 text-sm font-medium">Mapper uden regel</div>
          <ul className="space-y-2">
            {unrouted.map((folder) => (
              <li key={`${folder.media_kind}-${folder.root_folder}`} className="flex flex-wrap items-center gap-2 text-sm">
                <span className="break-all">{folder.root_folder}</span>
                <span className="text-text-3">{pluralize(folder.request_count, "anmodning", "anmodninger")} · senest {formatRelative(folder.latest_at)}</span>
                <Button className="ml-auto h-8 text-xs" onClick={() => setDraft({ folder: folder.root_folder, kind: folder.media_kind, root: "", profile: "" })}>Lav regel</Button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <Dialog
        open={removing !== null}
        onClose={() => setRemoving(null)}
        title="Slet Seerr-regel?"
        footer={<><Button variant="ghost" onClick={() => setRemoving(null)}>Fortryd</Button><Button variant="danger" busy={remove.isPending} onClick={() => removing && remove.mutate(removing.id)}>Slet regel</Button></>}
      >
        Nye anmodninger til <span className="font-medium text-text">{removing?.folder}</span> bruger derefter standardbiblioteket. Allerede hentede titler flyttes ikke.
      </Dialog>
      {draft ? (
        <form onSubmit={(event) => { event.preventDefault(); save.mutate() }} className="grid grid-cols-1 gap-3 border-t border-line px-5 py-4 text-sm sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end">
          <div className="sm:col-span-3 font-medium break-all">{draft.folder}</div>
          <OptionPicker
            label="Bibliotek"
            required
            placeholder="Vælg bibliotek"
            value={draft.root}
            onChange={(root) => setDraft({ ...draft, root })}
            options={rootOptions(roots.data ?? [], draft.kind)}
          />
          <OptionPicker
            label="Kvalitetsprofil"
            value={draft.profile}
            onChange={(profile) => setDraft({ ...draft, profile })}
            options={profileOptions(profiles.data ?? [], draft.kind, { label: "Mappens standardprofil", description: "Profilen, bibliotekets nye titler ellers får." })}
          />
          <div className="flex gap-2">
            <Button variant="ghost" onClick={() => setDraft(null)}>Fortryd</Button>
            <Button type="submit" variant="primary" busy={save.isPending} disabled={!draft.root}>Gem</Button>
          </div>
        </form>
      ) : null}
    </Card>
  )
}

function Notifications() {
  const queryClient = useQueryClient()
  const toast = useToast()
  const query = useQuery({ queryKey: ["settings", "notifications"], queryFn: ({ signal }) => loadNotificationDestinations(signal) })
  const [adding, setAdding] = useState(false)
  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => setNotificationDestinationEnabled(id, enabled),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ["settings", "notifications"] }) },
    onError: (error) => toast("bad", errorMessage(error)),
  })
  if (query.error) return <ErrorBox error={query.error} />
  if (query.isPending) return <Card><ListSkeleton /></Card>
  return (
    <Card>
      <CardHeader title="Notifikationer" description="Steder, Media Manager sender besked til, når noget sker." action={<Button onClick={() => setAdding(true)}>Tilføj</Button>} />
      {adding ? <AddNotificationDialog onClose={() => setAdding(false)} /> : null}
      {query.data.length === 0 ? <Empty title="Ingen notifikationer sat op">Media Manager sender ingen beskeder lige nu.</Empty> : null}
      <ul className="divide-y divide-line">
        {query.data.map((destination) => (
          <li key={destination.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2"><span className="font-medium">{destination.name}</span>{!destination.enabled ? <Badge>Slået fra</Badge> : null}</div>
              <div className="text-text-3">{pluralize(destination.events.length, "hændelse", "hændelser")}</div>
            </div>
            <Button className="h-8 text-xs" busy={toggle.isPending} onClick={() => toggle.mutate({ id: destination.id, enabled: !destination.enabled })}>{destination.enabled ? "Slå fra" : "Slå til"}</Button>
          </li>
        ))}
      </ul>
    </Card>
  )
}
