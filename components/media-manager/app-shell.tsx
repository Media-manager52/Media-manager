"use client"

import * as React from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  Activity,
  AlertTriangle,
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
  LayoutGrid,
  Library,
  ListChecks,
  Menu,
  Monitor,
  Moon,
  Plus,
  Search,
  ServerCog,
  Settings2,
  ShieldCheck,
  Sparkles,
  Sun,
  TerminalSquare,
  Tv,
  Users,
  Webhook,
  type LucideIcon,
} from "lucide-react"

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
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
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
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
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar"
import { Toaster } from "@/components/ui/sonner"
import { cn } from "@/lib/utils"
import { loadSystemHealth, loadSystemInfo, loadWanted, loadWorkflows, type APISystemHealth, type APIWantedItem, type APIWorkflow } from "@/lib/media-manager-api"
import { collectOwnerAttention, isActivelyAcquiring, type OwnerAttentionEntry } from "@/lib/workflow-states"
import { normalizeRoute, routeTitles } from "@/lib/media-manager-routes"
import { parseThemePreference, resolveDarkTheme, themeStorageKey, type ThemePreference } from "@/lib/theme"
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
  LiveReleaseIntelligenceScreen,
  LiveRequestsScreen,
  LiveSourcesScreen,
  LiveSystemInfoScreen,
  LiveTasksScreen,
  LiveWantedScreen,
} from "./live-operational-screens"
import { LiveOverviewScreen } from "./overview-screen"
import { LiveProblemsScreen } from "./problems-screen"
import { PageHeader, StateBadge } from "./ui"

type NavBadge = "attention" | "active"
type NavItem = { label: string; href: string; icon: LucideIcon; badge?: NavBadge }
type NavGroup = { label: string; items: NavItem[]; advanced?: boolean; collapsed?: boolean; description?: string }

/**
 * Navigation follows the owner's day: the library first, then intake and the
 * acquisition machinery, then sources, settings, and system. Every route stays
 * reachable; settings, system, and advanced evidence start collapsed so they
 * never compete with Movies, TV Shows, and what needs attention.
 */
const liveNavigation: NavGroup[] = [
  { label: "Home", items: [
    { label: "Overview", href: "/", icon: LayoutGrid },
    { label: "Try V2", href: "/v2", icon: Sparkles },
  ] },
  { label: "Library", description: "Everyday library work", items: [
    { label: "Movies", href: "/media", icon: Library },
    { label: "TV Shows", href: "/media/series", icon: Tv },
    { label: "Calendar", href: "/calendar", icon: FileClock },
    { label: "Wanted", href: "/wanted", icon: ListChecks, badge: "attention" },
    { label: "Collections", href: "/collections", icon: BookOpenCheck },
  ] },
  { label: "Intake", description: "How media arrives", items: [
    { label: "Requests", href: "/requests", icon: Inbox },
    { label: "Add media", href: "/discover", icon: Plus },
    { label: "Adoption", href: "/import", icon: DatabaseBackup },
    { label: "Import lists", href: "/sources/import-lists", icon: ListChecks },
  ] },
  { label: "Operations", description: "Acquisition and files", items: [
    { label: "Workflows", href: "/activity/queue", icon: Activity, badge: "active" },
    { label: "Files", href: "/files", icon: Files },
    { label: "Recycle", href: "/activity/recycle", icon: ArchiveRestore },
  ] },
  { label: "Sources", description: "Where media comes from", items: [
    { label: "Indexers", href: "/sources/indexers", icon: Globe2 },
    { label: "Download clients", href: "/sources/download-clients", icon: ServerCog },
    { label: "Prowlarr import", href: "/sources/indexer-lab", icon: DatabaseBackup },
  ] },
  { label: "Settings", description: "Policy and connections", collapsed: true, items: [
    { label: "Quality profiles", href: "/settings/profiles", icon: Gauge },
    { label: "Storage", href: "/settings/storage", icon: HardDrive },
    { label: "Connections", href: "/settings/connections", icon: Webhook },
    { label: "Metadata", href: "/settings/metadata", icon: BookOpenCheck },
    { label: "Media management", href: "/settings/media-management", icon: Settings2 },
    { label: "Notifications", href: "/settings/notifications", icon: Bell },
  ] },
  { label: "System", description: "Health and access", collapsed: true, items: [
    { label: "Health", href: "/system/health", icon: HeartPulse },
    { label: "Problems", href: "/system/problems", icon: AlertTriangle },
    { label: "Tasks", href: "/system/tasks", icon: ListChecks },
    { label: "Backups", href: "/system/backups", icon: DatabaseBackup },
    { label: "Security", href: "/system/security", icon: ShieldCheck },
    { label: "API", href: "/system/api", icon: TerminalSquare },
  ] },
  { label: "Advanced", description: "Evidence and observability", advanced: true, collapsed: true, items: [
    { label: "History & evidence", href: "/activity/history", icon: FileClock },
    { label: "Logs & audit", href: "/system/logs", icon: TerminalSquare },
    { label: "Releases", href: "/intelligence/releases", icon: Sparkles },
    { label: "Release groups", href: "/intelligence/groups", icon: Users },
    { label: "Evidence", href: "/intelligence/evidence", icon: BookOpenCheck },
    { label: "Blocklist", href: "/activity/blocklist", icon: ShieldCheck },
  ] },
]

/** The four destinations an owner reaches for most, pinned on phones. */
const mobileTabs: NavItem[] = [
  { label: "Overview", href: "/", icon: LayoutGrid },
  { label: "Movies", href: "/media", icon: Library },
  { label: "TV Shows", href: "/media/series", icon: Tv },
  { label: "Requests", href: "/requests", icon: Inbox },
]

const detailTitles: Record<string, { area: string; title: string; subtitle: string }> = {
  "/media/movies/$mediaId": { area: "Library · Movies", title: "Movie details", subtitle: "Monitoring, versions, files, history, metadata, and evidence." },
  "/media/series/$mediaId": { area: "Library · TV Shows", title: "Series details", subtitle: "Season monitoring, episode coverage, pack verification, and publication." },
  "/files/$fileId": { area: "Operations · File Manager", title: "File inspector", subtitle: "Identity, provenance, tracks, links, properties, and safe operations." },
  "/sources/indexers/$indexerId": { area: "Sources · Indexers", title: "Indexer details", subtitle: "Learned capabilities, behavior, limits, evidence, and safe strategy." },
}

/** Breadcrumb parents for detail routes, so the owner can step back one level. */
const detailParents: Record<string, string> = {
  "/media/movies/$mediaId": "/media",
  "/media/series/$mediaId": "/media/series",
  "/files/$fileId": "/files",
  "/sources/indexers/$indexerId": "/sources/indexers",
}

type OwnerAttentionState = {
  attention: OwnerAttentionEntry[]
  activeCount: number
  loaded: boolean
  error: string
  refresh: () => void
}

const OwnerAttentionContext = React.createContext<OwnerAttentionState>({ attention: [], activeCount: 0, loaded: false, error: "", refresh: () => undefined })

/**
 * Loads the owner-attention read models once for the whole shell, so the bell
 * and the navigation counts agree and are visible before anything is opened.
 */
function OwnerAttentionProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = React.useState<APIWantedItem[]>([])
  const [workflows, setWorkflows] = React.useState<APIWorkflow[]>([])
  const [loaded, setLoaded] = React.useState(false)
  const [error, setError] = React.useState("")
  const [generation, setGeneration] = React.useState(0)
  React.useEffect(() => {
    const controller = new AbortController()
    Promise.all([loadWanted(controller.signal), loadWorkflows(true, controller.signal)])
      .then(([nextItems, nextWorkflows]) => { setItems(nextItems); setWorkflows(nextWorkflows); setError(""); setLoaded(true) })
      .catch((reason: unknown) => { if (!controller.signal.aborted) { setError(reason instanceof Error ? reason.message : "Could not load owner attention"); setLoaded(true) } })
    return () => controller.abort()
  }, [generation])
  const value = React.useMemo<OwnerAttentionState>(() => ({
    attention: collectOwnerAttention(items, workflows),
    activeCount: workflows.filter((workflow) => isActivelyAcquiring(workflow.state)).length,
    loaded,
    error,
    refresh: () => setGeneration((current) => current + 1),
  }), [items, workflows, loaded, error])
  return <OwnerAttentionContext.Provider value={value}>{children}</OwnerAttentionContext.Provider>
}

export function MediaManagerApp({ path }: { path: string }) {
  return (
    <SidebarProvider style={{ "--sidebar-width": "15rem", "--sidebar-width-icon": "3.5rem" } as React.CSSProperties}>
      <OwnerAttentionProvider>
        <ShellLayout path={path} />
      </OwnerAttentionProvider>
    </SidebarProvider>
  )
}

function ShellLayout({ path }: { path: string }) {
  const router = useRouter()
  const normalized = normalizeRoute(path)
  const page = detailTitles[normalized] ?? routeTitles[normalized] ?? { area: "Media Manager", title: "Not found", subtitle: "This route is not in the locked screen map." }
  const [commandOpen, setCommandOpen] = React.useState(false)
  const libraryLayout = path === "/media" || path === "/media/series"
  // These screens render their own header (actions, hero, or live date line).
  const mediaDetailLayout = path === "/" || normalized === "/media/movies/$mediaId" || normalized === "/media/series/$mediaId"

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
  }, [router])

  return <>
    <AppSidebar path={path} onSearch={() => setCommandOpen(true)} />

    <SidebarInset className="min-w-0 bg-canvas">
      <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-2 bg-canvas/85 px-3 backdrop-blur-md supports-[backdrop-filter]:bg-canvas/70 sm:px-5 lg:px-9">
        <SidebarTrigger className="-ml-1 text-fg-muted hover:text-fg" />
        <span className="hidden h-4 w-px bg-line-strong md:block" aria-hidden />
        <ShellBreadcrumb normalized={normalized} page={page} />
        <div className="ml-auto flex items-center gap-0.5">
          <Button variant="ghost" size="icon" aria-label="Go to page…" className="text-fg-muted hover:text-fg" onClick={() => setCommandOpen(true)}><Search /></Button>
          <ThemeMenu />
          <OwnerAttentionBell />
        </div>
      </header>

      <main className={cn("w-full", libraryLayout ? "h-[calc(100dvh-7rem)] min-h-0 overflow-hidden md:h-[calc(100dvh-3.5rem)]" : "mx-auto max-w-[1560px] px-4 pb-24 pt-3 sm:px-5 md:pb-12 lg:px-9 lg:pt-4 min-[2200px]:max-w-[2040px]")}>
        {libraryLayout || mediaDetailLayout ? <RouteContent path={path} /> : <>
          <PageHeader
            {...page}
            actions={isAddContext(path) ? <Button asChild size="sm"><Link href="/discover"><Plus /> Add media</Link></Button> : undefined}
          />
          <div className="mt-5"><RouteContent path={path} /></div>
        </>}
      </main>
      <MobileTabBar path={path} />
    </SidebarInset>

    <CommandDialog open={commandOpen} onOpenChange={setCommandOpen} title="Go to page" description="Jump to a page in Media Manager">
      <CommandInput placeholder="Go to page…" />
      <CommandList><CommandEmpty>No matching page.</CommandEmpty>{liveNavigation.map((group) => <CommandGroup key={group.label} heading={group.advanced ? `${group.label} · advanced` : group.label}>{group.items.map((item) => { const Icon = item.icon; return <CommandItem key={item.href} value={`${group.label} ${item.label}`} onSelect={() => navigate(item.href)}><Icon /><span>{item.label}</span><CommandShortcut>{group.label}</CommandShortcut></CommandItem> })}</CommandGroup>)}</CommandList>
    </CommandDialog>
    <Toaster position="bottom-right" />
  </>
}

function BrandMark({ className }: { className?: string }) {
  return <span className={cn("grid size-[26px] shrink-0 place-items-center rounded-[7px] bg-[linear-gradient(135deg,#7c8cff,#4e5ba8)] text-[11px] font-extrabold text-[#0b0d11]", className)} aria-hidden>MM</span>
}

type ServiceStatus = { loaded: boolean; ready: boolean; version?: string; warnings: Array<{ name: string; reason: string }> }

/** One sentence per degraded component or unreadable AltMount output mount. */
function componentWarnings(health: APISystemHealth) {
  const mounts = (health.mounts ?? []).filter((mount) => mount.state !== "available").map((mount) => ({ name: mount.name, reason: "read-only output mount unavailable" }))
  return [...mounts, ...componentSourceWarnings(health.components ?? [])]
}

function componentSourceWarnings(components: APISystemHealth["components"]) {
  return components.filter((component) => component.enabled && (component.configuration_state !== "configured" || component.consecutive_failure > 0)).map((component) => ({
    name: component.name,
    reason: component.configuration_state === "awaiting_commission" ? "awaiting commission" : component.configuration_state === "disabled" ? "disabled" : component.last_outcome ? component.last_outcome.replaceAll("_", " ") : `${component.consecutive_failure} failed attempts`,
  }))
}

function AppSidebar({ path, onSearch }: { path: string; onSearch: () => void }) {
  const { state, isMobile, setOpenMobile } = useSidebar()
  const iconMode = state === "collapsed" && !isMobile
  const { attention, activeCount } = React.useContext(OwnerAttentionContext)
  const [openGroups, setOpenGroups] = React.useState<Set<string>>(() => new Set(liveNavigation.filter((group) => !group.collapsed).map((group) => group.label)))
  const [service, setService] = React.useState<ServiceStatus>({ loaded: false, ready: false, warnings: [] })

  React.useEffect(() => {
    const controller = new AbortController()
    Promise.all([loadSystemHealth(controller.signal), loadSystemInfo(controller.signal)])
      .then(([health, info]) => setService({ loaded: true, ready: health.database_state === "ready" && health.schema_version === health.expected_schema, version: info.version, warnings: componentWarnings(health) }))
      .catch(() => { if (!controller.signal.aborted) setService({ loaded: true, ready: false, warnings: [] }) })
    return () => controller.abort()
  }, [])

  const badgeValue = (badge?: NavBadge) => badge === "attention" ? attention.length : badge === "active" ? activeCount : 0
  const closeOnMobile = () => { if (isMobile) setOpenMobile(false) }
  const warningCount = service.warnings.length + (service.loaded && !service.ready ? 1 : 0)
  const statusTitle = !service.loaded ? "Checking system" : warningCount ? `${warningCount} system warning${warningCount === 1 ? "" : "s"}` : "All systems healthy"
  const statusDetail = !service.loaded ? "Health & version" : !service.ready ? "Database or schema needs attention" : service.warnings[0] ? `${service.warnings[0].name} · ${service.warnings[0].reason}` : "Database, worker, and sources"

  return (
    <Sidebar collapsible="icon" className="border-r border-line">
      <SidebarHeader className="gap-3 px-3 pb-2 pt-3.5 group-data-[collapsible=icon]:px-2">
        <Link href="/" onClick={closeOnMobile} className="flex min-w-0 items-center gap-2.5 rounded-lg px-1 outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <BrandMark />
          <div className="min-w-0 group-data-[collapsible=icon]:hidden"><div className="truncate text-[13.5px] font-bold tracking-[-0.01em] text-fg">Media Manager</div><div className="truncate text-[9.5px] font-semibold tracking-[0.14em] text-fg-subtle">OWNER CONSOLE</div></div>
        </Link>
        <button type="button" onClick={() => { closeOnMobile(); onSearch() }} className="flex h-[34px] items-center gap-2 rounded-lg border border-line bg-surface-2 px-2.5 text-left text-[12.5px] text-fg-subtle outline-none transition hover:border-line-strong hover:text-fg-muted focus-visible:ring-2 focus-visible:ring-ring group-data-[collapsible=icon]:hidden">
          <Search className="size-3.5 shrink-0" aria-hidden /><span className="truncate">Search and go to page…</span><kbd className="ml-auto rounded-[5px] border border-line-strong bg-surface px-1.5 py-px text-[10px] font-semibold text-fg-subtle">⌘K</kbd>
        </button>
      </SidebarHeader>
      <SidebarContent className="gap-0 px-2 pb-3 scrollbar-thin">
        {liveNavigation.map((group) => {
          const groupActive = group.items.some((item) => isRouteActive(path, item.href))
          const open = iconMode || openGroups.has(group.label) || groupActive
          const headless = group.label === "Home"
          return (
            <SidebarGroup key={group.label} className={cn("px-0 py-0", !headless && "pt-2.5")}>
              {headless ? null : <SidebarGroupLabel asChild className="h-6 rounded-md px-2.5 text-[10px] font-bold uppercase tracking-[0.12em] text-fg-subtle hover:text-fg-muted">
                <button type="button" title={group.description} aria-expanded={open} onClick={() => setOpenGroups((current) => { const next = new Set(current); if (next.has(group.label)) next.delete(group.label); else next.add(group.label); return next })}><span>{group.label}</span><ChevronDown className={cn("ml-auto size-3 opacity-60 transition-transform", !open && "-rotate-90")} /></button>
              </SidebarGroupLabel>}
              {open ? <SidebarGroupContent>
                <SidebarMenu className="gap-px">
                  {group.items.map((item) => {
                    const active = isRouteActive(path, item.href)
                    const Icon = item.icon
                    const count = badgeValue(item.badge)
                    return (
                      <SidebarMenuItem key={item.href}>
                        <SidebarMenuButton asChild isActive={active} tooltip={item.label} className={cn("h-[34px] rounded-lg px-2.5 text-[13px] font-medium text-fg-muted hover:bg-surface-2 hover:text-fg data-[active=true]:bg-brand-soft data-[active=true]:font-medium data-[active=true]:text-fg", group.advanced && "text-[12.5px] text-fg-subtle")}>
                          <Link href={item.href} onClick={closeOnMobile}><Icon className={cn("size-4", active ? "text-brand" : "text-fg-subtle")} /><span>{item.label}</span></Link>
                        </SidebarMenuButton>
                        {count > 0 ? <SidebarMenuBadge className={cn("h-[18px] min-w-5 rounded-full border px-1.5 text-[10.5px] font-bold tabular-nums", item.badge === "attention" ? "border-warn-line bg-warn-soft text-warn" : "border-brand-line bg-brand-soft text-brand-text")} aria-label={item.badge === "attention" ? `${count} need attention` : `${count} active`}>{count}</SidebarMenuBadge> : null}
                      </SidebarMenuItem>
                    )
                  })}
                </SidebarMenu>
              </SidebarGroupContent> : null}
            </SidebarGroup>
          )
        })}
      </SidebarContent>
      <SidebarFooter className="border-t border-line p-2.5">
        <Link href="/system/health" onClick={closeOnMobile} title={statusTitle} className={cn("flex items-center gap-2.5 rounded-lg border bg-surface-2 px-2.5 py-2 outline-none transition focus-visible:ring-2 focus-visible:ring-ring group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:border-0 group-data-[collapsible=icon]:bg-transparent group-data-[collapsible=icon]:p-2", warningCount ? "border-warn-line hover:border-warn" : "border-line hover:border-line-strong")}>
          <span className={cn("size-2 shrink-0 rounded-full", !service.loaded ? "bg-fg-subtle" : warningCount ? "bg-warn-solid shadow-[0_0_8px_var(--warn)]" : "bg-good-solid shadow-[0_0_8px_var(--good)]")} />
          <div className="min-w-0 group-data-[collapsible=icon]:hidden"><div className={cn("truncate text-[11.5px] font-semibold", warningCount ? "text-warn" : "text-fg")}>{statusTitle}</div><div className="truncate text-[10.5px] text-fg-subtle">{statusDetail}</div></div>
        </Link>
        <div className="truncate text-center text-[10px] text-fg-subtle group-data-[collapsible=icon]:hidden">{service.version ? `Media Manager ${service.version} · self-hosted` : "Media Manager · self-hosted"}</div>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

function ShellBreadcrumb({ normalized, page }: { normalized: string; page: { area: string; title: string } }) {
  const trail = page.area.split(" · ").filter((part) => part && part !== page.title)
  const parent = detailParents[normalized]
  return <Breadcrumb className="hidden min-w-0 md:block">
    <BreadcrumbList className="flex-nowrap text-[13px]">
      {trail.map((part, index) => <React.Fragment key={part}>
        <BreadcrumbItem className="shrink-0">
          {parent && index === trail.length - 1 ? <BreadcrumbLink asChild><Link href={parent}>{part}</Link></BreadcrumbLink> : <span className="text-fg-muted">{part}</span>}
        </BreadcrumbItem>
        <BreadcrumbSeparator />
      </React.Fragment>)}
      <BreadcrumbItem className="min-w-0"><BreadcrumbPage className="truncate font-medium text-fg">{page.title}</BreadcrumbPage></BreadcrumbItem>
    </BreadcrumbList>
  </Breadcrumb>
}

function MobileTabBar({ path }: { path: string }) {
  const { setOpenMobile } = useSidebar()
  const { attention } = React.useContext(OwnerAttentionContext)
  return <nav aria-label="Mobile navigation" className="fixed inset-x-0 bottom-0 z-40 grid h-14 grid-cols-5 border-t border-line bg-surface/95 pb-[env(safe-area-inset-bottom)] backdrop-blur-md md:hidden">
    {mobileTabs.map((item) => {
      const active = isRouteActive(path, item.href)
      const Icon = item.icon
      return <Link key={item.href} href={item.href} aria-current={active ? "page" : undefined} className={cn("flex flex-col items-center justify-center gap-0.5 text-[11px] font-medium outline-none focus-visible:bg-surface-3", active ? "text-brand-text" : "text-fg-muted")}><Icon className="size-[18px]" aria-hidden />{item.label}</Link>
    })}
    <button type="button" onClick={() => setOpenMobile(true)} className="relative flex flex-col items-center justify-center gap-0.5 text-[11px] font-medium text-fg-muted outline-none focus-visible:bg-surface-3">
      <Menu className="size-[18px]" aria-hidden />More
      {attention.length ? <span className="absolute right-[calc(50%-16px)] top-2 size-2 rounded-full bg-warn-solid" aria-hidden /> : null}
    </button>
  </nav>
}

const themeIcons: Record<ThemePreference, LucideIcon> = { dark: Moon, light: Sun, system: Monitor }

const themeListeners = new Set<() => void>()

function readThemePreference(): ThemePreference {
  try { return parseThemePreference(window.localStorage.getItem(themeStorageKey)) } catch { return "dark" }
}

function subscribeThemePreference(listener: () => void) {
  themeListeners.add(listener)
  window.addEventListener("storage", listener)
  return () => { themeListeners.delete(listener); window.removeEventListener("storage", listener) }
}

function ThemeMenu() {
  // The server always renders the dark default; the stored preference is read
  // after hydration without a mismatch.
  const preference = React.useSyncExternalStore(subscribeThemePreference, readThemePreference, () => "dark" as ThemePreference)
  React.useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)")
    const apply = () => document.documentElement.classList.toggle("dark", resolveDarkTheme(preference, media.matches))
    apply()
    if (preference !== "system") return
    media.addEventListener("change", apply)
    return () => media.removeEventListener("change", apply)
  }, [preference])
  function choose(value: string) {
    try { window.localStorage.setItem(themeStorageKey, parseThemePreference(value)) } catch { /* Storage may be unavailable. */ }
    document.documentElement.classList.toggle("dark", resolveDarkTheme(parseThemePreference(value), window.matchMedia("(prefers-color-scheme: dark)").matches))
    themeListeners.forEach((listener) => listener())
  }
  const Icon = themeIcons[preference]
  return <DropdownMenu>
    <DropdownMenuTrigger asChild>
      <Button variant="ghost" size="icon" aria-label="Theme" className="text-fg-muted hover:text-fg"><Icon /></Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" className="w-40">
      <DropdownMenuLabel className="text-[12px] text-fg-muted">Theme</DropdownMenuLabel>
      <DropdownMenuSeparator />
      <DropdownMenuRadioGroup value={preference} onValueChange={choose}>
        <DropdownMenuRadioItem value="dark"><Moon /> Dark</DropdownMenuRadioItem>
        <DropdownMenuRadioItem value="light"><Sun /> Light</DropdownMenuRadioItem>
        <DropdownMenuRadioItem value="system"><Monitor /> System</DropdownMenuRadioItem>
      </DropdownMenuRadioGroup>
    </DropdownMenuContent>
  </DropdownMenu>
}

function OwnerAttentionBell() {
  const [open, setOpen] = React.useState(false)
  const { attention, error, refresh } = React.useContext(OwnerAttentionContext)
  const shown = attention.slice(0, 5)
  function onOpenChange(next: boolean) {
    setOpen(next)
    if (next) refresh()
  }
  return <Popover open={open} onOpenChange={onOpenChange}>
    <PopoverTrigger asChild>
      <Button variant="ghost" size="icon" aria-label="Owner attention" className="relative text-fg-muted hover:text-fg">
        <Bell />
        {attention.length ? <span className="absolute -right-0.5 -top-0.5 grid h-4 min-w-4 place-items-center rounded-full bg-warn-solid px-1 text-[10px] font-bold leading-none text-canvas tabular-nums ring-2 ring-canvas" aria-hidden>{attention.length > 9 ? "9+" : attention.length}</span> : null}
      </Button>
    </PopoverTrigger>
    <PopoverContent align="end" className="w-[360px] p-0">
      <div className="flex items-center justify-between gap-2 border-b border-line px-4 py-3">
        <span className="text-[13px] font-semibold text-fg">Owner attention</span>
        {attention.length ? <span className="rounded-full border border-warn-line bg-warn-soft px-2 py-0.5 text-[11px] font-semibold text-warn">{attention.length}</span> : null}
      </div>
      {error ? <div className="px-4 py-4 text-[13px] text-bad">{error}</div>
        : shown.length === 0 ? <div className="px-4 py-5 text-[13px] leading-5 text-fg-muted">Nothing needs your attention. Waiting and monitoring continue automatically.</div>
        : <div className="max-h-[360px] divide-y divide-line overflow-y-auto">{shown.map((entry) => <Link key={entry.id} href={entry.href} onClick={() => setOpen(false)} className="block px-4 py-3 outline-none transition hover:bg-surface-2 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring">
          <div className="flex items-center justify-between gap-2"><span className="truncate text-[13px] font-medium text-fg">{entry.title}{entry.year ? ` (${entry.year})` : ""}</span><StateBadge state={entry.state} /></div>
          <div className="mt-1 line-clamp-2 text-[12px] leading-[18px] text-warn">{entry.reason}</div>
          <div className="mt-1 truncate font-mono text-[11px] text-fg-subtle">{entry.variant}</div>
        </Link>)}</div>}
      <div className="border-t border-line p-2">
        <Button asChild variant="ghost" size="sm" className="w-full justify-center" onClick={() => setOpen(false)}><Link href="/wanted">View all in Wanted</Link></Button>
      </div>
    </PopoverContent>
  </Popover>
}

function RouteContent({ path }: { path: string }) {
  const normalized = normalizeRoute(path)
  if (path === "/") return <LiveOverviewScreen />
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
  if (path === "/system/problems") return <LiveProblemsScreen />
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
  return path === "/" || path === "/media" || path.startsWith("/media/")
}
