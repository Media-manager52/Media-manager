/**
 * Canonical UI classification of persisted workflow / media states.
 *
 * The backend owns the durable state machine. The web UI only groups those
 * states for presentation. Keeping the grouping in one pure module prevents
 * "currently acquiring" from quietly absorbing normal waiting, held, or
 * monitored work that needs no owner action.
 */
import type { APIWantedItem, APIWorkflow } from "@/lib/media-manager-api"

/** Real ongoing acquisition work: a worker or provider is actively involved. */
export const activelyAcquiringStates = new Set([
  "searching",
  "candidate_selected",
  "submitting",
  "queued",
  "downloading",
  "streaming_ready",
  "importing",
  "import_complete",
  "verifying",
  "publishing",
  "plex_scanning",
  "running",
  "retrying",
  "restore_confirming",
])

/**
 * Normal waiting and monitoring. No owner action is required; automatic
 * pacing, discovery, and future air dates own the next step.
 */
export const waitingStates = new Set([
  "monitored",
  "waiting",
  "planned",
  "held",
  "retained",
  "awaiting_commission",
])

/** States that are complete and owner-visible as finished work. */
export const completedStates = new Set(["plex_confirmed", "available", "delivered", "succeeded", "success", "restored"])

/** States that genuinely pause for an owner decision or recovery. */
export const ownerAttentionStates = new Set([
  "needs_attention",
  "blocked",
  "quarantined",
  "transport_unknown",
  "failed",
  "declined",
  "cancelled",
  "restore_attention",
])

export function isActivelyAcquiring(state: string | undefined | null): boolean {
  return Boolean(state && activelyAcquiringStates.has(state))
}

export function isWaiting(state: string | undefined | null): boolean {
  return Boolean(state && waitingStates.has(state))
}

export function isCompleted(state: string | undefined | null): boolean {
  return Boolean(state && completedStates.has(state))
}

/**
 * Owner attention is driven by the durable `owner_action_required` flag when
 * present, and otherwise falls back to the explicit attention states. A
 * terminal failure without an owner flag is not claimed as a needed decision.
 */
export function requiresOwnerAttention(state: string | undefined | null, ownerActionRequired?: boolean): boolean {
  if (ownerActionRequired) return true
  return Boolean(state && ownerAttentionStates.has(state))
}

export type OwnerAttentionEntry = {
  id: string
  workflowId?: string
  title: string
  year?: number
  mediaId: string
  mediaKind: "movie" | "series"
  variant: string
  state: string
  reason: string
  href: string
}

/**
 * One truthful owner-attention list shared by the overview and the shell bell.
 * A wanted item carries its own reason; a workflow without a wanted row still
 * participates when it is explicitly owner-gated.
 */
export function collectOwnerAttention(items: APIWantedItem[] = [], workflows: APIWorkflow[] = []): OwnerAttentionEntry[] {
  const entries: OwnerAttentionEntry[] = []
  for (const item of items) {
    if (!item.owner_action_required) continue
    entries.push({
      id: item.workflow_id,
      workflowId: item.workflow_id,
      title: item.title,
      year: item.year,
      mediaId: item.media_id,
      mediaKind: item.media_kind,
      variant: item.variant,
      state: item.current_state || "needs_attention",
      reason: item.attention_detail?.trim() || "Media Manager paused this workstream for an owner decision.",
      href: item.media_kind === "movie" ? `/media/movies/${item.media_id}` : `/media/series/${item.media_id}`,
    })
  }
  for (const workflow of workflows) {
    if (!requiresOwnerAttention(workflow.state, workflow.owner_action_required)) continue
    if (entries.some((entry) => entry.workflowId === workflow.id)) continue
    entries.push({
      id: workflow.id,
      workflowId: workflow.id,
      title: workflow.title,
      year: workflow.year,
      mediaId: workflow.media_id,
      mediaKind: workflow.media_kind,
      variant: workflow.variant,
      state: workflow.state,
      reason: workflow.attention_detail?.trim() || workflow.attention_code?.replaceAll("_", " ") || "Media Manager paused this workstream for an owner decision.",
      href: workflow.media_kind === "movie" ? `/media/movies/${workflow.media_id}` : `/media/series/${workflow.media_id}`,
    })
  }
  return entries
}
