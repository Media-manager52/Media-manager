import type { APIManagedFile, APIWorkflow } from "@/lib/media-manager-api"
import { isActivelyAcquiring, requiresOwnerAttention } from "@/lib/workflow-states"

/**
 * One parser for every durable series variant the product can persist.
 *
 * Media Manager encodes two shapes today:
 *   season:NN                 a complete-season (pack) scope
 *   season:NN:episode:EE      one released episode of an airing season
 *
 * TV presentation must roll both shapes into the same season. Scattering
 * `^season:\d+$` regexes across screens made episode workflows invisible to the
 * inspector, so every screen and test now goes through this helper.
 */
export type ParsedSeriesVariant =
  | { kind: "season"; season: number }
  | { kind: "episode"; season: number; episode: number }
  | { kind: "other" }

export function parseSeriesVariant(variant: string | undefined | null): ParsedSeriesVariant {
  const value = (variant ?? "").trim()
  if (!value.startsWith("season:")) return { kind: "other" }
  const rest = value.slice("season:".length)
  const parts = rest.split(":")
  if (parts.length === 1) {
    const season = parseNumber(parts[0], 999)
    return season === undefined ? { kind: "other" } : { kind: "season", season }
  }
  if (parts.length === 3 && parts[1] === "episode") {
    const season = parseNumber(parts[0], 999)
    const episode = parseNumber(parts[2], 9999)
    if (season === undefined || episode === undefined || episode === 0) return { kind: "other" }
    return { kind: "episode", season, episode }
  }
  return { kind: "other" }
}

export function formatSeasonVariant(season: number): string {
  return `season:${String(Math.max(0, Math.trunc(season))).padStart(2, "0")}`
}

export function formatEpisodeVariant(season: number, episode: number): string {
  return `${formatSeasonVariant(season)}:episode:${String(Math.max(0, Math.trunc(episode))).padStart(2, "0")}`
}

function parseNumber(value: string, maximum: number): number | undefined {
  if (!/^\d+$/.test(value)) return undefined
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed < 0 || parsed > maximum) return undefined
  return parsed
}

export type SeriesSeasonRollup = {
  number: number
  /** The complete-season (pack) scope, which is intentionally separate from episode work. */
  packWorkflow?: APIWorkflow
  /** Every distinct episode scope, newest workflow per episode. */
  episodeWorkflows: APIWorkflow[]
  packFiles: APIManagedFile[]
  episodeFiles: APIManagedFile[]
  /** Episodes with a current Plex-confirmed file or a plex_confirmed workflow. */
  acquiredEpisodes: number[]
  /** Episodes with real ongoing acquisition work. */
  acquiringEpisodes: number[]
  /** Episodes explicitly waiting for a future release. */
  waitingEpisodes: number[]
  /** Episode workflows that need an owner decision. */
  attentionWorkflows: APIWorkflow[]
  attentionEpisodes: number[]
  /** Earliest persisted next search/rescan across the season scopes. */
  nextSearchAt?: string
}

/**
 * Rolls every season and episode workflow plus managed file into one entry per
 * season. Episode scopes never form a second, unrelated season. A confirmed
 * episode always wins over a later cancelled duplicate so covered content never
 * looks missing.
 */
export function rollupSeriesSeasons(workflows: APIWorkflow[] = [], files: APIManagedFile[] = []): SeriesSeasonRollup[] {
  const seasons = new Map<number, SeriesSeasonRollup>()
  const episodeWorkflowByEpisode = new Map<string, APIWorkflow>()

  function entry(number: number): SeriesSeasonRollup {
    const existing = seasons.get(number)
    if (existing) return existing
    const created: SeriesSeasonRollup = {
      number,
      episodeWorkflows: [],
      packFiles: [],
      episodeFiles: [],
      acquiredEpisodes: [],
      acquiringEpisodes: [],
      waitingEpisodes: [],
      attentionWorkflows: [],
      attentionEpisodes: [],
    }
    seasons.set(number, created)
    return created
  }

  for (const workflow of workflows) {
    const parsed = parseSeriesVariant(workflow.variant)
    if (parsed.kind === "season") {
      const season = entry(parsed.season)
      if (!season.packWorkflow || workflow.updated_at > season.packWorkflow.updated_at) season.packWorkflow = workflow
      continue
    }
    if (parsed.kind === "episode") {
      const key = `${parsed.season}:${parsed.episode}`
      const previous = episodeWorkflowByEpisode.get(key)
      if (!previous || isNewerEpisodeWorkflow(workflow, previous)) episodeWorkflowByEpisode.set(key, workflow)
    }
  }

  for (const workflow of episodeWorkflowByEpisode.values()) {
    const parsed = parseSeriesVariant(workflow.variant)
    if (parsed.kind !== "episode") continue
    const season = entry(parsed.season)
    season.episodeWorkflows.push(workflow)
    if (workflow.state === "plex_confirmed") season.acquiredEpisodes.push(parsed.episode)
    else if (requiresOwnerAttention(workflow.state, workflow.owner_action_required)) {
      season.attentionEpisodes.push(parsed.episode)
      season.attentionWorkflows.push(workflow)
    } else if (isActivelyAcquiring(workflow.state)) season.acquiringEpisodes.push(parsed.episode)
    else season.waitingEpisodes.push(parsed.episode)
    const next = workflow.next_rescan_at
    if (next && (!season.nextSearchAt || next < season.nextSearchAt)) season.nextSearchAt = next
  }

  for (const file of files) {
    const parsed = parseSeriesVariant(file.variant)
    if (parsed.kind === "season") entry(parsed.season).packFiles.push(file)
    else if (parsed.kind === "episode") {
      const season = entry(parsed.season)
      season.episodeFiles.push(file)
      if (file.state === "current" && file.plex_confirmed && !season.acquiredEpisodes.includes(parsed.episode)) season.acquiredEpisodes.push(parsed.episode)
    }
  }

  for (const season of seasons.values()) {
    season.episodeWorkflows.sort((left, right) => left.variant.localeCompare(right.variant))
    season.acquiredEpisodes.sort((left, right) => left - right)
    season.acquiringEpisodes.sort((left, right) => left - right)
    season.waitingEpisodes.sort((left, right) => left - right)
    season.attentionEpisodes.sort((left, right) => left - right)
  }

  return Array.from(seasons.values()).sort((left, right) => left.number - right.number)
}

function isNewerEpisodeWorkflow(candidate: APIWorkflow, current: APIWorkflow): boolean {
  // A confirmed episode can never be demoted by a later duplicate scope.
  if (current.state === "plex_confirmed" && candidate.state !== "plex_confirmed") return false
  if (candidate.state === "plex_confirmed" && current.state !== "plex_confirmed") return true
  return candidate.updated_at > current.updated_at
}
