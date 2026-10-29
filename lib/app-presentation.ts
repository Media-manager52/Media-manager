/**
 * Plain-language presentation for the everyday app. The backend owns every
 * durable state and decides which owner actions apply; these pure helpers only
 * put them into words an owner reads at a glance. Nothing here invents
 * progress: stages come from persisted workflow states and unknown states fall
 * back to the least advanced description.
 */
import type { APIRequest, APIWorkflow, APIWorkflowAction } from "@/lib/media-manager-api"
import { activelyAcquiringStates, isCompleted, isWaiting, requiresOwnerAttention } from "@/lib/workflow-states"

export type AppStage = "needs_you" | "finding" | "downloading" | "checking" | "adding" | "in_plex" | "waiting" | "not_started"
export type AppTone = "ok" | "info" | "warn" | "muted"

/** The five steps an owner sees on a title page, left to right. */
export const appSteps = ["Requested", "Finding a copy", "Downloading", "Checking", "In Plex"] as const

const stageByState: Record<string, AppStage> = {
  searching: "finding",
  candidate_selected: "finding",
  submitting: "finding",
  running: "finding",
  retrying: "finding",
  queued: "downloading",
  downloading: "downloading",
  streaming_ready: "downloading",
  importing: "checking",
  import_complete: "checking",
  verifying: "checking",
  publishing: "adding",
  plex_scanning: "adding",
}

/** The owner-facing stage of one workflow. */
export function workflowStage(workflow: Pick<APIWorkflow, "state" | "owner_action_required">): AppStage {
  if (requiresOwnerAttention(workflow.state, workflow.owner_action_required)) return "needs_you"
  const active = stageByState[workflow.state]
  if (active) return active
  if (isCompleted(workflow.state)) return "in_plex"
  if (isWaiting(workflow.state)) return "waiting"
  return "not_started"
}

const stageOrder: AppStage[] = ["not_started", "waiting", "finding", "downloading", "checking", "adding", "in_plex"]

export type StageInfo = { stage: AppStage; label: string; short: string; tone: AppTone; step: number }

/**
 * Label, tone and progress step for a stage. `step` is the index into
 * appSteps that is current; appSteps.length means every step is done.
 */
export function stageInfo(stage: AppStage, workflow?: Pick<APIWorkflow, "download_percent" | "attention_code">): StageInfo {
  switch (stage) {
    case "needs_you": return { stage, label: "Needs you", short: "Needs you", tone: "warn", step: attentionStep(workflow?.attention_code) }
    case "finding": return { stage, label: "Finding a copy", short: "Finding", tone: "info", step: 1 }
    case "downloading": {
      const percent = workflow?.download_percent
      const known = typeof percent === "number" && Number.isFinite(percent)
      const rounded = known ? Math.max(0, Math.min(100, Math.round(percent))) : 0
      return { stage, label: known ? `Downloading ${rounded}%` : "Downloading", short: known ? `${rounded}%` : "Downloading", tone: "info", step: 2 }
    }
    case "checking": return { stage, label: "Checking the files", short: "Checking", tone: "info", step: 3 }
    case "adding": return { stage, label: "Adding to Plex", short: "Adding", tone: "info", step: 4 }
    case "in_plex": return { stage, label: "In Plex", short: "In Plex", tone: "ok", step: appSteps.length }
    case "waiting": return { stage, label: "Waiting for a release", short: "Waiting", tone: "muted", step: 1 }
    default: return { stage, label: "Not in Plex yet", short: "Not yet", tone: "muted", step: 0 }
  }
}

/** Which step a problem stopped at, from its attention code. */
function attentionStep(code: string | undefined): number {
  const value = code ?? ""
  if (value.startsWith("subtitle") || value.includes("verification") || value.includes("inspection")) return 3
  if (value.startsWith("altmount") || value === "infrastructure_attention" || value.includes("download")) return 2
  if (value.startsWith("plex")) return 4
  return 1
}

export type TitleStatus = StageInfo & { workflow?: APIWorkflow }

/**
 * One headline status for a title. A decision the owner must make wins, then
 * the most advanced active work, then a confirmed file in Plex, then waiting.
 */
export function titleStatus(workflows: APIWorkflow[], availableInPlex: boolean): TitleStatus {
  const needs = workflows.find((workflow) => workflowStage(workflow) === "needs_you")
  if (needs) return { ...stageInfo("needs_you", needs), workflow: needs }
  let best: { stage: AppStage; workflow: APIWorkflow } | undefined
  for (const workflow of workflows) {
    const stage = workflowStage(workflow)
    if (!activelyAcquiringStates.has(workflow.state) || stage === "in_plex") continue
    if (!best || stageOrder.indexOf(stage) > stageOrder.indexOf(best.stage)) best = { stage, workflow }
  }
  if (best) return { ...stageInfo(best.stage, best.workflow), workflow: best.workflow }
  if (availableInPlex) return stageInfo("in_plex")
  const waiting = workflows.find((workflow) => workflowStage(workflow) === "waiting")
  if (waiting) return { ...stageInfo("waiting", waiting), workflow: waiting }
  return stageInfo("not_started")
}

/** A short, plain headline for why a workflow stopped for the owner. */
export function attentionTitle(workflow: Pick<APIWorkflow, "attention_code" | "state" | "access_rejected_candidate_id" | "retry_discovery_state">): string {
  const code = workflow.attention_code ?? ""
  if (workflow.access_rejected_candidate_id) return "The indexer refused the download"
  if (workflow.retry_discovery_state === "ready") return "A new copy was found"
  if (code.startsWith("subtitle") || code.includes("verification") || code === "output_inspection_failed") return "The download didn’t pass the check"
  if (code.startsWith("round_exhausted") || code === "no_acceptable_release" || code === "no_eligible_candidate") return "No good copy found yet"
  if (code === "plex_timeout") return "Plex didn’t confirm the file"
  if (code.startsWith("altmount") || code === "infrastructure_attention") return "The download service needs a look"
  if (code === "season_expectation_missing") return "The episode list is missing"
  if (workflow.state === "quarantined" || workflow.state === "transport_unknown") return "A download got stuck"
  return "Waiting for your decision"
}

/** Owner-facing names for the server's workflow actions, in the order offered. */
const actionLabels: Record<string, string> = {
  retry_access_rejected_release: "Retry the download",
  open_next_round: "Try the new copy",
  reconfirm_plex: "Ask Plex again",
  reconcile_altmount: "Check the download again",
  open_round_from_known: "Try other copies",
  search: "Search now",
  discover: "Look for new copies",
  cancel: "Stop",
}

export type OfferedAction = { label: string; action: APIWorkflowAction; primary: boolean; confirm: boolean }

/**
 * The server-listed actions the everyday app offers for one workflow. Only
 * available actions without an owner confirmation phrase are offered; those
 * that need a phrase stay in the expert console. Cancel always comes last and
 * asks first.
 */
export function offeredActions(workflow: Pick<APIWorkflow, "available_actions"> | undefined, limit = 3): OfferedAction[] {
  const available = (workflow?.available_actions ?? []).filter((action) => action.available && !action.confirmation && actionLabels[action.action])
  const ordered = Object.keys(actionLabels).flatMap((name) => available.filter((action) => action.action === name))
  const regular = ordered.filter((action) => action.action !== "cancel").slice(0, Math.max(0, limit - 1))
  const cancel = ordered.find((action) => action.action === "cancel")
  const chosen = cancel ? [...regular, cancel] : regular
  return chosen.map((action, index) => ({ label: actionLabels[action.action], action, primary: index === 0 && action.action !== "cancel", confirm: action.action === "cancel" }))
}

/** True while Seerr still waits for the owner to approve the request. */
export function awaitingApproval(request: Pick<APIRequest, "seerr_state" | "lifecycle">): boolean {
  return request.seerr_state === "1" || request.lifecycle === "not_approved"
}

/** Plain status for a Seerr request. */
export function requestStatus(request: Pick<APIRequest, "seerr_state" | "lifecycle" | "attention_count" | "workflow_count">): { label: string; tone: AppTone; group: "waiting_for_you" | "needs_you" | "on_the_way" | "in_plex" | "closed" } {
  const lifecycle = request.lifecycle
  if (awaitingApproval(request)) return { label: "Waiting for your OK", tone: "warn", group: "waiting_for_you" }
  if (lifecycle === "declined" || request.seerr_state === "3") return { label: "Declined", tone: "muted", group: "closed" }
  if (lifecycle === "cancelled") return { label: "Stopped", tone: "muted", group: "closed" }
  if (lifecycle === "plex_confirmed" || lifecycle === "available") return { label: "In Plex", tone: "ok", group: "in_plex" }
  if (request.attention_count > 0 || requiresOwnerAttention(lifecycle)) return { label: "Needs you", tone: "warn", group: "needs_you" }
  if (lifecycle === "approved_in_seerr" || (lifecycle === "monitored" && request.workflow_count === 0)) return { label: "Approved — being added", tone: "info", group: "on_the_way" }
  const stage = workflowStage({ state: lifecycle })
  return { label: stageInfo(stage).label, tone: stage === "waiting" ? "muted" : "info", group: "on_the_way" }
}

/** "3 hours ago", "yesterday", "in 2 hours": coarse words, never false precision. */
export function relativeTime(value: string | undefined, now = Date.now()): string {
  if (!value) return ""
  const at = Date.parse(value)
  if (!Number.isFinite(at)) return ""
  const minutes = Math.round((at - now) / 60000)
  const future = minutes > 0
  const size = Math.abs(minutes)
  const phrase = (amount: number, unit: string) => `${amount} ${unit}${amount === 1 ? "" : "s"}`
  let words: string
  if (size < 1) return "just now"
  if (size < 60) words = phrase(size, "minute")
  else if (size < 60 * 24) words = phrase(Math.round(size / 60), "hour")
  else if (size < 60 * 48) return future ? "tomorrow" : "yesterday"
  else if (size < 60 * 24 * 14) words = phrase(Math.round(size / 1440), "day")
  else if (size < 60 * 24 * 60) words = phrase(Math.round(size / 10080), "week")
  else words = phrase(Math.round(size / 43200), "month")
  return future ? `in ${words}` : `${words} ago`
}

/** What happens next for a waiting workflow, in words. */
export function waitingDetail(workflow: Pick<APIWorkflow, "next_rescan_at" | "retry_discovery_state"> | undefined, now = Date.now()): string {
  if (!workflow) return ""
  if (workflow.retry_discovery_state === "ready") return "A new copy was found and is waiting for you"
  if (workflow.next_rescan_at && Date.parse(workflow.next_rescan_at) > now) return `Next search ${relativeTime(workflow.next_rescan_at, now)}`
  if (workflow.next_rescan_at) return "Searching again shortly"
  return "Media Manager keeps looking automatically"
}

/** "Danish" for "da"; unknown codes stay as they are. */
export function languageName(code: string): string {
  try {
    return new Intl.DisplayNames(["en"], { type: "language" }).of(code) ?? code
  } catch {
    return code
  }
}

/** A stable, muted backdrop colour per title for artwork-less posters. */
export function posterTone(title: string): string {
  let hash = 0
  for (let index = 0; index < title.length; index += 1) hash = (hash * 31 + title.charCodeAt(index)) >>> 0
  return `hsl(${hash % 360} 24% 26%)`
}
