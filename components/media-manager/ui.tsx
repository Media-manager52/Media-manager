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
  title,
  subtitle,
  actions,
}: {
  /** Shown in the shell breadcrumb; kept so route metadata stays one object. */
  area?: string
  title: string
  subtitle: string
  actions?: React.ReactNode
}) {
  return (
    <header className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        <h1 className="text-[20px] font-bold leading-tight tracking-[-0.02em] text-fg">{title}</h1>
        <p className="mt-1 max-w-3xl text-[12.5px] leading-5 text-fg-subtle">{subtitle}</p>
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  )
}

/**
 * The console card: a header row (title, quiet count or subtitle, one action)
 * over a padded body. `flush` bodies (tables, lists) run edge to edge.
 */
export function Panel({
  title,
  subtitle,
  action,
  children,
  className,
  flush = false,
}: {
  title?: string
  subtitle?: string
  action?: React.ReactNode
  children: React.ReactNode
  className?: string
  /** Edge-to-edge body (tables, lists) with a padded header. */
  flush?: boolean
}) {
  return (
    <section className={cn("mm-panel", className)}>
      {title || action ? (
        <div className="flex flex-col gap-2 border-b border-line px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
          <div className="min-w-0">
            {title ? <h2 className="text-[13px] font-bold tracking-[-0.005em] text-fg">{title}</h2> : null}
            {subtitle ? <p className="mt-0.5 text-[12px] leading-[18px] text-fg-subtle">{subtitle}</p> : null}
          </div>
          {action ? <div className="flex shrink-0 items-center gap-2">{action}</div> : null}
        </div>
      ) : null}
      <div className={cn(!flush && "p-4")}>{children}</div>
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
        <span className="text-[12px] font-semibold text-fg-muted">{label}</span>
        {Icon ? <span className={cn("grid size-6 place-items-center rounded-md", toneTile[tone])}><Icon className="size-3.5" aria-hidden /></span> : null}
      </div>
      <div className="mt-2 text-[22px] font-extrabold leading-none tracking-[-0.02em] text-fg tabular-nums">{value}</div>
      <div className="mt-1.5 text-[11.5px] leading-[17px] text-fg-subtle">{note}</div>
    </div>
  )
}

const toneTile: Record<StatusTone, string> = {
  good: "bg-[color:var(--status-good-bg)] text-[color:var(--status-good)]",
  info: "bg-[color:var(--status-info-bg)] text-[color:var(--status-info)]",
  warn: "bg-[color:var(--status-warn-bg)] text-[color:var(--status-warn)]",
  bad: "bg-[color:var(--status-bad-bg)] text-[color:var(--status-bad)]",
  neutral: "bg-surface-2 text-fg-muted",
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
  return <StatusChip tone={descriptor.tone} className={className}>{label ?? descriptor.label}</StatusChip>
}

/** A status pill: a coloured dot and a short label, tinted by tone. */
export function StatusChip({ children, tone = "neutral", className, dot = true }: { children: React.ReactNode; tone?: StatusTone; className?: string; dot?: boolean }) {
  return (
    <Badge variant="outline" className={cn("h-[22px] gap-1.5 rounded-full px-2 text-[11px] font-semibold", toneBadge[tone], className)}>
      {dot ? <i className="size-1.5 shrink-0 rounded-full bg-current not-italic" aria-hidden /> : null}
      {children}
    </Badge>
  )
}

export function KeyValue({ label, value, valueClassName }: { label: string; value: React.ReactNode; valueClassName?: string }) {
  return (
    <div className="flex items-start justify-between gap-4 border-t border-line py-[7px] first:border-0">
      <span className="text-[12.5px] leading-5 text-fg-subtle">{label}</span>
      <span className={cn("text-right text-[12.5px] font-semibold leading-5 text-fg", valueClassName)}>{value}</span>
    </div>
  )
}

export function WorkProgress({ value, label }: { value: number; label?: string }) {
  return (
    <div>
      {label ? <div className="mb-1.5 text-[12px] text-fg-muted">{label}</div> : null}
      <Progress value={value} aria-label={label ?? "Progress"} className="h-1.5 bg-surface-3 [&>div]:bg-brand" />
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
    <div className={cn("rounded-xl border p-4", toneBadge[tone], className)}>
      <div className="flex items-start gap-3">
        <Icon className="mt-0.5 size-4 shrink-0" aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-semibold">{title}</div>
          <div className="mt-1 text-[13px] leading-5 opacity-90">{children}</div>
          {action ? <div className="mt-3">{action}</div> : null}
        </div>
      </div>
    </div>
  )
}

export function EmptyAction({ title, copy, action }: { title: string; copy: string; action?: React.ReactNode }) {
  return (
    <div className="grid min-h-44 place-items-center rounded-xl border border-dashed border-line-strong bg-surface-2/60 px-5 py-10 text-center">
      <div>
        <div className="text-[14px] font-semibold text-fg">{title}</div>
        <p className="mx-auto mt-1.5 max-w-md text-[13px] leading-5 text-fg-muted">{copy}</p>
        {action ? <div className="mt-4 flex justify-center">{action}</div> : null}
      </div>
    </div>
  )
}

export function SectionLabel({ children, className, action }: { children: React.ReactNode; className?: string; action?: React.ReactNode }) {
  return (
    <div className={cn("mb-2.5 flex items-center justify-between gap-3", className)}>
      <h3 className="text-[12px] font-semibold uppercase tracking-[0.06em] text-fg-muted">{children}</h3>
      {action}
    </div>
  )
}
