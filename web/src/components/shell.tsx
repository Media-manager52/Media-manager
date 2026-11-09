import { useQuery } from "@tanstack/react-query"
import {
  Activity, AlertTriangle, CalendarDays, Clapperboard, Home, Inbox, ListChecks, Menu, Monitor, Moon, Plus, Radio, Search, Settings, Sun, X, Server, Bug,
} from "lucide-react"
import { useEffect, useState, type ReactNode } from "react"
import { NavLink, Outlet, useLocation } from "react-router"

import { loadDashboard } from "@/api/client"
import { GlobalSearch } from "@/components/global-search"
import { cx } from "@/components/ui"

type NavItem = { to: string; label: string; icon: typeof Home; end?: boolean; badge?: "attention" }

const primary: NavItem[] = [
  { to: "/", label: "Oversigt", icon: Home, end: true },
  { to: "/handling", label: "Kræver handling", icon: AlertTriangle, badge: "attention" },
  { to: "/fejl", label: "Fejllog", icon: Bug },
  { to: "/bibliotek", label: "Bibliotek", icon: Clapperboard },
  { to: "/tilfoej", label: "Tilføj", icon: Plus },
  { to: "/anmodninger", label: "Anmodninger", icon: Inbox },
  { to: "/arbejde", label: "Arbejde", icon: Activity },
  { to: "/kalender", label: "Kalender", icon: CalendarDays },
]
const secondary: NavItem[] = [
  { to: "/kilder", label: "Kilder", icon: Radio },
  { to: "/indstillinger", label: "Indstillinger", icon: Settings },
  { to: "/system", label: "System", icon: Server },
  { to: "/kom-i-gang", label: "Kom i gang", icon: ListChecks },
]
const mobileBar: NavItem[] = [primary[0], primary[3], primary[1], primary[5]]

function useAttentionCount(): number | undefined {
  const { data } = useQuery({ queryKey: ["dashboard"], queryFn: ({ signal }) => loadDashboard(signal), refetchInterval: 60_000 })
  return data?.counts.needs_attention
}

function NavEntry({ item, count, onNavigate }: { item: NavItem; count?: number; onNavigate?: () => void }) {
  const Icon = item.icon
  return (
    <NavLink
      to={item.to}
      end={item.end}
      onClick={onNavigate}
      className={({ isActive }) =>
        cx(
          "group flex h-9 items-center gap-3 rounded-lg px-3 text-sm font-medium transition-colors",
          isActive ? "bg-surface-3 text-text" : "text-text-2 hover:bg-surface-2 hover:text-text",
        )
      }
    >
      <Icon aria-hidden className="size-4 shrink-0" />
      <span className="flex-1 truncate">{item.label}</span>
      {item.badge && count ? <span className="tabular rounded-full bg-warn-soft px-1.5 text-xs font-semibold text-warn">{count}</span> : null}
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
    <div className="flex items-center gap-2.5 px-3">
      <img src="/favicon.svg" alt="" className="size-7" />
      <div className="leading-tight">
        <div className="text-sm font-semibold">Media Manager</div>
        <div className="text-xs text-text-3">Dit bibliotek</div>
      </div>
    </div>
  )
}

function Sidebar({ count, onNavigate }: { count?: number; onNavigate?: () => void }) {
  return (
    <nav aria-label="Hovedmenu" className="flex h-full flex-col gap-6 px-3 py-5">
      <Brand />
      <GlobalSearch onNavigate={onNavigate} />
      <div className="flex flex-col gap-0.5">
        {primary.map((item) => <NavEntry key={item.to} item={item} count={count} onNavigate={onNavigate} />)}
      </div>
      <div className="flex flex-col gap-0.5">
        <div className="px-3 pb-1 text-xs font-medium uppercase tracking-wide text-text-3">Opsætning</div>
        {secondary.map((item) => <NavEntry key={item.to} item={item} onNavigate={onNavigate} />)}
      </div>
      <div className="mt-auto px-1">
        <ThemeSwitch />
      </div>
    </nav>
  )
}

export function AppShell({ children }: { children?: ReactNode }) {
  const count = useAttentionCount()
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
      <aside className="fixed inset-y-0 left-0 hidden w-60 border-r border-line bg-surface lg:block">
        <Sidebar count={count} />
      </aside>
      {drawer ? (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-black/50" onClick={() => setDrawer(false)} aria-hidden />
          <aside className="absolute inset-y-0 left-0 w-72 max-w-[85vw] overflow-y-auto border-r border-line bg-surface">
            <button type="button" onClick={() => setDrawer(false)} aria-label="Luk menu" className="absolute right-3 top-4 rounded-md p-1 text-text-3 hover:text-text">
              <X className="size-5" />
            </button>
            <Sidebar count={count} onNavigate={() => setDrawer(false)} />
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
        {searching ? (
          <div className="sticky top-14 z-30 border-b border-line bg-bg px-3 py-3 lg:hidden">
            <GlobalSearch autoFocus onNavigate={() => setSearching(false)} />
          </div>
        ) : null}
        <main id="main" className="mx-auto w-full max-w-7xl px-4 pb-28 pt-6 sm:px-6 lg:px-10 lg:pb-12 lg:pt-10">
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
              {item.badge && count ? <span className="tabular absolute right-[calc(50%-1.4rem)] top-1.5 rounded-full bg-warn px-1 text-[10px] font-semibold text-[#1b1b1b]">{count}</span> : null}
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
