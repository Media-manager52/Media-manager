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
import { toast } from "sonner"

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
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { loadSystemHealth, loadSystemInfo } from "@/lib/media-manager-api"
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
  LiveSourcesScreen,
  LiveSystemInfoScreen,
  LiveTasksScreen,
  LiveWantedScreen,
} from "./live-operational-screens"
import { PageHeader } from "./ui"

type NavItem = { label: string; href: string; icon: LucideIcon }
type NavGroup = { label: string; items: NavItem[] }

const liveNavigation: NavGroup[] = [
  { label: "Home", items: [{ label: "Overview", href: "/", icon: MonitorCog }] },
  { label: "Library", items: [
    { label: "Movies", href: "/media", icon: Library },
    { label: "TV Shows", href: "/media/series", icon: Tv },
    { label: "Collections", href: "/collections", icon: BookOpenCheck },
    { label: "Calendar", href: "/calendar", icon: FileClock },
    { label: "Wanted & Upgrades", href: "/wanted", icon: ListChecks },
    { label: "File Manager", href: "/files", icon: Files },
    { label: "Adoption Review", href: "/import", icon: DatabaseBackup },
    { label: "Add media", href: "/discover", icon: Plus },
    { label: "Recycle", href: "/activity/recycle", icon: ArchiveRestore },
  ] },
  { label: "Activity", items: [
    { label: "Workflows", href: "/activity/queue", icon: Activity },
    { label: "History", href: "/activity/history", icon: FileClock },
    { label: "Blocklist", href: "/activity/blocklist", icon: ShieldCheck },
  ] },
  { label: "Intelligence", items: [
    { label: "Release Intelligence", href: "/intelligence/releases", icon: Sparkles },
    { label: "Release Groups", href: "/intelligence/groups", icon: Users },
    { label: "Evidence Explorer", href: "/intelligence/evidence", icon: BookOpenCheck },
  ] },
  { label: "Sources", items: [
    { label: "Indexers", href: "/sources/indexers", icon: Globe2 },
    { label: "Prowlarr import", href: "/sources/indexer-lab", icon: DatabaseBackup },
    { label: "Import Lists", href: "/sources/import-lists", icon: ListChecks },
    { label: "Download Clients", href: "/sources/download-clients", icon: ServerCog },
  ] },
  { label: "Setup", items: [
    { label: "Connections", href: "/settings/connections", icon: Webhook },
    { label: "Storage", href: "/settings/storage", icon: HardDrive },
    { label: "Profiles", href: "/settings/profiles", icon: Gauge },
    { label: "Metadata & Artwork", href: "/settings/metadata", icon: BookOpenCheck },
    { label: "Media Management", href: "/settings/media-management", icon: Settings2 },
    { label: "Notifications", href: "/settings/notifications", icon: Bell },
  ] },
  { label: "System", items: [
    { label: "Tasks", href: "/system/tasks", icon: ListChecks },
    { label: "Backups", href: "/system/backups", icon: DatabaseBackup },
    { label: "Security", href: "/system/security", icon: ShieldCheck },
    { label: "API", href: "/system/api", icon: TerminalSquare },
    { label: "Health", href: "/system/health", icon: HeartPulse },
    { label: "Logs & Audit", href: "/system/logs", icon: TerminalSquare },
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
  const flatNavigation = renderedNavigation.flatMap((group) => group.items.map((item) => ({ ...item, group: group.label })))
  const [commandOpen, setCommandOpen] = React.useState(false)
  const [openGroups, setOpenGroups] = React.useState<Set<string>>(() => new Set(["Home", "Library"]))
  const [serviceStatus, setServiceStatus] = React.useState<{ ready?: boolean; version?: string }>({})
  const libraryLayout = path === "/media" || path === "/media/series"

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
      <Sidebar collapsible="offcanvas" className="border-r border-[#202a39] bg-[#0b0f17]">
        <SidebarHeader className="h-[60px] justify-center border-b border-[#202a39] px-3 py-0">
          <Link href="/" className="flex items-center gap-2.5 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-[#7c3aed]">
            <div className="grid size-8 place-items-center rounded-[6px] bg-[#7c3aed] text-[10px] font-black tracking-[-0.08em] text-white">MM</div>
            <div className="min-w-0"><div className="truncate text-[11px] font-extrabold text-[#f4f6fa]">Media Manager</div><div className="mt-0.5 truncate text-[10px] text-[#929dac]">Owner console</div></div>
          </Link>
        </SidebarHeader>
        <SidebarContent className="gap-0 px-1.5 py-2 scrollbar-thin">
          {renderedNavigation.map((group) => {
            const groupActive = group.items.some((item) => isRouteActive(path, item.href))
            const open = openGroups.has(group.label) || groupActive
            return (
            <SidebarGroup key={group.label} className="py-0.5">
              <SidebarGroupLabel asChild className="h-7 px-2 text-[9px] font-extrabold uppercase tracking-[0.14em] text-[#929dac] hover:bg-[#111925] hover:text-[#c2c8d0]">
                <button type="button" aria-expanded={open} onClick={() => setOpenGroups((current) => { const next = new Set(current); if (next.has(group.label)) next.delete(group.label); else next.add(group.label); return next })}><span>{group.label}</span><ChevronDown className={cn("ml-auto size-3 transition-transform", !open && "-rotate-90", groupActive && "text-[#9f7aea]")} /></button>
              </SidebarGroupLabel>
              {open ? <SidebarGroupContent>
                <SidebarMenu>
                  {group.items.map((item) => {
                    const active = isRouteActive(path, item.href)
                    const Icon = item.icon
                    return (
                      <SidebarMenuItem key={item.href}>
                        <SidebarMenuButton asChild isActive={active} tooltip={item.label} className="h-7 rounded-[5px] px-2 text-[11px] text-[#aab3bf] data-[active=true]:bg-[#211b36] data-[active=true]:font-bold data-[active=true]:text-white hover:bg-[#111925] hover:text-white">
                          <Link href={item.href}><Icon className={cn("size-3.5", active && "text-[#a98cf5]")} /><span>{item.label}</span></Link>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    )
                  })}
                </SidebarMenu>
              </SidebarGroupContent> : null}
            </SidebarGroup>
          )})}
        </SidebarContent>
        <SidebarFooter className="border-t border-[#202a39] p-2">
          <Link href="/system/health" className="flex items-center gap-2 rounded-[5px] p-2 transition hover:bg-[#111925] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#7c3aed]">
            <span className={cn("size-2 rounded-full", serviceStatus.ready === undefined ? "bg-[#697586]" : serviceStatus.ready ? "bg-[#54c48e]" : "bg-[#d99a4e]")} />
            <div className="min-w-0"><div className="truncate text-[10px] font-bold text-[#dbe1e8]">{serviceStatus.ready === undefined ? "Checking service" : serviceStatus.ready ? "Service healthy" : "Service attention"}</div><div className="mt-0.5 truncate text-[9px] text-[#929dac]">{serviceStatus.version ? `Version ${serviceStatus.version}` : "Health & version"}</div></div>
          </Link>
        </SidebarFooter>
        <SidebarRail />
      </Sidebar>

      <SidebarInset className="min-w-0 bg-transparent">
        <div className="sticky top-0 z-30 flex h-[60px] shrink-0 items-center gap-3 border-b border-[#202a39] bg-[#080b12]/95 px-3 backdrop-blur-md sm:px-4">
          <SidebarTrigger className="md:hidden" />
          <button onClick={() => setCommandOpen(true)} className="flex h-8 min-w-0 flex-1 items-center gap-2 rounded-[6px] border border-[#202a39] bg-[#0e141e] px-3 text-left text-[11px] text-[#9ba5b2] transition hover:border-[#344155] sm:max-w-[360px]">
            <Search className="size-3.5 shrink-0" /><span className="truncate">Navigate Media Manager…</span><kbd className="ml-auto hidden rounded-[4px] border border-[#283447] bg-[#111925] px-1.5 py-0.5 text-[9px] text-[#aeb6c1] sm:inline">Ctrl K</kbd>
          </button>
          <div className="ml-auto flex items-center gap-2">
            <Tooltip><TooltipTrigger asChild><Button variant="ghost" size="icon" aria-label="Notifications" onClick={() => toast.info("Workflow attention appears on the overview")}><Bell /></Button></TooltipTrigger><TooltipContent>Notifications</TooltipContent></Tooltip>
          </div>
        </div>

        <main className={cn("w-full", libraryLayout ? "h-[calc(100dvh-60px)] min-h-0 overflow-hidden" : "mx-auto max-w-[1780px] p-3 pb-12 sm:p-5 lg:p-7")}>
          {libraryLayout ? <RouteContent path={path} /> : <>
            <PageHeader
              {...page}
              actions={<><Button variant="outline" size="sm" onClick={() => setCommandOpen(true)}><Search /> Search</Button>{isAddContext(path) ? <Button asChild size="sm"><Link href="/discover"><Plus /> Add media</Link></Button> : null}</>}
            />
            <div className="mt-5"><RouteContent path={path} /></div>
          </>}
        </main>
      </SidebarInset>

      <CommandDialog open={commandOpen} onOpenChange={setCommandOpen} title="Search Media Manager" description="Navigate the live owner controls" className="border-[#303947] bg-[#10141a] text-[#eef2f6]">
        <CommandInput placeholder="Search owner controls…" />
        <CommandList><CommandEmpty>No matching page.</CommandEmpty><CommandGroup heading="Navigation">{flatNavigation.map((item) => { const Icon = item.icon; return <CommandItem key={item.href} value={`${item.group} ${item.label}`} onSelect={() => navigate(item.href)}><Icon /><span>{item.label}</span><CommandShortcut>{item.group}</CommandShortcut></CommandItem> })}</CommandGroup></CommandList>
      </CommandDialog>
      <Toaster position="bottom-right" />
    </SidebarProvider>
  )
}

function RouteContent({ path }: { path: string }) {
  const normalized = normalizeRoute(path)
  if (path === "/") return <LiveOperationalDashboardScreen />
  if (path === "/media") return <LiveLibraryScreen />
  if (path === "/media/series") return <LiveLibraryScreen mediaKind="series" />
  if (path === "/collections") return <LiveCollectionsScreen />
  if (path === "/calendar") return <LiveCalendarScreen />
  if (path === "/wanted") return <LiveWantedScreen />
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
