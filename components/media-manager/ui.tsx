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
  good: "text-[color:var(--status-good)]",
  info: "text-[color:var(--status-info)]",
  warn: "text-[color:var(--status-warn)]",
  bad: "text-[color:var(--status-bad)]",
  neutral: "text-[color:var(--status-wait)]",
}

// One semantic palette for every tone. These reference the canonical
// --status-* tokens in globals.css so a tone can never drift into a new hex.
const toneBadge: Record<StatusTone, string> = {
  good: "border-[color:var(--status-good-border)] bg-[color:var(--status-good-bg)] text-[color:var(--status-good)]",
  info: "border-[color:var(--status-info-border)] bg-[color:var(--status-info-bg)] text-[color:var(--status-info)]",
  warn: "border-[color:var(--status-warn-border)] bg-[color:var(--status-warn-bg)] text-[color:var(--status-warn)]",
  bad: "border-[color:var(--status-bad-border)] bg-[color:var(--status-bad-bg)] text-[color:var(--status-bad)]",
  neutral: "border-[color:var(--status-wait-border)] bg-[color:var(--status-wait-bg)] text-[color:var(--status-wait)]",
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
  delivering: { label: "Delivering", tone: "info", icon: Upload },
  delivered: { label: "Delivered", tone: "good", icon: CheckCircle2 },
  succeeded: { label: "Succeeded", tone: "good", icon: CheckCircle2 },
  success: { label: "Succeeded", tone: "good", icon: CheckCircle2 },
  healthy: { label: "Healthy", tone: "good", icon: CheckCircle2 },
  ready: { label: "Ready", tone: "good", icon: CheckCircle2 },
  configured: { label: "Configured", tone: "good", icon: CheckCircle2 },
  enabled: { label: "Enabled", tone: "good", icon: CheckCircle2 },
  monitored: { label: "Monitored", tone: "info", icon: SearchCheck, hint: "Monitoring is on; Media Manager will search within indexer pacing." },
  unmonitored: { label: "Unmonitored", tone: "neutral", icon: CircleDashed },
  missing: { label: "Missing", tone: "warn", icon: CircleDashed },
  upgradable: { label: "Upgrade wanted", tone: "info", icon: Download },
  upgrade: { label: "Upgrade", tone: "info", icon: Download },
  available_in_seerr: { label: "Available in Seerr", tone: "good", icon: CheckCircle2 },
  available_seerr: { label: "Available in Seerr", tone: "good", icon: CheckCircle2 },
  eligible: { label: "Eligible", tone: "good", icon: CheckCircle2 },
  media_info: { label: "Media info", tone: "info", icon: ShieldCheck },
  release_evidence: { label: "Release evidence", tone: "info", icon: ShieldCheck },
  unassessed: { label: "Unassessed", tone: "warn", icon: CircleAlert },
  pending_review: { label: "Pending review", tone: "warn", icon: Clock3 },
  already_cataloged: { label: "Already cataloged", tone: "good", icon: CheckCircle2 },
  approved: { label: "Approved", tone: "good", icon: CheckCircle2 },
  pending: { label: "Pending", tone: "neutral", icon: Clock3 },
  executing: { label: "Executing", tone: "info", icon: Clock3 },
  complete: { label: "Complete", tone: "good", icon: CheckCircle2 },
  unsupported: { label: "Unsupported", tone: "neutral", icon: CircleDashed },
  awaiting_validation: { label: "Awaiting validation", tone: "warn", icon: CircleAlert },
  not_approved: { label: "Not approved", tone: "neutral", icon: CircleDashed },
  expired: { label: "Expired", tone: "neutral", icon: Archive },

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
    <Badge variant="outline" className={cn("h-6 gap-1 rounded-full px-2 text-[11px] font-bold tracking-[0.01em]", toneBadge[tone], className)}>
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
