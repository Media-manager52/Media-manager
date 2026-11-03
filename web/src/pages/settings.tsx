import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Trash2 } from "lucide-react"
import { useState } from "react"
import { NavLink, useParams } from "react-router"

import {
  deleteSeerrRoute, loadIntegrations, loadLibraryRoots, loadNotificationDestinations, loadQualityProfiles, loadSeerrRouting, saveSeerrRoute,
  setNotificationDestinationEnabled, updateLibraryRoot,
  type APIIntegration, type APILibraryRoot, type APIQualityProfile,
} from "@/api/client"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, CardHeader, cx, Dialog, Empty, ErrorBox, PageHeader } from "@/components/ui"
import { formatRelative } from "@/format"
import { kindLabel, languageName } from "@/i18n"
import { audioLabel, resolutionLabel, type RuleRecord } from "@/lib/quality-profile-rules"
import { ListSkeleton } from "@/pages/overview"

const sections = [
  { id: "profiler", label: "Kvalitetsprofiler" },
  { id: "biblioteker", label: "Biblioteker" },
  { id: "seerr", label: "Seerr-regler" },
  { id: "notifikationer", label: "Notifikationer" },
]

export function SettingsPage() {
  const { section = "profiler" } = useParams()
  return (
    <>
      <PageHeader title="Indstillinger" description="Hvordan Media Manager vælger udgivelser, og hvor filerne havner." />
      <nav aria-label="Indstillinger" className="mb-6 flex gap-1 overflow-x-auto border-b border-line">
        {sections.map((entry) => (
          <NavLink key={entry.id} to={`/indstillinger/${entry.id}`} className={({ isActive }) => cx("-mb-px shrink-0 border-b-2 px-3 py-2 text-sm font-medium", isActive ? "border-accent text-text" : "border-transparent text-text-2 hover:text-text")}>
            {entry.label}
          </NavLink>
        ))}
      </nav>
      {section === "profiler" ? <Profiles /> : null}
      {section === "biblioteker" ? <Roots /> : null}
      {section === "seerr" ? <SeerrRoutes /> : null}
      {section === "notifikationer" ? <Notifications /> : null}
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
  if (query.error) return <ErrorBox error={query.error} retry={() => query.refetch()} />
  if (query.isPending) return <Card><ListSkeleton /></Card>
  const groups: Array<[string, APIQualityProfile[]]> = [["Film", query.data.filter((p) => p.media_kind === "movie")], ["Serier", query.data.filter((p) => p.media_kind === "series")]]
  return (
    <div className="flex flex-col gap-6">
      <p className="text-sm text-text-2">En profil afgør, hvilke udgivelser der er gode nok. Krav er faste; præferencer bruges kun til at vælge den bedste blandt de egnede.</p>
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
                  </div>
                  <dl className="mt-2 grid gap-x-6 gap-y-1 text-sm sm:grid-cols-2">
                    <div><dt className="inline text-text-3">Opløsning: </dt><dd className="inline">{danishRuleLabel(resolutionLabel(rules))}</dd></div>
                    <div><dt className="inline text-text-3">Lyd: </dt><dd className="inline">{danishRuleLabel(audioLabel(rules))}</dd></div>
                    <div><dt className="inline text-text-3">Krævede undertekster: </dt><dd className="inline">{subs || "Ingen"}</dd></div>
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

function Roots() {
  const queryClient = useQueryClient()
  const toast = useToast()
  const query = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const mutation = useMutation({
    mutationFn: ({ root, patch }: { root: APILibraryRoot; patch: { is_default?: boolean; enabled?: boolean } }) => updateLibraryRoot(root.id, patch),
    onSuccess: async (_, { root }) => {
      toast("good", `${root.name} er opdateret.`)
      await queryClient.invalidateQueries({ queryKey: ["settings", "roots"] })
    },
    onError: (error) => toast("bad", error instanceof Error ? error.message : String(error)),
  })
  if (query.error) return <ErrorBox error={query.error} retry={() => query.refetch()} />
  if (query.isPending) return <Card><ListSkeleton /></Card>
  const sorted = [...query.data].sort((a, b) => a.media_kind.localeCompare(b.media_kind) || a.name.localeCompare(b.name, "da"))
  return (
    <Card>
      <CardHeader title="Biblioteksmapper" description="Hver mappe svarer til et bibliotek i Plex. Standardmappen bruges, når hverken du eller en Seerr-regel vælger en anden." />
      <ul className="divide-y divide-line">
        {sorted.map((root) => (
          <li key={root.id} className="flex flex-wrap items-center gap-3 px-5 py-3">
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{root.name}</span>
                <Badge>{kindLabel(root.media_kind)}</Badge>
                {root.is_default ? <Badge tone="info">Standard</Badge> : null}
                {!root.enabled ? <Badge>Slået fra</Badge> : null}
              </div>
              <div className="mt-0.5 break-all text-xs text-text-3">{root.plex_visible_root || root.destination_root}</div>
            </div>
            <div className="flex gap-2">
              {!root.is_default && root.enabled ? <Button className="h-8 text-xs" busy={mutation.isPending} onClick={() => mutation.mutate({ root, patch: { is_default: true } })}>Gør til standard</Button> : null}
              {!root.is_default ? <Button variant="ghost" className="h-8 text-xs" busy={mutation.isPending} onClick={() => mutation.mutate({ root, patch: { enabled: !root.enabled } })}>{root.enabled ? "Slå fra" : "Slå til"}</Button> : null}
            </div>
          </li>
        ))}
      </ul>
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
    onError: (error) => toast("bad", error instanceof Error ? error.message : String(error)),
  })
  const remove = useMutation({
    mutationFn: (routeId: string) => deleteSeerrRoute(instance.id, routeId),
    onSuccess: async () => { toast("good", "Reglen er slettet."); setRemoving(null); await refresh() },
    onError: (error) => toast("bad", error instanceof Error ? error.message : String(error)),
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
        {(routing.data?.routes ?? []).sort((a, b) => a.root_folder.localeCompare(b.root_folder, "da")).map((route) => (
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
                <span className="text-text-3">{folder.request_count} anmodninger · senest {formatRelative(folder.latest_at)}</span>
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
        <form onSubmit={(event) => { event.preventDefault(); save.mutate() }} className="grid gap-3 border-t border-line px-5 py-4 text-sm sm:grid-cols-[1fr_1fr_auto]">
          <div className="sm:col-span-3 font-medium break-all">{draft.folder}</div>
          <select required value={draft.root} onChange={(event) => setDraft({ ...draft, root: event.target.value })} className="h-9 rounded-lg border border-line bg-surface px-2">
            <option value="">Vælg bibliotek</option>
            {(roots.data ?? []).filter((root) => root.media_kind === draft.kind && root.enabled).map((root) => <option key={root.id} value={root.id}>{root.name}</option>)}
          </select>
          <select value={draft.profile} onChange={(event) => setDraft({ ...draft, profile: event.target.value })} className="h-9 rounded-lg border border-line bg-surface px-2">
            <option value="">Mappens standardprofil</option>
            {(profiles.data ?? []).filter((profile) => profile.media_kind === draft.kind && profile.enabled).map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}
          </select>
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
  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => setNotificationDestinationEnabled(id, enabled),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ["settings", "notifications"] }) },
    onError: (error) => toast("bad", error instanceof Error ? error.message : String(error)),
  })
  if (query.error) return <ErrorBox error={query.error} />
  if (query.isPending) return <Card><ListSkeleton /></Card>
  return (
    <Card>
      <CardHeader title="Notifikationer" description="Steder, Media Manager sender besked til, når noget sker." />
      {query.data.length === 0 ? <Empty title="Ingen notifikationer sat op">Media Manager sender ingen beskeder lige nu.</Empty> : null}
      <ul className="divide-y divide-line">
        {query.data.map((destination) => (
          <li key={destination.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
            <div className="min-w-0 flex-1">
              <div className="font-medium">{destination.name}</div>
              <div className="text-text-3">{destination.events.length} hændelser</div>
            </div>
            <Button className="h-8 text-xs" busy={toggle.isPending} onClick={() => toggle.mutate({ id: destination.id, enabled: !destination.enabled })}>{destination.enabled ? "Slå fra" : "Slå til"}</Button>
          </li>
        ))}
      </ul>
    </Card>
  )
}
