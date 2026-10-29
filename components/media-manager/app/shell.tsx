"use client"

import * as React from "react"
import Link from "next/link"
import "@fontsource-variable/bricolage-grotesque"
import { Bell, CirclePlus, House, Inbox, LayoutGrid, Plus, SlidersHorizontal, X } from "lucide-react"

import { AppDataProvider, useAppData } from "./app-data"
import "./app.css"

export type AppSection = "home" | "library" | "requests" | "add" | "needs" | "settings"

function BrandMark() {
  return <svg width="30" height="30" viewBox="0 0 24 24" aria-hidden><rect x="2" y="2" width="20" height="20" rx="6" fill="#8AB0FF" /><path d="M10 8.2v7.6l6.2-3.8z" fill="#0D1015" /></svg>
}

const topNav = [
  { section: "home", href: "/", label: "Home" },
  { section: "library", href: "/library", label: "Library" },
  { section: "requests", href: "/requests", label: "Requests" },
] as const

const tabs = [
  { section: "home", href: "/", label: "Home", icon: House },
  { section: "library", href: "/library", label: "Library", icon: LayoutGrid },
  { section: "add", href: "/add", label: "Add", icon: CirclePlus },
  { section: "requests", href: "/requests", label: "Requests", icon: Inbox },
  { section: "settings", href: "/settings", label: "Settings", icon: SlidersHorizontal },
] as const

function current(active: AppSection, section: AppSection) {
  return active === section ? "page" as const : undefined
}

function Frame({ section, children }: { section: AppSection; children: React.ReactNode }) {
  const { data, notice, clearNotice } = useAppData()
  const needs = data?.needs.length ?? 0
  return <div className="mm-app">
    <a href="#mm-main" className="mm-skip">Skip to content</a>
    <header className="mm-top">
      <Link href="/" className="mm-brand"><BrandMark />Media Manager</Link>
      <nav aria-label="Main" className="mm-nav">{topNav.map((item) => <Link key={item.href} href={item.href} aria-current={current(section, item.section)}>{item.label}</Link>)}</nav>
      <span className="mm-top-spacer" />
      <Link href="/add" className="mm-btn mm-btn-primary mm-top-desktop" aria-current={current(section, "add")}><Plus size={18} aria-hidden />Add</Link>
      <Link href="/needs-you" className="mm-icon-link" aria-current={current(section, "needs")} aria-label={needs ? `Needs you: ${needs} ${needs === 1 ? "item" : "items"}` : "Needs you: nothing"}>
        <Bell size={22} aria-hidden />{needs ? <span className="mm-count" aria-hidden>{needs > 99 ? "99+" : needs}</span> : null}
      </Link>
      <Link href="/settings" className="mm-icon-link mm-top-desktop" aria-current={current(section, "settings")} aria-label="Settings"><SlidersHorizontal size={22} aria-hidden /></Link>
    </header>
    <main id="mm-main" className="mm-main">
      {notice ? <div role="status" className="mm-notice"><span>{notice}</span><button type="button" className="mm-btn mm-btn-quiet" onClick={clearNotice} aria-label="Dismiss message"><X size={18} aria-hidden /></button></div> : null}
      {children}
    </main>
    <nav aria-label="Main" className="mm-tabs">{tabs.map((tab) => <Link key={tab.href} href={tab.href} aria-current={current(section, tab.section)}><tab.icon size={22} aria-hidden />{tab.label}</Link>)}</nav>
  </div>
}

/** The everyday app's frame. Every page reads one shared, auto-refreshing snapshot. */
export function AppShell({ section, children }: { section: AppSection; children: React.ReactNode }) {
  return <AppDataProvider><Frame section={section}>{children}</Frame></AppDataProvider>
}
