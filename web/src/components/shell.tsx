import { useQuery } from "@tanstack/react-query"
import {
  Activity, AlertTriangle, CalendarDays, Clapperboard, Film, Home, Inbox, LibraryBig, ListChecks, Menu, Monitor, Moon, Plus, Radio, Search, SearchCheck, Settings, Sun, Tv, X, Server, Bug,
} from "lucide-react"
import { useEffect, useState, type ReactNode } from "react"
import { NavLink, Outlet, useLocation } from "react-router"

import { loadDashboard } from "@/api/client"
import { GlobalSearch } from "@/components/global-search"
import { cx } from "@/components/ui"
import { formatNumber } from "@/format"

type NavItem = { to: string; label: string; icon: typeof Home; end?: boolean; badge?: "attention"; count?: "movies" | "series"; type?: string }
type NavGroup = { label?: string; items: NavItem[] }

const groups: NavGroup[] = [
  { items: [
    { to: "/", label: "Oversigt", icon: Home, end: true },
    { to: "/tilfoej", label: "Tilføj", icon: Plus },
    { to: "/kalender", label: "Kalender", icon: CalendarDays },
  ] },
  { label: "Bibliotek", items: [
    { to: "/bibliotek", label: "Alle titler", icon: LibraryBig, end: true, type: "" },
    { to: "/bibliotek?type=film", label: "Film", icon: Film, end: true, type: "film", count: "movies" },
    { to: "/bibliotek?type=serier", label: "Serier", icon: Tv, end: true, type: "serier", count: "series" },
    { to: "/bibliotek/mangler", label: "Find manglende", icon: SearchCheck },
  ] },
  { label: "Opgaver", items: [
    { to: "/handling", label: "Kræver handling", icon: AlertTriangle, badge: "attention" },
    { to: "/anmodninger", label: "Anmodninger", icon: Inbox },
    { to: "/arbejde", label: "Arbejde", icon: Activity },
    { to: "/fejl", label: "Fejllog", icon: Bug },
  ] },
  { label: "Opsætning", items: [
    { to: "/kilder", label: "Kilder", icon: Radio },
    { to: "/indstillinger", label: "Indstillinger", icon: Settings },
    { to: "/system", label: "System", icon: Server },
    { to: "/kom-i-gang", label: "Kom i gang", icon: ListChecks },
  ] },
]
const mobileBar: NavItem[] = [
  { to: "/", label: "Oversigt", icon: Home, end: true },
  { to: "/bibliotek", label: "Bibliotek", icon: Clapperboard },
  { to: "/handling", label: "Kræver handling", icon: AlertTriangle, badge: "attention" },
  { to: "/anmodninger", label: "Anmodninger", icon: Inbox },
]

type Counts = { attention?: number; movies?: number; series?: number }

function useCounts(): Counts {
  const { data } = useQuery({ queryKey: ["dashboard"], queryFn: ({ signal }) => loadDashboard(signal), refetchInterval: 60_000 })
  return { attention: data?.counts.needs_attention, movies: data?.counts.movies, series: data?.counts.series }
}

/** A library entry is current only for its own ?type=, so Film and Serier never light up together. */
function libraryEntryActive(item: NavItem, pathname: string, search: string): boolean {
  return pathname === "/bibliotek" && (new URLSearchParams(search).get("type") ?? "") === item.type
}

function NavEntry({ item, counts, onNavigate }: { item: NavItem; counts: Counts; onNavigate?: () => void }) {
  const Icon = item.icon
  const location = useLocation()
  const value = item.badge ? counts.attention : item.count ? counts[item.count] : undefined
  return (
    <NavLink
      to={item.to}
      end={item.end}
      onClick={onNavigate}
      className={({ isActive }) => {
        const active = item.type === undefined ? isActive : libraryEntryActive(item, location.pathname, location.search)
        return cx(
          "group flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-sm transition-colors",
          active ? "bg-surface-3 font-semibold text-text" : "font-medium text-text-2 hover:bg-surface-2 hover:text-text",
        )
      }}
    >
      <Icon aria-hidden className="size-4 shrink-0" />
      <span className="flex-1 truncate">{item.label}</span>
      {item.badge && value ? <span className="tabular rounded-full bg-warn-soft px-1.5 text-xs font-semibold text-warn">{formatNumber(value)}</span> : null}
      {item.count && value !== undefined ? <span className="tabular text-xs text-text-3">{formatNumber(value)}</span> : null}
    </NavLink>
  )
}

type Theme = "system" | "light" | "dark"

function readTheme(): Theme {
  try {
    const value = localStorage.getItem("mm-theme")
    return value === "light" || value === "dark" ? value : "system"
  } catch {
    return "system"
  }
}

function ThemeSwitch() {
  const [theme, setTheme] = useState<Theme>(readTheme)
  useEffect(() => {
    if (theme === "system") delete document.documentElement.dataset.theme
    else document.documentElement.dataset.theme = theme
    try {
      if (theme === "system") localStorage.removeItem("mm-theme")
      else localStorage.setItem("mm-theme", theme)
    } catch { /* storage blocked: the choice lasts for this page only */ }
  }, [theme])
  const options: Array<{ value: Theme; label: string; icon: typeof Sun }> = [
    { value: "light", label: "Lyst tema", icon: Sun },
    { value: "system", label: "Følg systemet", icon: Monitor },
    { value: "dark", label: "Mørkt tema", icon: Moon },
  ]
  return (
    <div role="radiogroup" aria-label="Tema" className="flex gap-1 rounded-lg bg-surface-2 p-1">
      {options.map(({ value, label, icon: Icon }) => (
        <button
          key={value}
          type="button"
          role="radio"
          aria-checked={theme === value}
          aria-label={label}
          title={label}
          onClick={() => setTheme(value)}
          className={cx("flex h-7 flex-1 items-center justify-center rounded-md", theme === value ? "bg-surface text-text shadow-sm" : "text-text-3 hover:text-text")}
        >
          <Icon aria-hidden className="size-3.5" />
        </button>
      ))}
    </div>
  )
}

function Brand() {
  return (
    <div className="flex items-center gap-2.5 px-2.5">
      <img src="/favicon.svg" alt="" className="size-7" />
      <div className="text-[15px] font-semibold leading-tight">Media Manager</div>
    </div>
  )
}

function Sidebar({ counts, onNavigate }: { counts: Counts; onNavigate?: () => void }) {
  return (
    <nav aria-label="Hovedmenu" className="flex h-full flex-col gap-5 overflow-y-auto px-3 py-4">
      <Brand />
      {groups.map((group, index) => (
        <div key={group.label ?? index} className="flex flex-col gap-0.5">
          {group.label ? <div className="px-2.5 pb-1 text-xs font-medium text-text-3">{group.label}</div> : null}
          {group.items.map((item) => <NavEntry key={item.to} item={item} counts={counts} onNavigate={onNavigate} />)}
        </div>
      ))}
      <div className="mt-auto px-1">
        <ThemeSwitch />
      </div>
    </nav>
  )
}

export function AppShell({ children }: { children?: ReactNode }) {
  const counts = useCounts()
  const [drawer, setDrawer] = useState(false)
  const [searching, setSearching] = useState(false)
  const location = useLocation()
  useEffect(() => {
    window.scrollTo(0, 0)
  }, [location.pathname])
  useEffect(() => {
    if (!drawer) return
    const close = (event: KeyboardEvent) => { if (event.key === "Escape") setDrawer(false) }
    window.addEventListener("keydown", close)
    return () => window.removeEventListener("keydown", close)
  }, [drawer])
  return (
    <div className="min-h-dvh">
      <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-50 focus:rounded-lg focus:bg-surface focus:px-3 focus:py-2">
        Spring til indhold
      </a>
      <aside className="fixed inset-y-0 left-0 hidden w-60 border-r border-line bg-sidebar lg:block">
        <Sidebar counts={counts} />
      </aside>
      {drawer ? (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-black/50" onClick={() => setDrawer(false)} aria-hidden />
          <aside className="absolute inset-y-0 left-0 w-72 max-w-[85vw] overflow-y-auto border-r border-line bg-sidebar">
            <button type="button" onClick={() => setDrawer(false)} aria-label="Luk menu" className="absolute right-3 top-4 rounded-md p-1 text-text-3 hover:text-text">
              <X className="size-5" />
            </button>
            <Sidebar counts={counts} onNavigate={() => setDrawer(false)} />
          </aside>
        </div>
      ) : null}
      <div className="lg:pl-60">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-line bg-bg/90 px-4 backdrop-blur lg:hidden">
          <button type="button" onClick={() => setDrawer(true)} aria-label="Åbn menu" className="-ml-2 rounded-md p-2.5 text-text-2 hover:text-text">
            <Menu className="size-5" />
          </button>
          <img src="/favicon.svg" alt="" className="size-6" />
          <span className="flex-1 font-semibold">Media Manager</span>
          <button type="button" onClick={() => setSearching((value) => !value)} aria-label={searching ? "Luk søgning" : "Søg i dine titler"} aria-expanded={searching} className="-mr-2 rounded-md p-2.5 text-text-2 hover:text-text">
            {searching ? <X className="size-5" /> : <Search className="size-5" />}
          </button>
        </header>
        <div className="sticky top-0 z-30 hidden h-14 items-center justify-center border-b border-line bg-bg/90 px-10 backdrop-blur lg:flex">
          <div className="w-full max-w-md">
            <GlobalSearch shortcut />
          </div>
        </div>
        {searching ? (
          <div className="sticky top-14 z-30 border-b border-line bg-bg px-3 py-3 lg:hidden">
            <GlobalSearch autoFocus onNavigate={() => setSearching(false)} />
          </div>
        ) : null}
        <main id="main" className="mx-auto w-full max-w-7xl px-4 pb-28 pt-6 sm:px-6 lg:px-10 lg:pb-12 lg:pt-8">
          {children ?? <Outlet />}
        </main>
      </div>
      <nav aria-label="Genveje" className="fixed inset-x-0 bottom-0 z-30 grid grid-cols-5 border-t border-line bg-surface/95 pb-[env(safe-area-inset-bottom)] backdrop-blur lg:hidden">
        {mobileBar.map((item) => {
          const Icon = item.icon
          return (
            <NavLink key={item.to} to={item.to} end={item.end} className={({ isActive }) => cx("relative flex h-14 flex-col items-center justify-center gap-0.5 text-[11px] font-medium", isActive ? "text-accent-strong" : "text-text-2")}>
              <Icon aria-hidden className="size-5" />
              <span className="max-w-full truncate px-1">{({ "Kræver handling": "Handling", Anmodninger: "Ønsker" } as Record<string, string>)[item.label] ?? item.label}</span>
              {item.badge && counts.attention ? <span className="tabular absolute right-[calc(50%-1.4rem)] top-1.5 rounded-full bg-warn px-1 text-[10px] font-semibold text-[#1b1b1b]">{counts.attention}</span> : null}
            </NavLink>
          )
        })}
        <button type="button" onClick={() => setDrawer(true)} className="flex h-14 flex-col items-center justify-center gap-0.5 text-[11px] font-medium text-text-2">
          <Menu aria-hidden className="size-5" />
          Mere
        </button>
      </nav>
    </div>
  )
}
