/**
 * Control-room pipeline stages. The backend owns the durable workflow state
 * machine; this module only groups persisted states into the stages an owner
 * reads left to right. Unknown states fall into "requested" rather than
 * pretending to be further along, and only exact Plex confirmation counts as
 * "in Plex".
 */
import type { APIWorkflow } from "@/lib/media-manager-api"

export type PipelineStage = "attention" | "requested" | "searching" | "downloading" | "verifying" | "publishing" | "in_plex" | "stopped"

export const pipelineStages: Array<{ key: PipelineStage; label: string; hint: string }> = [
  { key: "attention", label: "Needs you", hint: "Paused for a decision only you can make." },
  { key: "requested", label: "Requested", hint: "Monitored or waiting within indexer pacing." },
  { key: "searching", label: "Searching", hint: "Searching indexers or holding a selected release." },
  { key: "downloading", label: "Downloading", hint: "Submitted to AltMount and streaming in." },
  { key: "verifying", label: "Verifying", hint: "Inspecting the real output: episodes and subtitles." },
  { key: "publishing", label: "Publishing", hint: "Linking into the library and waiting for Plex." },
  { key: "in_plex", label: "In Plex", hint: "Plex confirmed the exact path and identity." },
]

const stageByState: Record<string, PipelineStage> = {
  needs_attention: "attention",
  blocked: "attention",
  quarantined: "attention",
  transport_unknown: "attention",
  monitored: "requested",
  waiting: "requested",
  held: "requested",
  planned: "requested",
  searching: "searching",
  candidate_selected: "searching",
  submitting: "downloading",
  queued: "downloading",
  downloading: "downloading",
  streaming_ready: "downloading",
  import_complete: "verifying",
  verifying: "verifying",
  publishing: "publishing",
  plex_scanning: "publishing",
  plex_confirmed: "in_plex",
  cancelled: "stopped",
}

export function pipelineStage(workflow: Pick<APIWorkflow, "state" | "owner_action_required">): PipelineStage {
  if (workflow.owner_action_required) return "attention"
  return stageByState[workflow.state] ?? "requested"
}

/** Groups workflows by stage, newest activity first; cancelled work is omitted. */
export function groupPipeline(workflows: APIWorkflow[]): Record<PipelineStage, APIWorkflow[]> {
  const groups: Record<PipelineStage, APIWorkflow[]> = { attention: [], requested: [], searching: [], downloading: [], verifying: [], publishing: [], in_plex: [], stopped: [] }
  for (const workflow of workflows) groups[pipelineStage(workflow)].push(workflow)
  for (const list of Object.values(groups)) list.sort((left, right) => new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime())
  return groups
}
