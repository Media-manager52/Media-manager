"use client"

import * as React from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  Activity,
  ArchiveRestore,
  Bell,
  BookOpenCheck,
  CalendarDays,
  Clock3,
  Code2,
  DatabaseBackup,
  FileClock,
  Files,
  FolderCog,
  FolderInput,
  Gauge,
  Globe2,
  HardDrive,
  HeartPulse,
  Image,
  Layers3,
  Library,
  ListChecks,
  LockKeyhole,
  MonitorCog,
  Plus,
  Search,
  ServerCog,
  ShieldCheck,
  Sparkles,
  Target,
  TerminalSquare,
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
import {
  normalizeRoute,
  queueRecords,
  routeTitles,
  type QueueRecord,
  type ReleaseCandidate,
} from "@/lib/media-manager-data"
import { liveMode } from "@/lib/media-manager-api"
import {
  DashboardScreen,
  DiscoverScreen,
  FileDetailScreen,
  FilesScreen,
  IndexerDetailScreen,
  LibraryScreen,
  MovieScreen,
  PreviewBadge,
  QueueScreen,
  ReleasesScreen,
  SeriesScreen,
} from "./screens"
import { OperationalScreen } from "./operational-screens"
import {
  LiveConnectionsScreen,
  LiveDashboardScreen,
  LiveDiscoverScreen,
  LiveHealthScreen,
  LiveMediaDetailScreen,
  LiveProwlarrImportScreen,
  LiveProfilesScreen,
  LiveRecycleScreen,
  LiveRootsScreen,
  LiveUnavailableScreen,
  LiveWorkflowScreen,
} from "./live-screens"
import { PageHeader } from "./ui"

type NavItem = { label: string; href: string; icon: LucideIcon }
type NavGroup = { label: string; items: NavItem[] }

const navigation: NavGroup[] = [
  { label: "Home", items: [{ label: "Overview", href: "/", icon: MonitorCog }] },
  { label: "Library", items: [
    { label: "Media", href: "/media", icon: Library },
    { label: "File Manager", href: "/files", icon: Files },
    { label: "Collections", href: "/collections", icon: Layers3 },
    { label: "Calendar", href: "/calendar", icon: CalendarDays },
    { label: "Wanted & Upgrades", href: "/wanted", icon: Target },
    { label: "Import", href: "/import", icon: FolderInput },
  ] },
  { label: "Activity", items: [
    { label: "Queue", href: "/activity/queue", icon: Activity },
    { label: "History", href: "/activity/history", icon: FileClock },
    { label: "Blocklist", href: "/activity/blocklist", icon: ShieldCheck },
  ] },
  { label: "Intelligence", items: [
    { label: "Discover", href: "/discover", icon: Search },
    { label: "Release Intelligence", href: "/intelligence/releases", icon: Sparkles },
    { label: "Release Groups", href: "/intelligence/groups", icon: Users },
    { label: "Evidence Explorer", href: "/intelligence/evidence", icon: BookOpenCheck },
  ] },
  { label: "Sources", items: [
    { label: "Indexers", href: "/sources/indexers", icon: Globe2 },
    { label: "Indexer Lab", href: "/sources/indexer-lab", icon: TerminalSquare },
    { label: "Download Clients", href: "/sources/download-clients", icon: ServerCog },
    { label: "Import Lists", href: "/sources/import-lists", icon: ListChecks },
  ] },
  { label: "Settings", items: [
    { label: "Profiles & Policies", href: "/settings/profiles", icon: Gauge },
    { label: "Metadata & Artwork", href: "/settings/metadata", icon: Image },
    { label: "Media Management", href: "/settings/media-management", icon: FolderCog },
    { label: "Storage & Naming", href: "/settings/storage", icon: HardDrive },
    { label: "Connections", href: "/settings/connections", icon: Webhook },
    { label: "Notifications", href: "/settings/notifications", icon: Bell },
  ] },
  { label: "System", items: [
    { label: "Tasks", href: "/system/tasks", icon: Clock3 },
    { label: "Backups", href: "/system/backups", icon: DatabaseBackup },
    { label: "Security", href: "/system/security", icon: LockKeyhole },
    { label: "API", href: "/system/api", icon: Code2 },
    { label: "Health", href: "/system/health", icon: HeartPulse },
    { label: "Logs & Audit", href: "/system/logs", icon: TerminalSquare },
  ] },
]

const liveNavigation: NavGroup[] = [
  { label: "Home", items: [{ label: "Overview", href: "/", icon: MonitorCog }] },
  { label: "Library", items: [
    { label: "Media", href: "/media", icon: Library },
    { label: "Add media", href: "/discover", icon: Plus },
    { label: "Recycle", href: "/activity/recycle", icon: ArchiveRestore },
  ] },
  { label: "Activity", items: [{ label: "Workflows", href: "/activity/queue", icon: Activity }] },
  { label: "Setup", items: [
    { label: "Connections", href: "/settings/connections", icon: Webhook },
    { label: "Prowlarr import", href: "/sources/indexer-lab", icon: DatabaseBackup },
    { label: "Storage", href: "/settings/storage", icon: HardDrive },
    { label: "Profiles", href: "/settings/profiles", icon: Gauge },
    { label: "Health", href: "/system/health", icon: HeartPulse },
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
  const renderedNavigation = liveMode ? liveNavigation : navigation
  const flatNavigation = renderedNavigation.flatMap((group) => group.items.map((item) => ({ ...item, group: group.label })))
  const [commandOpen, setCommandOpen] = React.useState(false)
  const [queue, setQueue] = React.useState<QueueRecord[]>(queueRecords)

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

  const grabRelease = React.useCallback((candidate: ReleaseCandidate) => {
    setQueue((current) => {
      if (current.some((item) => item.id === `job_${candidate.id}`)) return current
      return [{ id: `job_${candidate.id}`, title: "Blade Runner 2049 (2017) · selected UHD replacement", kind: "Movie", client: "AltMount", stage: "Submission intent persisted; awaiting queue identity", progress: 3, state: "Queued", eta: "—" }, ...current]
    })
    toast.success("Release selected and one AltMount submission intent created")
    router.push("/activity/queue")
  }, [router])

  return (
    <SidebarProvider style={{ "--sidebar-width": "252px" } as React.CSSProperties}>
      <Sidebar collapsible="offcanvas" className="border-r border-[#222a34] bg-[#090c10]">
        <SidebarHeader className="border-b border-white/[0.06] p-4">
          <Link href="/" className="flex items-center gap-3 rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-[#8199f4]">
            <div className="grid size-9 place-items-center rounded-[11px] bg-gradient-to-br from-[#9bb0ff] to-[#647ddd] text-[11px] font-black tracking-[-0.08em] text-[#08101d] shadow-[0_8px_28px_rgba(100,125,221,.3)]">MM</div>
            <div className="min-w-0"><div className="truncate text-[13px] font-extrabold text-[#f0f3f6]">Media Manager</div><div className="mt-0.5 truncate text-[9px] text-[#778291]">One system for the whole library</div></div>
          </Link>
        </SidebarHeader>
        <SidebarContent className="px-2 py-2 scrollbar-thin">
          {renderedNavigation.map((group) => (
            <SidebarGroup key={group.label} className="py-1.5">
              <SidebarGroupLabel className="h-6 px-2 text-[8.5px] font-extrabold uppercase tracking-[0.18em] text-[#707b8a]">{group.label}</SidebarGroupLabel>
              <SidebarGroupContent>
                <SidebarMenu>
                  {group.items.map((item) => {
                    const active = isRouteActive(path, item.href)
                    const Icon = item.icon
                    return (
                      <SidebarMenuItem key={item.href}>
                        <SidebarMenuButton asChild isActive={active} tooltip={item.label} className="h-8 rounded-lg px-2 text-[11px] text-[#aeb7c3] data-[active=true]:bg-[#1a2234] data-[active=true]:font-bold data-[active=true]:text-white hover:bg-[#121820] hover:text-white">
                          <Link href={item.href}><Icon className={cn("size-4", active && "text-[#a8b8ff]")} /><span>{item.label}</span></Link>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    )
                  })}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          ))}
        </SidebarContent>
        <SidebarFooter className="border-t border-white/[0.06] p-3">
          <Link href="/system/health" className="flex items-center gap-2 rounded-lg p-2 transition hover:bg-white/[0.04] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#8199f4]">
            <span className="size-2 rounded-full bg-[#5fce9e] shadow-[0_0_0_4px_rgba(95,206,158,.09)]" />
            <div><div className="text-[9.5px] font-bold text-[#dbe1e8]">{liveMode ? "Owner service" : "Development environment healthy"}</div><div className="mt-0.5 text-[8.5px] text-[#707b89]">{liveMode ? "Open live health" : "UI fixture · safe interactions"}</div></div>
          </Link>
        </SidebarFooter>
        <SidebarRail />
      </Sidebar>

      <SidebarInset className="min-w-0 bg-transparent">
        <div className="sticky top-0 z-30 flex h-16 items-center gap-3 border-b border-white/[0.055] bg-[#080a0d]/90 px-3 backdrop-blur-xl sm:px-5 lg:px-7">
          <SidebarTrigger className="md:hidden" />
          <button onClick={() => setCommandOpen(true)} className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-[10px] border border-[#252d38] bg-[#0d1116] px-3 text-left text-[10px] text-[#818c9b] transition hover:border-[#3b4655] sm:max-w-[430px]">
            <Search className="size-4 shrink-0" /><span className="truncate">{liveMode ? "Navigate Media Manager…" : "Search pages, media, files, releases, settings…"}</span><kbd className="ml-auto hidden rounded-md border border-[#2e3744] bg-[#151a21] px-1.5 py-0.5 text-[8px] text-[#aeb6c1] sm:inline">Ctrl K</kbd>
          </button>
          <div className="ml-auto flex items-center gap-2">
            <PreviewBadge />
            <Tooltip><TooltipTrigger asChild><Button variant="ghost" size="icon" aria-label="Notifications" onClick={() => toast.info(liveMode ? "Workflow attention appears on the overview" : "No unread development notifications")}><Bell /></Button></TooltipTrigger><TooltipContent>Notifications</TooltipContent></Tooltip>
          </div>
        </div>

        <main className="mx-auto w-full max-w-[1780px] p-3 pb-12 sm:p-5 lg:p-7">
          <PageHeader
            {...page}
            actions={<><Button variant="outline" size="sm" onClick={() => setCommandOpen(true)}><Search /> Search</Button>{isAddContext(path) ? <Button asChild size="sm"><Link href="/discover"><Plus /> Add media</Link></Button> : null}</>}
          />
          <div className="mt-5"><RouteContent path={path} queue={queue} onGrab={grabRelease} /></div>
        </main>
      </SidebarInset>

      <CommandDialog open={commandOpen} onOpenChange={setCommandOpen} title="Search Media Manager" description={liveMode ? "Navigate the live owner controls" : "Navigate every production route and development record"} className="border-[#303947] bg-[#10141a] text-[#eef2f6]">
        <CommandInput placeholder={liveMode ? "Search owner controls…" : "Search pages, media, files, releases…"} />
        <CommandList><CommandEmpty>No matching page or record.</CommandEmpty><CommandGroup heading="Navigation">{flatNavigation.map((item) => { const Icon = item.icon; return <CommandItem key={item.href} value={`${item.group} ${item.label}`} onSelect={() => navigate(item.href)}><Icon /><span>{item.label}</span><CommandShortcut>{item.group}</CommandShortcut></CommandItem> })}</CommandGroup>{!liveMode ? <CommandGroup heading="Media"><CommandItem value="Blade Runner 2049 movie" onSelect={() => navigate("/media/movies/mov_blade_runner_2049")}><Library /><span>Blade Runner 2049</span><CommandShortcut>Movie</CommandShortcut></CommandItem><CommandItem value="Foundation series season pack" onSelect={() => navigate("/media/series/ser_foundation")}><Library /><span>Foundation</span><CommandShortcut>Series</CommandShortcut></CommandItem><CommandItem value="Blade Runner file inspector" onSelect={() => navigate("/files/file_blade_2049")}><Files /><span>Blade Runner managed file</span><CommandShortcut>File</CommandShortcut></CommandItem></CommandGroup> : null}</CommandList>
      </CommandDialog>
      <Toaster position="bottom-right" />
    </SidebarProvider>
  )
}

function RouteContent({ path, queue, onGrab }: { path: string; queue: QueueRecord[]; onGrab: (candidate: ReleaseCandidate) => void }) {
  const normalized = normalizeRoute(path)
  if (liveMode) {
    if (path === "/") return <LiveDashboardScreen />
    if (path === "/media") return <LibraryScreen />
    if (normalized === "/media/movies/$mediaId" || normalized === "/media/series/$mediaId") return <LiveMediaDetailScreen mediaId={path.split("/").at(-1) ?? ""} />
    if (path === "/discover") return <LiveDiscoverScreen />
    if (path === "/activity/queue") return <LiveWorkflowScreen />
    if (path === "/activity/recycle") return <LiveRecycleScreen />
    if (path === "/settings/connections") return <LiveConnectionsScreen />
    if (path === "/sources/indexer-lab") return <LiveProwlarrImportScreen />
    if (path === "/settings/storage") return <LiveRootsScreen />
    if (path === "/settings/profiles") return <LiveProfilesScreen />
    if (path === "/system/health") return <LiveHealthScreen />
    return <LiveUnavailableScreen />
  }
  if (path === "/") return <DashboardScreen queue={queue} />
  if (path === "/media") return <LibraryScreen />
  if (normalized === "/media/movies/$mediaId") return <MovieScreen />
  if (normalized === "/media/series/$mediaId") return <SeriesScreen />
  if (path === "/discover") return <DiscoverScreen />
  if (path === "/intelligence/releases") return <ReleasesScreen onGrab={onGrab} />
  if (path === "/activity/queue") return <QueueScreen queue={queue} />
  if (path === "/files") return <FilesScreen />
  if (normalized === "/files/$fileId") return <FileDetailScreen />
  if (normalized === "/sources/indexers/$indexerId") return <IndexerDetailScreen />
  return <OperationalScreen path={path} />
}

function isRouteActive(path: string, href: string) {
  if (href === "/") return path === "/"
  if (href === "/media") return path === "/media" || path.startsWith("/media/")
  if (href === "/files") return path === "/files" || path.startsWith("/files/")
  if (href === "/sources/indexers") return path === href || path.startsWith("/sources/indexers/")
  return path === href
}

function isAddContext(path: string) {
  return path === "/" || path === "/media" || path === "/discover" || path.startsWith("/media/")
}
