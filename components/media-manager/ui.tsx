"use client"

import * as React from "react"
import { CheckCircle2, CircleAlert, CircleX, Info, type LucideIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { cn } from "@/lib/utils"
import type { StatusTone } from "@/lib/media-manager-data"

export function PageHeader({
  area,
  title,
  subtitle,
  actions,
}: {
  area: string
  title: string
  subtitle: string
  actions?: React.ReactNode
}) {
  return (
    <header className="flex flex-col gap-4 border-b border-white/[0.06] pb-5 lg:flex-row lg:items-end lg:justify-between">
      <div className="min-w-0">
        <div className="text-[10px] font-extrabold uppercase tracking-[0.2em] text-[#7f8a9a]">{area}</div>
        <h1 className="mt-1 text-[28px] font-[820] leading-tight tracking-[-0.035em] text-white sm:text-[34px]">{title}</h1>
        <p className="mt-2 max-w-3xl text-[13px] leading-6 text-[#8b96a5]">{subtitle}</p>
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  )
}

export function Panel({
  title,
  subtitle,
  action,
  children,
  className,
}: {
  title?: string
  subtitle?: string
  action?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <section className={cn("mm-panel", className)}>
      {title || action ? (
        <div className="mb-4 flex items-start justify-between gap-4">
          <div>
            {title ? <h2 className="text-[14px] font-bold text-[#eef2f7]">{title}</h2> : null}
            {subtitle ? <p className="mt-1 text-[11px] leading-5 text-[#7f8a9a]">{subtitle}</p> : null}
          </div>
          {action}
        </div>
      ) : null}
      {children}
    </section>
  )
}

export function MetricCard({
  label,
  value,
  note,
  tone = "neutral",
  icon: Icon,
}: {
  label: string
  value: string
  note: string
  tone?: StatusTone
  icon?: LucideIcon
}) {
  return (
    <div className="mm-metric">
      <div className="flex items-center justify-between gap-3">
        <span className="text-[9px] font-extrabold uppercase tracking-[0.16em] text-[#7b8695]">{label}</span>
        {Icon ? <Icon className={cn("size-4", toneText[tone])} aria-hidden /> : null}
      </div>
      <div className="mt-2 text-[25px] font-[850] tracking-[-0.04em] text-[#f4f6f9]">{value}</div>
      <div className="mt-1 text-[10px] leading-5 text-[#7f8a9a]">{note}</div>
    </div>
  )
}

const toneText: Record<StatusTone, string> = {
  good: "text-[#62d0a0]",
  info: "text-[#9cafef]",
  warn: "text-[#efc36f]",
  bad: "text-[#ee8b8b]",
  neutral: "text-[#9ba6b5]",
}

const toneBadge: Record<StatusTone, string> = {
  good: "border-[#285142] bg-[#10201b] text-[#99e1c3]",
  info: "border-[#30446d] bg-[#131c31] text-[#b6c6ff]",
  warn: "border-[#594923] bg-[#211b10] text-[#ebcf8c]",
  bad: "border-[#5c3339] bg-[#221316] text-[#f1a6a6]",
  neutral: "border-[#303844] bg-[#141920] text-[#b7c0cb]",
}

export function StatusChip({ children, tone = "neutral" }: { children: React.ReactNode; tone?: StatusTone }) {
  return (
    <Badge variant="outline" className={cn("h-6 rounded-full px-2 text-[9px] font-bold tracking-wide", toneBadge[tone])}>
      {children}
    </Badge>
  )
}

export function KeyValue({ label, value, valueClassName }: { label: string; value: React.ReactNode; valueClassName?: string }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-white/[0.055] py-2.5 last:border-0">
      <span className="text-[10px] leading-5 text-[#7f8a9a]">{label}</span>
      <span className={cn("text-right text-[10.5px] font-semibold leading-5 text-[#d9dee5]", valueClassName)}>{value}</span>
    </div>
  )
}

export function WorkProgress({ value, label }: { value: number; label?: string }) {
  return (
    <div>
      {label ? <div className="mb-1.5 text-[9px] text-[#7f8a9a]">{label}</div> : null}
      <Progress value={value} className="h-1.5 bg-[#202733] [&>div]:bg-gradient-to-r [&>div]:from-[#7790ef] [&>div]:to-[#a8b8f7]" />
    </div>
  )
}

export function InlineNotice({
  tone = "info",
  title,
  children,
  action,
}: {
  tone?: Exclude<StatusTone, "neutral">
  title: string
  children: React.ReactNode
  action?: React.ReactNode
}) {
  const Icon = tone === "good" ? CheckCircle2 : tone === "warn" ? CircleAlert : tone === "bad" ? CircleX : Info
  return (
    <div className={cn("rounded-[14px] border p-3.5", toneBadge[tone])}>
      <div className="flex items-start gap-3">
        <Icon className="mt-0.5 size-4 shrink-0" aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="text-[11px] font-bold">{title}</div>
          <div className="mt-1 text-[10px] leading-5 opacity-80">{children}</div>
          {action ? <div className="mt-3">{action}</div> : null}
        </div>
      </div>
    </div>
  )
}

export function EmptyAction({ title, copy, onClick }: { title: string; copy: string; onClick?: () => void }) {
  return (
    <div className="rounded-2xl border border-dashed border-[#2e3744] bg-[#0c1015] px-5 py-10 text-center">
      <div className="text-sm font-bold text-[#e9edf2]">{title}</div>
      <p className="mx-auto mt-2 max-w-md text-[11px] leading-5 text-[#7f8a9a]">{copy}</p>
      {onClick ? <Button className="mt-4" onClick={onClick}>Start</Button> : null}
    </div>
  )
}
