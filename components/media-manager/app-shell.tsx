"use client"

import * as React from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  Activity,
  ArchiveRestore,
  Bell,
  BookOpenCheck,
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
import { normalizeRoute, routeTitles } from "@/lib/media-manager-routes"
import {
  LiveConnectionsScreen,
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
  LiveComponentHealthScreen,
  LiveFileDetailScreen,
  LiveFilesScreen,
  LiveIndexerDetailScreen,
  LiveLibraryScreen,
  LiveMetadataSettingsScreen,
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
    { label: "Media", href: "/media", icon: Library },
    { label: "Wanted & Upgrades", href: "/wanted", icon: ListChecks },
    { label: "File Manager", href: "/files", icon: Files },
    { label: "Add media", href: "/discover", icon: Plus },
    { label: "Recycle", href: "/activity/recycle", icon: ArchiveRestore },
  ] },
  { label: "Activity", items: [
    { label: "Workflows", href: "/activity/queue", icon: Activity },
    { label: "History", href: "/activity/history", icon: FileClock },
  ] },
  { label: "Intelligence", items: [
    { label: "Release Intelligence", href: "/intelligence/releases", icon: Sparkles },
    { label: "Release Groups", href: "/intelligence/groups", icon: Users },
    { label: "Evidence Explorer", href: "/intelligence/evidence", icon: BookOpenCheck },
  ] },
  { label: "Sources", items: [
    { label: "Indexers", href: "/sources/indexers", icon: Globe2 },
    { label: "Prowlarr import", href: "/sources/indexer-lab", icon: DatabaseBackup },
    { label: "Download Clients", href: "/sources/download-clients", icon: ServerCog },
  ] },
  { label: "Setup", items: [
    { label: "Connections", href: "/settings/connections", icon: Webhook },
    { label: "Storage", href: "/settings/storage", icon: HardDrive },
    { label: "Profiles", href: "/settings/profiles", icon: Gauge },
    { label: "Metadata & Artwork", href: "/settings/metadata", icon: BookOpenCheck },
    { label: "Media Management", href: "/settings/media-management", icon: Settings2 },
  ] },
  { label: "System", items: [
    { label: "Tasks", href: "/system/tasks", icon: ListChecks },
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
            <div><div className="text-[9.5px] font-bold text-[#dbe1e8]">Owner service</div><div className="mt-0.5 text-[8.5px] text-[#707b89]">Open live health</div></div>
          </Link>
        </SidebarFooter>
        <SidebarRail />
      </Sidebar>

      <SidebarInset className="min-w-0 bg-transparent">
        <div className="sticky top-0 z-30 flex h-16 items-center gap-3 border-b border-white/[0.055] bg-[#080a0d]/90 px-3 backdrop-blur-xl sm:px-5 lg:px-7">
          <SidebarTrigger className="md:hidden" />
          <button onClick={() => setCommandOpen(true)} className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-[10px] border border-[#252d38] bg-[#0d1116] px-3 text-left text-[10px] text-[#818c9b] transition hover:border-[#3b4655] sm:max-w-[430px]">
            <Search className="size-4 shrink-0" /><span className="truncate">Navigate Media Manager…</span><kbd className="ml-auto hidden rounded-md border border-[#2e3744] bg-[#151a21] px-1.5 py-0.5 text-[8px] text-[#aeb6c1] sm:inline">Ctrl K</kbd>
          </button>
          <div className="ml-auto flex items-center gap-2">
            <Tooltip><TooltipTrigger asChild><Button variant="ghost" size="icon" aria-label="Notifications" onClick={() => toast.info("Workflow attention appears on the overview")}><Bell /></Button></TooltipTrigger><TooltipContent>Notifications</TooltipContent></Tooltip>
          </div>
        </div>

        <main className="mx-auto w-full max-w-[1780px] p-3 pb-12 sm:p-5 lg:p-7">
          <PageHeader
            {...page}
            actions={<><Button variant="outline" size="sm" onClick={() => setCommandOpen(true)}><Search /> Search</Button>{isAddContext(path) ? <Button asChild size="sm"><Link href="/discover"><Plus /> Add media</Link></Button> : null}</>}
          />
          <div className="mt-5"><RouteContent path={path} /></div>
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
  if (path === "/wanted") return <LiveWantedScreen />
  if (normalized === "/media/movies/$mediaId" || normalized === "/media/series/$mediaId") return <LiveMediaDetailScreen mediaId={path.split("/").at(-1) ?? ""} />
  if (path === "/files") return <LiveFilesScreen />
  if (normalized === "/files/$fileId") return <LiveFileDetailScreen fileId={path.split("/").at(-1) ?? ""} />
  if (path === "/discover") return <LiveDiscoverScreen />
  if (path === "/activity/queue") return <LiveWorkflowScreen />
  if (path === "/activity/recycle") return <LiveRecycleScreen />
  if (path === "/activity/history") return <LiveActivityHistoryScreen />
  if (path === "/intelligence/releases") return <LiveReleaseIntelligenceScreen />
  if (path === "/intelligence/groups") return <LiveReleaseIntelligenceScreen mode="groups" />
  if (path === "/intelligence/evidence") return <LiveReleaseIntelligenceScreen mode="evidence" />
  if (path === "/sources/indexers") return <LiveSourcesScreen kind="newznab" />
  if (normalized === "/sources/indexers/$indexerId") return <LiveIndexerDetailScreen indexerId={path.split("/").at(-1) ?? ""} />
  if (path === "/sources/download-clients") return <LiveSourcesScreen kind="altmount" />
  if (path === "/settings/connections") return <LiveConnectionsScreen />
  if (path === "/sources/indexer-lab") return <LiveProwlarrImportScreen />
  if (path === "/settings/storage") return <LiveRootsScreen />
  if (path === "/settings/profiles") return <LiveProfilesScreen />
  if (path === "/settings/metadata") return <LiveMetadataSettingsScreen />
  if (path === "/settings/media-management") return <LiveSystemInfoScreen mode="media-management" />
  if (path === "/system/tasks") return <LiveTasksScreen />
  if (path === "/system/security") return <LiveSystemInfoScreen mode="security" />
  if (path === "/system/api") return <LiveSystemInfoScreen mode="api" />
  if (path === "/system/health") return <LiveComponentHealthScreen />
  if (path === "/system/logs") return <LiveActivityHistoryScreen logs />
  return <LiveUnavailableScreen />
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
