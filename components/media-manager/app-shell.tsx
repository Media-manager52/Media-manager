"use client"

import * as React from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  Activity,
  ArchiveRestore,
  Bell,
  BookOpenCheck,
  ChevronDown,
  DatabaseBackup,
  FileClock,
  Files,
  Gauge,
  Globe2,
  HardDrive,
  HeartPulse,
  Inbox,
  Library,
  ListChecks,
  MonitorCog,
  Plus,
  Search,
  ServerCog,
  Settings2,
  ShieldCheck,
  Sparkles,
  TerminalSquare,
  Tv,
  Users,
  Webhook,
  type LucideIcon,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandShortcut,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { Toaster } from "@/components/ui/sonner"
import { cn } from "@/lib/utils"
import { loadSystemHealth, loadSystemInfo, loadWanted, loadWorkflows, type APIWantedItem, type APIWorkflow } from "@/lib/media-manager-api"
import { collectOwnerAttention } from "@/lib/workflow-states"
import { normalizeRoute, routeTitles } from "@/lib/media-manager-routes"
import {
  LiveConnectionsScreen,
  LiveAdoptionScreen,
  LiveCollectionsScreen,
  LiveDiscoverScreen,
  LiveMediaDetailScreen,
  LiveProwlarrImportScreen,
  LiveProfilesScreen,
  LiveRecycleScreen,
  LiveRootsScreen,
  LiveUnavailableScreen,
  LiveWorkflowScreen,
} from "./live-screens"
import {
  LiveActivityHistoryScreen,
  LiveBlocklistScreen,
  LiveBackupsScreen,
  LiveCalendarScreen,
  LiveComponentHealthScreen,
  LiveFileDetailScreen,
  LiveFilesScreen,
  LiveIndexerDetailScreen,
  LiveImportListsScreen,
  LiveLibraryScreen,
  LiveMetadataSettingsScreen,
  LiveNotificationsScreen,
  LiveOperationalDashboardScreen,
  LiveReleaseIntelligenceScreen,
  LiveRequestsScreen,
  LiveSourcesScreen,
  LiveSystemInfoScreen,
  LiveTasksScreen,
  LiveWantedScreen,
} from "./live-operational-screens"
import { PageHeader, StateBadge } from "./ui"

type NavItem = { label: string; href: string; icon: LucideIcon }
type NavGroup = { label: string; items: NavItem[]; advanced?: boolean; description?: string }

/**
 * Navigation deliberately separates the everyday product from advanced tooling.
 * Every route stays reachable; only its visual weight changes. Advanced
 * observability surfaces live in the collapsible Advanced group instead of
 * competing with Movies, TV Shows, and Requests.
 */
const liveNavigation: NavGroup[] = [
  { label: "Product", description: "Everyday library work", items: [
    { label: "Overview", href: "/", icon: MonitorCog },
    { label: "Movies", href: "/media", icon: Library },
    { label: "TV Shows", href: "/media/series", icon: Tv },
    { label: "Requests", href: "/requests", icon: Inbox },
    { label: "Calendar", href: "/calendar", icon: FileClock },
    { label: "Wanted", href: "/wanted", icon: ListChecks },
    { label: "Add media", href: "/discover", icon: Plus },
  ] },
  { label: "Operations", description: "Acquisition and files", items: [
    { label: "Workflows", href: "/activity/queue", icon: Activity },
    { label: "File Manager", href: "/files", icon: Files },
    { label: "Adoption", href: "/import", icon: DatabaseBackup },
    { label: "Recycle", href: "/activity/recycle", icon: ArchiveRestore },
  ] },
  { label: "Sources", description: "Where media comes from", items: [
    { label: "Indexers", href: "/sources/indexers", icon: Globe2 },
    { label: "Clients", href: "/sources/download-clients", icon: ServerCog },
    { label: "Import Lists", href: "/sources/import-lists", icon: ListChecks },
    { label: "Prowlarr", href: "/sources/indexer-lab", icon: DatabaseBackup },
  ] },
  { label: "Settings", description: "Policy and connections", items: [
    { label: "Profiles", href: "/settings/profiles", icon: Gauge },
    { label: "Storage", href: "/settings/storage", icon: HardDrive },
    { label: "Metadata", href: "/settings/metadata", icon: BookOpenCheck },
    { label: "Media mgmt", href: "/settings/media-management", icon: Settings2 },
    { label: "Connections", href: "/settings/connections", icon: Webhook },
    { label: "Notifications", href: "/settings/notifications", icon: Bell },
  ] },
  { label: "System", description: "Health and access", items: [
    { label: "Health", href: "/system/health", icon: HeartPulse },
    { label: "Tasks", href: "/system/tasks", icon: ListChecks },
    { label: "Backups", href: "/system/backups", icon: DatabaseBackup },
    { label: "Security", href: "/system/security", icon: ShieldCheck },
    { label: "API", href: "/system/api", icon: TerminalSquare },
    { label: "Logs & Audit", href: "/system/logs", icon: TerminalSquare },
  ] },
  { label: "Advanced", description: "Evidence and observability", advanced: true, items: [
    { label: "Collections", href: "/collections", icon: BookOpenCheck },
    { label: "Releases", href: "/intelligence/releases", icon: Sparkles },
    { label: "Release groups", href: "/intelligence/groups", icon: Users },
    { label: "Evidence", href: "/intelligence/evidence", icon: BookOpenCheck },
    { label: "History", href: "/activity/history", icon: FileClock },
    { label: "Blocklist", href: "/activity/blocklist", icon: ShieldCheck },
  ] },
]

const detailTitles: Record<string, { area: string; title: string; subtitle: string }> = {
  "/media/movies/$mediaId": { area: "Library · Movie", title: "Movie details", subtitle: "Monitoring, versions, files, history, metadata, and evidence." },
  "/media/series/$mediaId": { area: "Library · Series", title: "Series details", subtitle: "Season monitoring, episode coverage, pack verification, and publication." },
  "/files/$fileId": { area: "Library · File Manager", title: "File inspector", subtitle: "Identity, provenance, tracks, links, properties, and safe operations." },
  "/sources/indexers/$indexerId": { area: "Sources · Indexers", title: "Indexer details", subtitle: "Learned capabilities, behavior, limits, evidence, and safe strategy." },
}

export function MediaManagerApp({ path }: { path: string }) {
  const router = useRouter()
  const normalized = normalizeRoute(path)
  const page = detailTitles[normalized] ?? routeTitles[normalized] ?? { area: "Media Manager", title: "Not found", subtitle: "This route is not in the locked screen map." }
  const renderedNavigation = liveNavigation
  const [commandOpen, setCommandOpen] = React.useState(false)
  const [openGroups, setOpenGroups] = React.useState<Set<string>>(() => new Set(["Product", "Operations"]))
  const [serviceStatus, setServiceStatus] = React.useState<{ ready?: boolean; version?: string }>({})
  const libraryLayout = path === "/media" || path === "/media/series"
  const mediaDetailLayout = normalized === "/media/movies/$mediaId" || normalized === "/media/series/$mediaId"

  React.useEffect(() => {
    const controller = new AbortController()
    Promise.all([loadSystemHealth(controller.signal), loadSystemInfo(controller.signal)])
      .then(([health, info]) => setServiceStatus({ ready: health.database_state === "ready" && health.schema_version === health.expected_schema, version: info.version }))
      .catch(() => { if (!controller.signal.aborted) setServiceStatus({ ready: false }) })
    return () => controller.abort()
  }, [])

  React.useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault()
        setCommandOpen((current) => !current)
      }
    }
    window.addEventListener("keydown", handler)
    return () => window.removeEventListener("keydown", handler)
  }, [])

  const navigate = React.useCallback((href: string) => {
    setCommandOpen(false)
    router.push(href)
  }, [router, setCommandOpen])

  return (
    <SidebarProvider style={{ "--sidebar-width": "184px" } as React.CSSProperties}>
      <Sidebar collapsible="offcanvas" className="border-r border-[#1e2735] bg-[#0a0e15]">
        <SidebarHeader className="h-[60px] justify-center border-b border-[#1e2735] px-3 py-0">
          <Link href="/" className="flex items-center gap-2.5 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-[#7c5cf0]">
            <div className="grid size-8 place-items-center rounded-[7px] bg-gradient-to-br from-[#8b5cf6] to-[#6d28d9] text-[11px] font-black tracking-[-0.06em] text-white shadow-[0_4px_14px_rgba(124,92,240,0.35)]">MM</div>
            <div className="min-w-0"><div className="truncate text-[12.5px] font-extrabold tracking-[-0.01em] text-[#f4f6fa]">Media Manager</div><div className="mt-0.5 truncate text-[11px] text-[#8f9aab]">Owner console</div></div>
          </Link>
        </SidebarHeader>
        <SidebarContent className="gap-0 px-1.5 py-2 scrollbar-thin">
          {renderedNavigation.map((group) => {
            const groupActive = group.items.some((item) => isRouteActive(path, item.href))
            const open = openGroups.has(group.label) || groupActive
            return (
            <SidebarGroup key={group.label} className={cn("py-0.5", group.advanced && "mt-2 border-t border-[#1a2230] pt-2")}>
              <SidebarGroupLabel asChild className={cn("h-7 px-2 text-[11px] font-extrabold uppercase tracking-[0.16em] hover:bg-[#111925] hover:text-[#c2c8d0]", group.advanced ? "text-[#8b96a6]" : "text-[#8f9aab]")}>
                <button type="button" title={group.description} aria-expanded={open} onClick={() => setOpenGroups((current) => { const next = new Set(current); if (next.has(group.label)) next.delete(group.label); else next.add(group.label); return next })}><span>{group.label}</span><ChevronDown className={cn("ml-auto size-3 transition-transform", !open && "-rotate-90", groupActive && "text-[#a98cf5]")} /></button>
              </SidebarGroupLabel>
              {open ? <SidebarGroupContent>
                <SidebarMenu>
                  {group.items.map((item) => {
                    const active = isRouteActive(path, item.href)
                    const Icon = item.icon
                    return (
                      <SidebarMenuItem key={item.href}>
                        <SidebarMenuButton asChild isActive={active} tooltip={item.label} className={cn("h-[30px] rounded-[6px] px-2 text-[12px] data-[active=true]:bg-[#1c1735] data-[active=true]:font-bold data-[active=true]:text-white hover:bg-[#111925] hover:text-white", group.advanced ? "text-[#96a0ae]" : "text-[#b3bcc8]")}>
                          <Link href={item.href}><Icon className={cn("size-4", active ? "text-[#a98cf5]" : group.advanced ? "text-[#6c7686]" : "text-[#828d9d]")} /><span>{item.label}</span></Link>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    )
                  })}
                </SidebarMenu>
              </SidebarGroupContent> : null}
            </SidebarGroup>
          )})}
        </SidebarContent>
        <SidebarFooter className="border-t border-[#1e2735] p-2">
          <Link href="/system/health" className="flex items-center gap-2.5 rounded-[6px] p-2 transition hover:bg-[#111925] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#7c5cf0]">
            <span className={cn("size-2 rounded-full", serviceStatus.ready === undefined ? "bg-[#697586]" : serviceStatus.ready ? "bg-[#4fc48d]" : "bg-[#eeb95c]")} />
            <div className="min-w-0"><div className="truncate text-[11.5px] font-bold text-[#dbe1e8]">{serviceStatus.ready === undefined ? "Checking service" : serviceStatus.ready ? "Service healthy" : "Service attention"}</div><div className="mt-0.5 truncate text-[11px] text-[#8f9aab]">{serviceStatus.version ? `Version ${serviceStatus.version}` : "Health & version"}</div></div>
          </Link>
        </SidebarFooter>
        <SidebarRail />
      </Sidebar>

      <SidebarInset className="min-w-0 bg-transparent">
        <div className="sticky top-0 z-30 flex h-[60px] shrink-0 items-center gap-3 border-b border-[#1e2735] bg-[#07090f]/95 px-3 backdrop-blur-md sm:px-4">
          <SidebarTrigger className="md:hidden" />
          <button onClick={() => setCommandOpen(true)} className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-[7px] border border-[#202a39] bg-[#0d121b] px-3 text-left text-[12px] text-[#9ba5b2] transition hover:border-[#344155] hover:text-[#c2cbd6] sm:max-w-[380px]">
            <Search className="size-4 shrink-0" /><span className="truncate">Go to page…</span><kbd className="ml-auto hidden rounded-[4px] border border-[#283447] bg-[#111925] px-1.5 py-0.5 text-[11px] text-[#aeb6c1] sm:inline">Ctrl K</kbd>
          </button>
          <div className="ml-auto flex items-center gap-2">
            <OwnerAttentionBell />
          </div>
        </div>

        <main className={cn("w-full", libraryLayout ? "h-[calc(100dvh-60px)] min-h-0 overflow-hidden" : "mx-auto max-w-[1780px] p-3 pb-12 sm:p-5 lg:p-7")}>
          {libraryLayout || mediaDetailLayout ? <RouteContent path={path} /> : <>
            <PageHeader
              {...page}
              actions={<><Button variant="outline" size="sm" onClick={() => setCommandOpen(true)}><Search /> Go to page</Button>{isAddContext(path) ? <Button asChild size="sm"><Link href="/discover"><Plus /> Add media</Link></Button> : null}</>}
            />
            <div className="mt-5"><RouteContent path={path} /></div>
          </>}
        </main>
      </SidebarInset>

      <CommandDialog open={commandOpen} onOpenChange={setCommandOpen} title="Go to page" description="Jump to a page in Media Manager" className="border-[#303947] bg-[#10141a] text-[#eef2f6]">
        <CommandInput placeholder="Go to page…" />
        <CommandList><CommandEmpty>No matching page.</CommandEmpty>{renderedNavigation.map((group) => <CommandGroup key={group.label} heading={group.advanced ? `${group.label} · advanced` : group.label}>{group.items.map((item) => { const Icon = item.icon; return <CommandItem key={item.href} value={`${group.label} ${item.label}`} onSelect={() => navigate(item.href)}><Icon /><span>{item.label}</span><CommandShortcut>{group.label}</CommandShortcut></CommandItem> })}</CommandGroup>)}</CommandList>
      </CommandDialog>
      <Toaster position="bottom-right" />
    </SidebarProvider>
  )
}

function OwnerAttentionBell() {
  const [open, setOpen] = React.useState(false)
  const [items, setItems] = React.useState<APIWantedItem[]>([])
  const [workflows, setWorkflows] = React.useState<APIWorkflow[]>([])
  const [error, setError] = React.useState("")
  React.useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    Promise.all([loadWanted(controller.signal), loadWorkflows(true, controller.signal)])
      .then(([nextItems, nextWorkflows]) => { setItems(nextItems); setWorkflows(nextWorkflows); setError("") })
      .catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Could not load owner attention") })
    return () => controller.abort()
  }, [open])
  const attention = collectOwnerAttention(items, workflows)
  const shown = attention.slice(0, 4)
  return <Popover open={open} onOpenChange={setOpen}>
    <PopoverTrigger asChild>
      <Button variant="ghost" size="icon" aria-label="Owner attention" className="relative">
        <Bell />
        {attention.length ? <span className="absolute right-1 top-1 size-2 rounded-full bg-[#eeb95c] ring-2 ring-[#07090f]" aria-hidden /> : null}
      </Button>
    </PopoverTrigger>
    <PopoverContent align="end" className="w-[340px] border-[#303947] bg-[#10141a] p-0 text-[#eef2f6]">
      <div className="flex items-center justify-between gap-2 border-b border-[#202a39] px-3.5 py-2.5">
        <span className="text-[12px] font-bold">Owner attention</span>
        {attention.length ? <span className="rounded-full border border-[#594923] bg-[#211b10] px-2 py-0.5 text-[11px] font-bold text-[#ebcf8c]">{attention.length}</span> : null}
      </div>
      {error ? <div className="px-3.5 py-4 text-[12px] text-[#f1a6a6]">{error}</div>
        : shown.length === 0 ? <div className="px-3.5 py-4 text-[12px] text-[#9ba5b2]">Nothing needs your attention. Waiting and monitoring continue automatically.</div>
        : <div className="divide-y divide-[#202a39]">{shown.map((entry) => <Link key={entry.id} href={entry.href} onClick={() => setOpen(false)} className="block px-3.5 py-2.5 outline-none hover:bg-[#111925] focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[#7c5cf0]">
          <div className="flex items-center justify-between gap-2"><span className="truncate text-[12.5px] font-semibold text-[#eef2f7]">{entry.title}{entry.year ? ` (${entry.year})` : ""}</span><StateBadge state={entry.state} /></div>
          <div className="mt-1 line-clamp-2 text-[11.5px] leading-4 text-[#dcc9a0]">{entry.reason}</div>
          <div className="mt-1 truncate font-mono text-[11px] text-[#7f8a9a]">{entry.variant}</div>
        </Link>)}</div>}
      <div className="border-t border-[#202a39] p-2">
        <Button asChild variant="ghost" size="sm" className="w-full justify-center" onClick={() => setOpen(false)}><Link href="/wanted">View all in Wanted</Link></Button>
      </div>
    </PopoverContent>
  </Popover>
}

function RouteContent({ path }: { path: string }) {
  const normalized = normalizeRoute(path)
  if (path === "/") return <LiveOperationalDashboardScreen />
  if (path === "/media") return <LiveLibraryScreen />
  if (path === "/media/series") return <LiveLibraryScreen mediaKind="series" />
  if (path === "/collections") return <LiveCollectionsScreen />
  if (path === "/calendar") return <LiveCalendarScreen />
  if (path === "/wanted") return <LiveWantedScreen />
  if (path === "/requests") return <LiveRequestsScreen />
  if (normalized === "/media/movies/$mediaId" || normalized === "/media/series/$mediaId") return <LiveMediaDetailScreen mediaId={path.split("/").at(-1) ?? ""} />
  if (path === "/files") return <LiveFilesScreen />
  if (path === "/import") return <LiveAdoptionScreen />
  if (normalized === "/files/$fileId") return <LiveFileDetailScreen fileId={path.split("/").at(-1) ?? ""} />
  if (path === "/discover") return <LiveDiscoverScreen />
  if (path === "/activity/queue") return <LiveWorkflowScreen />
  if (path === "/activity/recycle") return <LiveRecycleScreen />
  if (path === "/activity/history") return <LiveActivityHistoryScreen />
  if (path === "/activity/blocklist") return <LiveBlocklistScreen />
  if (path === "/intelligence/releases") return <LiveReleaseIntelligenceScreen />
  if (path === "/intelligence/groups") return <LiveReleaseIntelligenceScreen mode="groups" />
  if (path === "/intelligence/evidence") return <LiveReleaseIntelligenceScreen mode="evidence" />
  if (path === "/sources/indexers") return <LiveSourcesScreen kind="newznab" />
  if (normalized === "/sources/indexers/$indexerId") return <LiveIndexerDetailScreen indexerId={path.split("/").at(-1) ?? ""} />
  if (path === "/sources/download-clients") return <LiveSourcesScreen kind="altmount" />
  if (path === "/settings/connections") return <LiveConnectionsScreen />
  if (path === "/sources/indexer-lab") return <LiveProwlarrImportScreen />
  if (path === "/sources/import-lists") return <LiveImportListsScreen />
  if (path === "/settings/storage") return <LiveRootsScreen />
  if (path === "/settings/profiles") return <LiveProfilesScreen />
  if (path === "/settings/metadata") return <LiveMetadataSettingsScreen />
  if (path === "/settings/media-management") return <LiveSystemInfoScreen mode="media-management" />
  if (path === "/settings/notifications") return <LiveNotificationsScreen />
  if (path === "/system/tasks") return <LiveTasksScreen />
  if (path === "/system/backups") return <LiveBackupsScreen />
  if (path === "/system/security") return <LiveSystemInfoScreen mode="security" />
  if (path === "/system/api") return <LiveSystemInfoScreen mode="api" />
  if (path === "/system/health") return <LiveComponentHealthScreen />
  if (path === "/system/logs") return <LiveActivityHistoryScreen logs />
  return <LiveUnavailableScreen />
}

function isRouteActive(path: string, href: string) {
  if (href === "/") return path === "/"
  if (href === "/media") return path === "/media" || path.startsWith("/media/movies/")
  if (href === "/media/series") return path === href || path.startsWith("/media/series/")
  if (href === "/files") return path === "/files" || path.startsWith("/files/")
  if (href === "/sources/indexers") return path === href || path.startsWith("/sources/indexers/")
  return path === href
}

function isAddContext(path: string) {
  return path === "/" || path === "/media" || path === "/discover" || path.startsWith("/media/")
}
