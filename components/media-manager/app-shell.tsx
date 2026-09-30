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
  DatabaseBackup,
  FileClock,
  Files,
  Gauge,
  Globe2,
  HardDrive,
  HeartPulse,
  Home,
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
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Toaster } from "@/components/ui/sonner"
import { cn } from "@/lib/utils"
import { loadWanted, loadWorkflows, type APIWantedItem, type APIWorkflow } from "@/lib/media-manager-api"
import { collectOwnerAttention, isActivelyAcquiring, type OwnerAttentionEntry } from "@/lib/workflow-states"
import { normalizeRoute, routeTitles } from "@/lib/media-manager-routes"
import { parseThemePreference, resolveDarkTheme, themeStorageKey, type ThemePreference } from "@/lib/theme"
import { HomeFeed } from "./home-feed"
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
import { StateBadge } from "./ui"

type NavBadge = "attention" | "active"
type NavItem = { label: string; href: string; icon: LucideIcon; badge?: NavBadge }
type NavGroup = { label: string; description: string; items: NavItem[] }

/**
 * Primary destinations sit in the top bar. Everything else is one click away
 * in the More menu, grouped by section, and each section page shows its
 * siblings as tabs. Every route stays reachable.
 */
const primaryNavigation: NavItem[] = [
  { label: "Home", href: "/", icon: Home },
  { label: "Movies", href: "/media", icon: Library },
  { label: "TV Shows", href: "/media/series", icon: Tv },
  { label: "Requests", href: "/requests", icon: Inbox },
  { label: "Wanted", href: "/wanted", icon: ListChecks, badge: "attention" },
  { label: "Control room", href: "/activity/queue", icon: Activity, badge: "active" },
]

const sectionNavigation: NavGroup[] = [
  { label: "Library", description: "Everyday library work", items: [
    { label: "Add media", href: "/discover", icon: Plus },
    { label: "Calendar", href: "/calendar", icon: CalendarDays },
    { label: "Collections", href: "/collections", icon: BookOpenCheck },
  ] },
  { label: "Operations", description: "Acquisition and files", items: [
    { label: "Control room", href: "/activity/queue", icon: Activity },
    { label: "File Manager", href: "/files", icon: Files },
    { label: "Adoption", href: "/import", icon: DatabaseBackup },
    { label: "Recycle", href: "/activity/recycle", icon: ArchiveRestore },
    { label: "History", href: "/activity/history", icon: FileClock },
    { label: "Blocklist", href: "/activity/blocklist", icon: ShieldCheck },
  ] },
  { label: "Intelligence", description: "Release evidence", items: [
    { label: "Releases", href: "/intelligence/releases", icon: Sparkles },
    { label: "Release groups", href: "/intelligence/groups", icon: Users },
    { label: "Evidence", href: "/intelligence/evidence", icon: BookOpenCheck },
  ] },
  { label: "Sources", description: "Where media comes from", items: [
    { label: "Indexers", href: "/sources/indexers", icon: Globe2 },
    { label: "Download clients", href: "/sources/download-clients", icon: ServerCog },
    { label: "Import lists", href: "/sources/import-lists", icon: ListChecks },
    { label: "Prowlarr import", href: "/sources/indexer-lab", icon: DatabaseBackup },
  ] },
  { label: "Settings", description: "Policy and connections", items: [
    { label: "Profiles", href: "/settings/profiles", icon: Gauge },
    { label: "Storage", href: "/settings/storage", icon: HardDrive },
    { label: "Metadata", href: "/settings/metadata", icon: BookOpenCheck },
    { label: "Media management", href: "/settings/media-management", icon: Settings2 },
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
]

/** Phones and tablets: the four everyday destinations plus More. */
const mobileTabs: NavItem[] = [
  { label: "Home", href: "/", icon: Home },
  { label: "Movies", href: "/media", icon: Library },
  { label: "TV Shows", href: "/media/series", icon: Tv },
  { label: "Requests", href: "/requests", icon: Inbox },
]

const detailTitles: Record<string, { area: string; title: string; subtitle: string }> = {
  "/media/movies/$mediaId": { area: "Movies", title: "Movie details", subtitle: "Monitoring, versions, files, history, metadata, and evidence." },
  "/media/series/$mediaId": { area: "TV Shows", title: "Series details", subtitle: "Season monitoring, episode coverage, pack verification, and publication." },
  "/files/$fileId": { area: "Operations", title: "File inspector", subtitle: "Identity, provenance, tracks, links, properties, and safe operations." },
  "/sources/indexers/$indexerId": { area: "Sources", title: "Indexer details", subtitle: "Learned capabilities, behavior, limits, evidence, and safe strategy." },
}

/** Detail pages step back to their list. */
const detailParents: Record<string, { href: string; label: string }> = {
  "/files/$fileId": { href: "/files", label: "File Manager" },
  "/sources/indexers/$indexerId": { href: "/sources/indexers", label: "Indexers" },
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
  return <OwnerAttentionProvider><ShellLayout path={path} /></OwnerAttentionProvider>
}

function sectionFor(path: string): NavGroup | undefined {
  return sectionNavigation.find((group) => group.items.some((item) => isRouteActive(path, item.href)))
}

function ShellLayout({ path }: { path: string }) {
  const router = useRouter()
  const normalized = normalizeRoute(path)
  const page = detailTitles[normalized] ?? routeTitles[normalized] ?? { area: "Media Manager", title: "Not found", subtitle: "This route is not in the locked screen map." }
  const [commandOpen, setCommandOpen] = React.useState(false)
  const [moreOpen, setMoreOpen] = React.useState(false)
  const libraryLayout = path === "/media" || path === "/media/series"
  const immersive = path === "/" || normalized === "/media/movies/$mediaId" || normalized === "/media/series/$mediaId"
  const section = sectionFor(path)
  const parent = detailParents[normalized]

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

  return <div className="min-h-dvh bg-canvas">
    <a href="#main" className="sr-only z-50 rounded-md bg-brand px-3 py-2 text-brand-fg focus:not-sr-only focus:fixed focus:left-3 focus:top-3">Skip to content</a>
    <TopBar path={path} immersive={immersive} onSearch={() => setCommandOpen(true)} onMore={() => setMoreOpen(true)} />

    <main id="main" className={cn(libraryLayout ? "h-[calc(100dvh-7.5rem)] min-h-0 overflow-hidden lg:h-[calc(100dvh-4rem)]" : immersive ? "pb-24 lg:pb-16" : "mx-auto w-full max-w-[1480px] px-4 pb-24 pt-8 sm:px-6 lg:px-10 lg:pb-16")}>
      {libraryLayout || immersive ? <RouteContent path={path} /> : <>
        <header>
          {parent ? <Link href={parent.href} className="text-[13px] font-medium text-fg-muted hover:text-fg">← {parent.label}</Link> : <div className="text-[13px] font-medium text-brand-text">{section?.label ?? page.area}</div>}
          <div className="mt-1.5 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
            <div className="min-w-0">
              <h1 className="text-[32px] font-semibold leading-[1.1] tracking-[-0.03em] text-fg sm:text-[38px]">{page.title}</h1>
              <p className="mt-2 max-w-3xl text-[15px] leading-6 text-fg-muted">{page.subtitle}</p>
            </div>
          </div>
          {section && !parent ? <SectionTabs group={section} path={path} /> : null}
        </header>
        <div className={cn(section && !parent ? "mt-6" : "mt-8")}><RouteContent path={path} /></div>
      </>}
    </main>

    <MobileTabBar path={path} onMore={() => setMoreOpen(true)} />
    <MoreSheet open={moreOpen} onOpenChange={setMoreOpen} path={path} />

    <CommandDialog open={commandOpen} onOpenChange={setCommandOpen} title="Go to page" description="Jump to a page in Media Manager">
      <CommandInput placeholder="Go to page…" />
      <CommandList>
        <CommandEmpty>No matching page.</CommandEmpty>
        <CommandGroup heading="Browse">{primaryNavigation.map((item) => { const Icon = item.icon; return <CommandItem key={item.href} value={`Browse ${item.label}`} onSelect={() => navigate(item.href)}><Icon /><span>{item.label}</span></CommandItem> })}</CommandGroup>
        {sectionNavigation.map((group) => <CommandGroup key={group.label} heading={group.label}>{group.items.map((item) => { const Icon = item.icon; return <CommandItem key={item.href} value={`${group.label} ${item.label}`} onSelect={() => navigate(item.href)}><Icon /><span>{item.label}</span><CommandShortcut>{group.label}</CommandShortcut></CommandItem> })}</CommandGroup>)}
      </CommandList>
    </CommandDialog>
    <Toaster position="bottom-right" />
  </div>
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

function TopBar({ path, immersive, onSearch, onMore }: { path: string; immersive: boolean; onSearch: () => void; onMore: () => void }) {
  const { attention, activeCount } = React.useContext(OwnerAttentionContext)
  const count = (badge?: NavBadge) => badge === "attention" ? attention.length : badge === "active" ? activeCount : 0
  return <header className={cn("sticky top-0 z-40 border-b backdrop-blur-xl", immersive ? "border-transparent bg-gradient-to-b from-canvas/85 to-canvas/30" : "border-line bg-canvas/80 supports-[backdrop-filter]:bg-canvas/65")}>
    <div className="mx-auto flex h-16 w-full max-w-[1480px] items-center gap-3 px-4 sm:px-6 lg:px-10">
      <Link href="/" className="flex shrink-0 items-center gap-2.5 rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label="Media Manager home">
        <BrandMark />
        <span className="hidden text-[15px] font-semibold tracking-[-0.01em] text-fg sm:inline">Media Manager</span>
      </Link>
      <nav aria-label="Primary" className="ml-4 hidden items-center gap-1 lg:flex">
        {primaryNavigation.map((item) => {
          const active = isRouteActive(path, item.href)
          const value = count(item.badge)
          return <Link key={item.href} href={item.href} aria-current={active ? "page" : undefined} className={cn("relative flex h-9 items-center gap-1.5 rounded-full px-3.5 text-[14px] font-medium outline-none transition focus-visible:ring-2 focus-visible:ring-ring", active ? "bg-fg text-canvas" : "text-fg-secondary hover:bg-surface-3 hover:text-fg")}>
            {item.label}
            {value > 0 ? <span className={cn("grid h-5 min-w-5 place-items-center rounded-full px-1.5 text-[11px] font-semibold tabular-nums", active ? "bg-canvas/20 text-canvas" : item.badge === "attention" ? "bg-warn-soft text-warn" : "bg-surface-3 text-fg-secondary")} aria-label={item.badge === "attention" ? `${value} need attention` : `${value} active`}>{value}</span> : null}
          </Link>
        })}
      </nav>
      <div className="ml-auto flex items-center gap-1">
        <button type="button" onClick={onSearch} aria-label="Go to page…" className="flex h-9 items-center gap-2 rounded-full border border-line bg-surface px-3 text-[13px] text-fg-muted outline-none transition hover:border-line-strong hover:text-fg-secondary focus-visible:ring-2 focus-visible:ring-ring xl:w-56">
          <Search className="size-4 shrink-0" /><span className="hidden truncate xl:inline">Go to page…</span><kbd className="ml-auto hidden rounded-md border border-line bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-fg-muted xl:inline">Ctrl K</kbd>
        </button>
        <Button asChild size="sm" className="ml-1 hidden h-9 rounded-full px-4 sm:inline-flex"><Link href="/discover"><Plus /> Add</Link></Button>
        <ThemeMenu />
        <OwnerAttentionBell />
        <MoreMenu path={path} onOpenSheet={onMore} />
      </div>
    </div>
  </header>
}

function SectionTabs({ group, path }: { group: NavGroup; path: string }) {
  return <nav aria-label={`${group.label} pages`} className="mt-6 flex gap-1 overflow-x-auto border-b border-line scrollbar-none">
    {group.items.map((item) => {
      const active = isRouteActive(path, item.href)
      return <Link key={item.href} href={item.href} aria-current={active ? "page" : undefined} className={cn("-mb-px shrink-0 border-b-2 px-3 py-2.5 text-[14px] font-medium outline-none transition focus-visible:ring-2 focus-visible:ring-ring", active ? "border-brand text-fg" : "border-transparent text-fg-muted hover:text-fg")}>{item.label}</Link>
    })}
  </nav>
}

/** Desktop: a grouped dropdown of every secondary page. Below lg the bottom bar's More opens the sheet instead. */
function MoreMenu({ path, onOpenSheet }: { path: string; onOpenSheet: () => void }) {
  const router = useRouter()
  return <>
    <Button variant="ghost" size="icon" aria-label="More pages" className="text-fg-muted hover:text-fg lg:hidden" onClick={onOpenSheet}><Menu /></Button>
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label="All pages" className="hidden text-fg-muted hover:text-fg lg:inline-flex"><LayoutGrid /></Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-[640px] p-3">
        <div className="grid grid-cols-3 gap-x-4 gap-y-3">
          {sectionNavigation.map((group) => <DropdownMenuGroup key={group.label}>
            <DropdownMenuLabel className="px-2 text-[12px] font-semibold text-fg-muted">{group.label}</DropdownMenuLabel>
            {group.items.map((item) => { const Icon = item.icon; const active = isRouteActive(path, item.href); return <DropdownMenuItem key={item.href} onSelect={() => router.push(item.href)} className={cn("gap-2 rounded-md text-[13px]", active && "bg-brand-soft text-brand-text")}><Icon className="size-4" />{item.label}</DropdownMenuItem> })}
          </DropdownMenuGroup>)}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  </>
}

// Pages the mobile tab bar has no room for; the sheet lists each page once.
const sheetBrowse = [primaryNavigation[4], primaryNavigation[5]]

function MoreSheet({ open, onOpenChange, path }: { open: boolean; onOpenChange: (open: boolean) => void; path: string }) {
  const { attention, activeCount } = React.useContext(OwnerAttentionContext)
  return <Sheet open={open} onOpenChange={onOpenChange}>
    <SheetContent side="right" className="w-[320px] overflow-y-auto bg-surface p-0 sm:max-w-[360px]">
      <SheetHeader className="border-b border-line p-4">
        <SheetTitle asChild><Link href="/" onClick={() => onOpenChange(false)} className="flex items-center gap-2.5"><BrandMark /><span>Media Manager</span></Link></SheetTitle>
        <SheetDescription className="text-fg-muted">Every page, grouped by section.</SheetDescription>
      </SheetHeader>
      <nav aria-label="All pages" className="space-y-5 p-4">
        <div>
          <div className="px-2 pb-1 text-[12px] font-semibold text-fg-muted">Browse</div>
          {sheetBrowse.map((item) => { const Icon = item.icon; const value = item.badge === "attention" ? attention.length : activeCount; return <Link key={item.href} href={item.href} onClick={() => onOpenChange(false)} aria-current={isRouteActive(path, item.href) ? "page" : undefined} className={cn("flex items-center gap-2.5 rounded-md px-2 py-2 text-[14px] text-fg-secondary hover:bg-surface-3", isRouteActive(path, item.href) && "bg-brand-soft text-brand-text")}><Icon className="size-4" />{item.label}{value ? <span className="ml-auto rounded-full bg-surface-3 px-1.5 text-[11px] font-semibold tabular-nums">{value}</span> : null}</Link> })}
        </div>
        {sectionNavigation.map((group) => <div key={group.label}>
          <div className="px-2 pb-1 text-[12px] font-semibold text-fg-muted">{group.label}</div>
          {group.items.filter((item) => !sheetBrowse.some((entry) => entry.href === item.href)).map((item) => { const Icon = item.icon; const active = isRouteActive(path, item.href); return <Link key={item.href} href={item.href} onClick={() => onOpenChange(false)} aria-current={active ? "page" : undefined} className={cn("flex items-center gap-2.5 rounded-md px-2 py-2 text-[14px] text-fg-secondary hover:bg-surface-3", active && "bg-brand-soft text-brand-text")}><Icon className="size-4" />{item.label}</Link> })}
        </div>)}
      </nav>
    </SheetContent>
  </Sheet>
}

function MobileTabBar({ path, onMore }: { path: string; onMore: () => void }) {
  const { attention } = React.useContext(OwnerAttentionContext)
  return <nav aria-label="Mobile navigation" className="fixed inset-x-0 bottom-0 z-40 grid h-16 grid-cols-5 border-t border-line bg-surface/90 pb-[env(safe-area-inset-bottom)] backdrop-blur-xl lg:hidden">
    {mobileTabs.map((item) => {
      const active = isRouteActive(path, item.href)
      const Icon = item.icon
      return <Link key={item.href} href={item.href} aria-current={active ? "page" : undefined} className={cn("flex flex-col items-center justify-center gap-1 text-[11px] font-medium outline-none focus-visible:bg-surface-3", active ? "text-fg" : "text-fg-muted")}><Icon className={cn("size-5", active && "text-brand-text")} aria-hidden />{item.label}</Link>
    })}
    <button type="button" onClick={onMore} className="relative flex flex-col items-center justify-center gap-1 text-[11px] font-medium text-fg-muted outline-none focus-visible:bg-surface-3">
      <Menu className="size-5" aria-hidden />More
      {attention.length ? <span className="absolute right-[calc(50%-18px)] top-2.5 size-2 rounded-full bg-warn-solid" aria-hidden /> : null}
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
  if (path === "/") return <HomeFeed />
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

