import type { APIWorkflow } from "@/api/client"
import { parseSeriesVariant } from "@/lib/series-variants"

export function workflowHref(workflow: Pick<APIWorkflow, "id">): string {
  return `/arbejde/${workflow.id}`
}

export function titleHref(mediaId: string): string {
  return `/bibliotek/${mediaId}`
}

/** "Film", "Sæson 2", "S02E05" — the scope one workflow covers. */
export function describeVariant(workflow: Pick<APIWorkflow, "variant" | "media_kind">): string {
  if (workflow.media_kind === "movie") return "Film"
  const parsed = parseSeriesVariant(workflow.variant)
  if (parsed.kind === "season") return `Sæson ${parsed.season}`
  if (parsed.kind === "episode") return `S${String(parsed.season).padStart(2, "0")}E${String(parsed.episode).padStart(2, "0")}`
  return workflow.variant === "default" ? "Hele serien" : workflow.variant
}
