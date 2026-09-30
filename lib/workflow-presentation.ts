/**
 * Plain-language presentation of persisted workflow state. The backend owns
 * the durable state machine; these helpers only describe it. Nothing here
 * invents progress: the stepper position comes from the persisted state, and
 * unknown states render at the start rather than further along.
 */

/** The eight acquisition steps an owner reads left to right. */
export const workflowSteps = ["Search", "Select", "Submit", "Download", "Import", "Verify", "Publish", "Plex"] as const

const stepByState: Record<string, number> = {
  searching: 0,
  candidate_selected: 1,
  submitting: 2,
  queued: 2,
  downloading: 3,
  streaming_ready: 3,
  import_complete: 4,
  verifying: 5,
  publishing: 6,
  plex_scanning: 6,
  plex_confirmed: 7,
}

/**
 * Index of the current step (0-7), or -1 when the state is not an acquisition
 * step (waiting, monitored, held, attention). `complete` is true only for exact
 * Plex confirmation.
 */
export function workflowStep(state: string): { current: number; complete: boolean } {
  const current = stepByState[state] ?? -1
  return { current, complete: state === "plex_confirmed" }
}

/**
 * A short owner-facing headline for an attention code. The persisted detail is
 * always shown next to it; this only names the kind of problem.
 */
export function attentionHeadline(code: string | undefined, state?: string): { label: string; tone: "warn" | "bad" } {
  const value = code ?? ""
  if (value.startsWith("subtitle") || value.includes("verification") || value === "output_inspection_failed") return { label: "Verification failed", tone: "warn" }
  if (value.startsWith("round_exhausted") || value === "no_acceptable_release" || value === "no_eligible_candidate") return { label: "No acceptable release", tone: "warn" }
  if (value.startsWith("altmount") || value === "infrastructure_attention") return { label: "Download client needs you", tone: "warn" }
  if (value === "season_expectation_missing") return { label: "Season details missing", tone: "warn" }
  if (state === "quarantined" || state === "transport_unknown") return { label: "Owner action required", tone: "bad" }
  return { label: "Needs your decision", tone: "warn" }
}

/**
 * When Media Manager will try again on its own, in words. Only persisted
 * schedule facts are used; with none, it says so plainly.
 */
export function retryPlan(workflow: { retry_discovery_state?: string; next_rescan_at?: string } | undefined, now = Date.now()): string {
  if (!workflow) return "Will not retry automatically"
  if (workflow.retry_discovery_state === "ready") return "New release found · waiting for your approval"
  if (workflow.next_rescan_at) {
    const minutes = Math.round((new Date(workflow.next_rescan_at).getTime() - now) / 60000)
    if (minutes <= 0) return "Automatic retry due now"
    if (minutes < 60) return `Auto-retry in ${minutes}m`
    const hours = Math.floor(minutes / 60)
    if (hours < 48) return `Auto-retry in ${hours}h ${minutes % 60}m`
    return `Auto-retry ${new Date(workflow.next_rescan_at).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" })}`
  }
  if (workflow.retry_discovery_state === "waiting") return "Waiting for a new eligible release"
  return "Will not retry automatically"
}
