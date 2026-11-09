import type { APIWorkflow } from "@/api/client"
import { parseSeriesVariant } from "@/lib/series-variants"

export function workflowHref(workflow: Pick<APIWorkflow, "id">): string {
  return `/arbejde/${workflow.id}`
}

export function titleHref(mediaId: string): string {
  return `/bibliotek/${mediaId}`
}

/** "Film", "Sæson 2", "S02E05" — the scope one workflow covers. */
export function describeVariant(workflow: Pick<APIWorkflow, "variant" | "media_kind" | "edition">): string {
  const label = describeScope(workflow)
  return workflow.edition === "4k" ? `${label} · 4K` : label
}

/** "Sæson 2" or "S02E05" for a series variant; empty for films and whole titles. */
export function scopeLabel(variant: string | undefined): string {
  if (!variant) return ""
  const parsed = parseSeriesVariant(variant)
  if (parsed.kind === "season") return `Sæson ${parsed.season}`
  if (parsed.kind === "episode") return `S${String(parsed.season).padStart(2, "0")}E${String(parsed.episode).padStart(2, "0")}`
  return ""
}

function describeScope(workflow: Pick<APIWorkflow, "variant" | "media_kind">): string {
  if (workflow.media_kind === "movie") return "Film"
  return scopeLabel(workflow.variant) || (workflow.variant === "default" ? "Hele serien" : workflow.variant)
}
