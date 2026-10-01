import type { APIRequest } from "@/lib/media-manager-api"
import type { StatusTone } from "@/lib/media-manager-data"

export type RequestFilter = "all" | "active" | "completed" | "attention" | "movie" | "series"

export const requestAttentionStates = new Set(["needs_attention", "held", "blocked", "quarantined", "transport_unknown", "not_approved", "failed"])
export const requestCompletedStates = new Set(["plex_confirmed", "available"])
export const requestActiveStates = new Set(["approved_in_seerr", "monitored", "waiting", "searching", "candidate_selected", "submitting", "queued", "streaming_ready", "import_complete", "verifying", "publishing", "plex_scanning"])

export function requestLifecycleTone(lifecycle: string): StatusTone {
  if (lifecycle === "plex_confirmed" || lifecycle === "available") return "good"
  if (lifecycle === "failed" || lifecycle === "declined" || lifecycle === "cancelled") return "bad"
  if (requestAttentionStates.has(lifecycle)) return "warn"
  if (requestActiveStates.has(lifecycle)) return "info"
  return "neutral"
}

export function requestLifecycleLabel(lifecycle: string): string {
  if (lifecycle === "plex_confirmed") return "Plex confirmed"
  if (lifecycle === "available") return "Available"
  if (lifecycle === "not_approved") return "Awaiting approval"
  if (lifecycle === "approved_in_seerr") return "Approved in Seerr"
  return lifecycle.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase())
}

export function seerrStateLabel(state: string): string {
  switch (state) {
    case "1": return "Pending approval"
    case "2": return "Approved"
    case "3": return "Declined"
    case "4": return "Failed"
    case "5": return "Available"
    default: return state ? `State ${state}` : "Unknown"
  }
}

/** Explain admission separately from acquisition, using the recorded outcome. */
export function requestProgressMessage(item: APIRequest): string {
  if (item.attention_detail || item.intake_error) return item.attention_detail || item.intake_error || ""
  if (item.lifecycle === "waiting") {
    if (item.waiting_reason === "no eligible release yet") return "Added to Media Manager. No eligible release was found."
    if (item.waiting_reason) return item.waiting_reason
    if (item.workflow_count > 0) return "Added to Media Manager; acquisition is waiting."
  }
  if (item.lifecycle === "approved_in_seerr" || (item.lifecycle === "monitored" && item.workflow_count === 0)) return "Approved in Seerr; Media Manager intake has not created a workflow yet."
  return ""
}

export function requestMatchesFilter(item: APIRequest, filter: RequestFilter): boolean {
  if (filter === "movie") return item.media_kind === "movie"
  if (filter === "series") return item.media_kind === "series"
  if (filter === "active") return requestActiveStates.has(item.lifecycle)
  if (filter === "completed") return requestCompletedStates.has(item.lifecycle)
  if (filter === "attention") return requestAttentionStates.has(item.lifecycle)
  return true
}

export function filterRequests(items: APIRequest[], filter: RequestFilter, query: string): APIRequest[] {
  const needle = query.trim().toLocaleLowerCase()
  return items.filter((item) => {
    if (!requestMatchesFilter(item, filter)) return false
    if (!needle) return true
    const haystack = `${item.title} ${item.year ?? ""} ${item.requester ?? ""} ${item.request_id}`.toLocaleLowerCase()
    return haystack.includes(needle)
  })
}

export type RequestFilterCounts = { total: number; active: number; completed: number; attention: number; movie: number; series: number }

export function requestFilterCounts(items: APIRequest[]): RequestFilterCounts {
  return {
    total: items.length,
    active: items.filter((item) => requestActiveStates.has(item.lifecycle)).length,
    completed: items.filter((item) => requestCompletedStates.has(item.lifecycle)).length,
    attention: items.filter((item) => requestAttentionStates.has(item.lifecycle)).length,
    movie: items.filter((item) => item.media_kind === "movie").length,
    series: items.filter((item) => item.media_kind === "series").length,
  }
}
