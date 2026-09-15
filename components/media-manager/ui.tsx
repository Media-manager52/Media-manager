"use client"

import * as React from "react"
import {
  Archive,
  Ban,
  CheckCircle2,
  CircleAlert,
  CircleDashed,
  CircleX,
  Clock3,
  Download,
  Info,
  PauseCircle,
  Search,
  SearchCheck,
  ShieldCheck,
  Upload,
  type LucideIcon,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
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
        <div className="text-[11.5px] font-extrabold uppercase tracking-[0.18em] text-[#7f8a9a]">{area}</div>
        <h1 className="mt-1.5 text-[28px] font-[820] leading-tight tracking-[-0.035em] text-white sm:text-[33px]">{title}</h1>
        <p className="mt-2 max-w-3xl text-[14px] leading-6 text-[#8f9aab]">{subtitle}</p>
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
          <div className="min-w-0">
            {title ? <h2 className="text-[15px] font-bold tracking-[-0.01em] text-[#eef2f7]">{title}</h2> : null}
            {subtitle ? <p className="mt-1 text-[12.5px] leading-5 text-[#838f9f]">{subtitle}</p> : null}
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
        <span className="text-[11px] font-extrabold uppercase tracking-[0.14em] text-[#828d9d]">{label}</span>
        {Icon ? <Icon className={cn("size-4", toneText[tone])} aria-hidden /> : null}
      </div>
      <div className="mt-2 text-[27px] font-[850] leading-none tracking-[-0.04em] text-[#f4f6f9]">{value}</div>
      <div className="mt-1.5 text-[12px] leading-5 text-[#838f9f]">{note}</div>
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

/**
 * Canonical product-wide status language.
 *
 * Every workflow, file, request, and source state resolves through this one
 * vocabulary so the same real condition always looks the same. Waiting and
 * searching are deliberately calm (neutral/info); only genuine owner action
 * uses the attention color.
 */
export type StatusDescriptor = { label: string; tone: StatusTone; icon: LucideIcon; hint?: string }

const statusVocabulary: Record<string, StatusDescriptor> = {
  // Availability / publication
  available: { label: "Available", tone: "good", icon: CheckCircle2, hint: "A verified current file exists for this item." },
  current: { label: "Available", tone: "good", icon: CheckCircle2 },
  plex_confirmed: { label: "Plex confirmed", tone: "good", icon: ShieldCheck, hint: "Plex confirmed the exact managed path and provider identity." },
  verified: { label: "Verified", tone: "good", icon: CheckCircle2 },
  delivered: { label: "Delivered", tone: "good", icon: CheckCircle2 },
  succeeded: { label: "Succeeded", tone: "good", icon: CheckCircle2 },
  success: { label: "Succeeded", tone: "good", icon: CheckCircle2 },
  healthy: { label: "Healthy", tone: "good", icon: CheckCircle2 },
  ready: { label: "Ready", tone: "good", icon: CheckCircle2 },
  configured: { label: "Configured", tone: "good", icon: CheckCircle2 },
  enabled: { label: "Enabled", tone: "good", icon: CheckCircle2 },
  monitored: { label: "Monitored", tone: "info", icon: SearchCheck, hint: "Monitoring is on; Media Manager will search within indexer pacing." },
  available_in_seerr: { label: "Available in Seerr", tone: "good", icon: CheckCircle2 },
  available_seerr: { label: "Available in Seerr", tone: "good", icon: CheckCircle2 },

  // Waiting / in progress (not a warning)
  waiting: { label: "Waiting", tone: "neutral", icon: Clock3, hint: "Waiting normally. No owner action is required." },
  queued: { label: "Queued", tone: "info", icon: Clock3 },
  searching: { label: "Searching", tone: "info", icon: Search },
  candidate_selected: { label: "Candidate selected", tone: "info", icon: Search },
  submitting: { label: "Submitting", tone: "info", icon: Download },
  downloading: { label: "Downloading", tone: "info", icon: Download },
  streaming_ready: { label: "Streaming ready", tone: "info", icon: Download },
  import_complete: { label: "Import complete", tone: "info", icon: Download },
  verifying: { label: "Verifying", tone: "info", icon: ShieldCheck },
  publishing: { label: "Publishing", tone: "info", icon: Upload },
  plex_scanning: { label: "Plex scanning", tone: "info", icon: Search },
  planned: { label: "Planned", tone: "neutral", icon: Clock3 },
  retained: { label: "Retained", tone: "neutral", icon: Archive },
  restore_confirming: { label: "Restore confirming", tone: "info", icon: Clock3 },
  running: { label: "Running", tone: "info", icon: Clock3 },
  retrying: { label: "Retrying", tone: "info", icon: Clock3 },
  awaiting_commission: { label: "Awaiting commission", tone: "warn", icon: CircleAlert },

  // Genuine owner attention
  needs_attention: { label: "Needs attention", tone: "warn", icon: CircleAlert, hint: "Media Manager paused for a decision only you can make." },
  held: { label: "Held", tone: "warn", icon: PauseCircle, hint: "Held pending stronger evidence. No action may be required." },
  blocked: { label: "Blocked", tone: "warn", icon: CircleAlert },
  quarantined: { label: "Quarantined", tone: "warn", icon: CircleAlert },
  transport_unknown: { label: "Transport unknown", tone: "warn", icon: CircleAlert },
  restore_attention: { label: "Restore needs attention", tone: "warn", icon: CircleAlert },
  failed: { label: "Failed", tone: "bad", icon: CircleX },
  declined: { label: "Declined", tone: "bad", icon: CircleX },
  cancelled: { label: "Cancelled", tone: "bad", icon: Ban },
  rejected: { label: "Rejected", tone: "bad", icon: CircleX },

  // Inactive / neutral
  disabled: { label: "Disabled", tone: "neutral", icon: Ban },
  archived: { label: "Archived", tone: "neutral", icon: Archive },
  purged: { label: "Purged", tone: "neutral", icon: Archive },
  restored: { label: "Restored", tone: "good", icon: CheckCircle2 },
  unmapped: { label: "Unmapped", tone: "neutral", icon: CircleDashed },
  unknown: { label: "Unknown", tone: "neutral", icon: CircleDashed },
  unavailable: { label: "Unavailable", tone: "neutral", icon: CircleDashed },
}

export function describeStatus(state: string): StatusDescriptor {
  const key = state.trim().toLocaleLowerCase().replaceAll(" ", "_")
  return statusVocabulary[key] ?? {
    label: state ? state.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase()) : "Unknown",
    tone: "neutral",
    icon: CircleDashed,
  }
}

/**
 * Renders the canonical status for a raw persisted state. Use this instead of
 * hand-picking a tone so terminology and color stay consistent across screens.
 */
export function StateBadge({ state, label, className }: { state: string; label?: string; className?: string }) {
  const descriptor = describeStatus(state)
  const Icon = descriptor.icon
  return (
    <StatusChip tone={descriptor.tone} className={className}>
      <Icon className="size-3" aria-hidden />
      {label ?? descriptor.label}
    </StatusChip>
  )
}

export function StatusChip({ children, tone = "neutral", className }: { children: React.ReactNode; tone?: StatusTone; className?: string }) {
  return (
    <Badge variant="outline" className={cn("h-6 gap-1 rounded-full px-2 text-[10.5px] font-bold tracking-[0.01em]", toneBadge[tone], className)}>
      {children}
    </Badge>
  )
}

export function KeyValue({ label, value, valueClassName }: { label: string; value: React.ReactNode; valueClassName?: string }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-white/[0.055] py-2.5 last:border-0">
      <span className="text-[12px] leading-5 text-[#838f9f]">{label}</span>
      <span className={cn("text-right text-[12.5px] font-semibold leading-5 text-[#dde2e9]", valueClassName)}>{value}</span>
    </div>
  )
}

export function WorkProgress({ value, label }: { value: number; label?: string }) {
  return (
    <div>
      {label ? <div className="mb-1.5 text-[11px] text-[#838f9f]">{label}</div> : null}
      <Progress value={value} className="h-1.5 bg-[#202733] [&>div]:bg-gradient-to-r [&>div]:from-[#7790ef] [&>div]:to-[#a8b8f7]" />
    </div>
  )
}

export function InlineNotice({
  tone = "info",
  title,
  children,
  action,
  className,
}: {
  tone?: Exclude<StatusTone, "neutral">
  title: string
  children: React.ReactNode
  action?: React.ReactNode
  className?: string
}) {
  const Icon = tone === "good" ? CheckCircle2 : tone === "warn" ? CircleAlert : tone === "bad" ? CircleX : Info
  return (
    <div className={cn("rounded-[14px] border p-3.5", toneBadge[tone], className)}>
      <div className="flex items-start gap-3">
        <Icon className="mt-0.5 size-4 shrink-0" aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="text-[12.5px] font-bold">{title}</div>
          <div className="mt-1 text-[12px] leading-5 opacity-85">{children}</div>
          {action ? <div className="mt-3">{action}</div> : null}
        </div>
      </div>
    </div>
  )
}

export function EmptyAction({ title, copy, action }: { title: string; copy: string; action?: React.ReactNode }) {
  return (
    <div className="grid min-h-44 place-items-center rounded-2xl border border-dashed border-[#2e3744] bg-[#0c1015] px-5 py-10 text-center">
      <div>
        <div className="text-[14px] font-bold text-[#e9edf2]">{title}</div>
        <p className="mx-auto mt-2 max-w-md text-[12.5px] leading-5 text-[#838f9f]">{copy}</p>
        {action ? <div className="mt-4 flex justify-center">{action}</div> : null}
      </div>
    </div>
  )
}

export function SectionLabel({ children, className, action }: { children: React.ReactNode; className?: string; action?: React.ReactNode }) {
  return (
    <div className={cn("mb-2.5 flex items-center justify-between gap-3", className)}>
      <h3 className="text-[11px] font-extrabold uppercase tracking-[0.14em] text-[#828d9d]">{children}</h3>
      {action}
    </div>
  )
}
