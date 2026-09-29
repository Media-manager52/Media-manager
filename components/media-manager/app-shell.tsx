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
import { loadSystemHealth, loadSystemInfo, loadWanted, loadWorkflows, type APIWantedItem, type APIWorkflow } from "@/lib/media-manager-api"
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
  LiveOperationalDashboardScreen,
  LiveReleaseIntelligenceScreen,
  LiveRequestsScreen,
  LiveSourcesScreen,
  LiveSystemInfoScreen,
  LiveTasksScreen,
  LiveWantedScreen,
} from "./live-operational-screens"
import { PageHeader, StateBadge } from "./ui"

type NavBadge = "attention" | "active"
type NavItem = { label: string; href: string; icon: LucideIcon; badge?: NavBadge }
type NavGroup = { label: string; items: NavItem[]; advanced?: boolean; description?: string }

/**
 * Navigation deliberately separates the everyday product from advanced tooling.
 * Every route stays reachable; only its visual weight changes. Advanced
 * observability surfaces live in the collapsible Advanced group instead of
 * competing with Movies, TV Shows, and Requests.
 */
const liveNavigation: NavGroup[] = [
  { label: "Library", description: "Everyday library work", items: [
    { label: "Overview", href: "/", icon: LayoutGrid },
    { label: "Movies", href: "/media", icon: Library },
    { label: "TV Shows", href: "/media/series", icon: Tv },
    { label: "Requests", href: "/requests", icon: Inbox },
    { label: "Wanted", href: "/wanted", icon: ListChecks, badge: "attention" },
    { label: "Calendar", href: "/calendar", icon: FileClock },
    { label: "Add media", href: "/discover", icon: Plus },
  ] },
  { label: "Operations", description: "Acquisition and files", items: [
    { label: "Workflows", href: "/activity/queue", icon: Activity, badge: "active" },
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
  const mediaDetailLayout = normalized === "/media/movies/$mediaId" || normalized === "/media/series/$mediaId"

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
    <AppSidebar path={path} />

    <SidebarInset className="min-w-0 bg-canvas">
      <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-2 border-b border-line bg-canvas/85 px-3 backdrop-blur-md supports-[backdrop-filter]:bg-canvas/70 sm:px-4">
        <SidebarTrigger className="-ml-1 text-fg-muted hover:text-fg" />
        <span className="hidden h-5 w-px bg-line md:block" aria-hidden />
        <ShellBreadcrumb normalized={normalized} page={page} />
        <button type="button" onClick={() => setCommandOpen(true)} className="ml-auto flex h-9 min-w-0 flex-1 items-center gap-2 rounded-lg border border-line bg-surface px-3 text-left text-[13px] text-fg-muted shadow-[var(--shadow-panel)] outline-none transition hover:border-line-strong hover:text-fg-secondary focus-visible:ring-2 focus-visible:ring-ring md:max-w-[300px] md:flex-none lg:w-[300px]">
          <Search className="size-4 shrink-0" /><span className="truncate">Go to page…</span><kbd className="ml-auto hidden rounded-md border border-line bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-fg-muted sm:inline">Ctrl K</kbd>
        </button>
        <div className="flex items-center gap-0.5">
          <ThemeMenu />
          <OwnerAttentionBell />
        </div>
      </header>

      <main className={cn("w-full", libraryLayout ? "h-[calc(100dvh-7rem)] min-h-0 overflow-hidden md:h-[calc(100dvh-3.5rem)]" : "mx-auto max-w-[1600px] px-4 pb-24 pt-6 sm:px-6 md:pb-12 lg:px-8 lg:pt-8")}>
        {libraryLayout || mediaDetailLayout ? <RouteContent path={path} /> : <>
          <PageHeader
            {...page}
            actions={isAddContext(path) ? <Button asChild size="sm"><Link href="/discover"><Plus /> Add media</Link></Button> : undefined}
          />
          <div className="mt-6"><RouteContent path={path} /></div>
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
  // A stack of frames: one managed library assembled from verified sources.
  return <span className={cn("grid size-8 shrink-0 place-items-center rounded-lg bg-brand text-brand-fg shadow-[0_1px_0_rgba(255,255,255,0.18)_inset]", className)} aria-hidden>
    <svg viewBox="0 0 20 20" className="size-[18px]" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="6.5" width="14" height="10" rx="2" />
      <path d="M5.5 3.5h9" opacity="0.75" />
      <path d="M8.5 9.5v4l3.5-2z" fill="currentColor" stroke="none" />
    </svg>
  </span>
}

function AppSidebar({ path }: { path: string }) {
  const { state, isMobile, setOpenMobile } = useSidebar()
  const iconMode = state === "collapsed" && !isMobile
  const { attention, activeCount } = React.useContext(OwnerAttentionContext)
  const [openGroups, setOpenGroups] = React.useState<Set<string>>(() => new Set(["Library", "Operations"]))
  const [serviceStatus, setServiceStatus] = React.useState<{ ready?: boolean; version?: string }>({})

  React.useEffect(() => {
    const controller = new AbortController()
    Promise.all([loadSystemHealth(controller.signal), loadSystemInfo(controller.signal)])
      .then(([health, info]) => setServiceStatus({ ready: health.database_state === "ready" && health.schema_version === health.expected_schema, version: info.version }))
      .catch(() => { if (!controller.signal.aborted) setServiceStatus({ ready: false }) })
    return () => controller.abort()
  }, [])

  const badgeValue = (badge?: NavBadge) => badge === "attention" ? attention.length : badge === "active" ? activeCount : 0
  const closeOnMobile = () => { if (isMobile) setOpenMobile(false) }

  return (
    <Sidebar collapsible="icon" className="border-r border-line">
      <SidebarHeader className="h-14 justify-center border-b border-line px-3 py-0 group-data-[collapsible=icon]:px-2">
        <Link href="/" onClick={closeOnMobile} className="flex min-w-0 items-center gap-2.5 rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <BrandMark />
          <div className="min-w-0 group-data-[collapsible=icon]:hidden"><div className="truncate text-[14px] font-semibold tracking-[-0.01em] text-fg">Media Manager</div><div className="truncate text-[11.5px] text-fg-muted">Owner console</div></div>
        </Link>
      </SidebarHeader>
      <SidebarContent className="gap-0 px-2 py-3 scrollbar-thin">
        {liveNavigation.map((group) => {
          const groupActive = group.items.some((item) => isRouteActive(path, item.href))
          const open = iconMode || openGroups.has(group.label) || groupActive
          return (
            <SidebarGroup key={group.label} className={cn("px-0 py-1", group.advanced && "mt-2 border-t border-line pt-3")}>
              <SidebarGroupLabel asChild className="h-7 rounded-md px-2 text-[11.5px] font-semibold tracking-[0.02em] text-fg-subtle hover:bg-surface-3 hover:text-fg-secondary">
                <button type="button" title={group.description} aria-expanded={open} onClick={() => setOpenGroups((current) => { const next = new Set(current); if (next.has(group.label)) next.delete(group.label); else next.add(group.label); return next })}><span>{group.label}</span><ChevronDown className={cn("ml-auto size-3.5 transition-transform", !open && "-rotate-90")} /></button>
              </SidebarGroupLabel>
              {open ? <SidebarGroupContent>
                <SidebarMenu className="gap-0.5">
                  {group.items.map((item) => {
                    const active = isRouteActive(path, item.href)
                    const Icon = item.icon
                    const count = badgeValue(item.badge)
                    return (
                      <SidebarMenuItem key={item.href}>
                        <SidebarMenuButton asChild isActive={active} tooltip={item.label} className={cn("h-8 rounded-md px-2 text-[13px] text-fg-secondary hover:bg-surface-3 hover:text-fg data-[active=true]:bg-brand-soft data-[active=true]:font-medium data-[active=true]:text-brand-text", group.advanced && "text-fg-muted")}>
                          <Link href={item.href} onClick={closeOnMobile}><Icon className={cn("size-4", active ? "text-brand-text" : "text-fg-subtle")} /><span>{item.label}</span></Link>
                        </SidebarMenuButton>
                        {count > 0 ? <SidebarMenuBadge className={cn("h-5 min-w-5 rounded-full px-1.5 text-[11px] font-semibold tabular-nums", item.badge === "attention" ? "bg-warn-soft text-warn" : "bg-surface-3 text-fg-secondary")} aria-label={item.badge === "attention" ? `${count} need attention` : `${count} active`}>{count}</SidebarMenuBadge> : null}
                      </SidebarMenuItem>
                    )
                  })}
                </SidebarMenu>
              </SidebarGroupContent> : null}
            </SidebarGroup>
          )
        })}
      </SidebarContent>
      <SidebarFooter className="border-t border-line p-2">
        <Link href="/system/health" onClick={closeOnMobile} title={serviceStatus.ready === undefined ? "Checking service" : serviceStatus.ready ? "Service healthy" : "Service attention"} className="flex items-center gap-2.5 rounded-md p-2 outline-none transition hover:bg-surface-3 focus-visible:ring-2 focus-visible:ring-ring group-data-[collapsible=icon]:justify-center">
          <span className={cn("relative flex size-2.5 shrink-0 rounded-full", serviceStatus.ready === undefined ? "bg-fg-subtle" : serviceStatus.ready ? "bg-good-solid" : "bg-warn-solid")}>
            {serviceStatus.ready ? <span className="absolute inset-0 animate-ping rounded-full bg-good-solid opacity-40 motion-reduce:hidden" aria-hidden /> : null}
          </span>
          <div className="min-w-0 group-data-[collapsible=icon]:hidden"><div className="truncate text-[12.5px] font-medium text-fg">{serviceStatus.ready === undefined ? "Checking service" : serviceStatus.ready ? "Service healthy" : "Service attention"}</div><div className="truncate text-[11.5px] text-fg-muted">{serviceStatus.version ? `Version ${serviceStatus.version}` : "Health & version"}</div></div>
        </Link>
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
  return path === "/" || path === "/media" || path.startsWith("/media/")
}
