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

export type EpisodeStatus = "acquired" | "acquiring" | "attention" | "unaired" | "undated" | "missing"

/**
 * One status per expected episode, derived only from persisted workflows,
 * files, and the provider air date. A season pack confirmed in Plex covers
 * every episode. An episode without an air date is "undated": it is neither
 * aired nor unaired, so it never makes a season look like it is airing.
 */
export function episodeStatus(episode: { number: number; air_date?: string }, rollup: SeriesSeasonRollup | undefined, today = new Date()): { status: EpisodeStatus; workflow?: APIWorkflow } {
  const workflow = rollup?.episodeWorkflows.find((entry) => { const parsed = parseSeriesVariant(entry.variant); return parsed.kind === "episode" && parsed.episode === episode.number })
  const packConfirmed = rollup?.packWorkflow?.state === "plex_confirmed" || (rollup?.packFiles ?? []).some((file) => file.state === "current" && file.plex_confirmed)
  if (packConfirmed || rollup?.acquiredEpisodes.includes(episode.number)) return { status: "acquired", workflow }
  if (rollup?.attentionEpisodes.includes(episode.number)) return { status: "attention", workflow }
  if (rollup?.acquiringEpisodes.includes(episode.number)) return { status: "acquiring", workflow }
  const startOfToday = new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime()
  if (!episode.air_date) return { status: "undated", workflow }
  if (new Date(`${episode.air_date.slice(0, 10)}T00:00:00`).getTime() >= startOfToday) return { status: "unaired", workflow }
  return { status: "missing", workflow }
}

export type SeasonAddCheck = { status: "ready" } | { status: "confirm"; message: string } | { status: "blocked"; message: string }

/**
 * Whether the title page may add a season workflow. A season that already has
 * an active season workflow is refused: the server keeps one active workflow
 * per scope, and the existing one is where the owner acts. A season already in
 * Plex, partly in Plex, or with episode work in progress is allowed only after
 * an explicit confirmation, because adding it starts a new season search.
 */
export function seasonAddCheck(season: number, workflows: APIWorkflow[], rollup: SeriesSeasonRollup | undefined, expectedEpisodes = 0): SeasonAddCheck {
  const finished = (workflow: APIWorkflow) => Boolean(workflow.terminal_at) || workflow.state === "plex_confirmed" || workflow.state === "cancelled"
  const active = workflows.find((workflow) => workflow.variant === formatSeasonVariant(season) && !finished(workflow))
  if (active) return { status: "blocked", message: `Season ${season} already has an active workflow (${active.state.replaceAll("_", " ")}). Open it in Workflows instead of adding another.` }
  const packInPlex = rollup?.packWorkflow?.state === "plex_confirmed" || (rollup?.packFiles ?? []).some((file) => file.state === "current" && file.plex_confirmed)
  const acquired = rollup?.acquiredEpisodes.length ?? 0
  const of = expectedEpisodes > 0 ? ` of ${expectedEpisodes}` : ""
  if (packInPlex || (expectedEpisodes > 0 && acquired >= expectedEpisodes)) return { status: "confirm", message: `Season ${season} is already in Plex${expectedEpisodes > 0 ? ` (${expectedEpisodes}${of} episodes)` : ""}. Adding it again starts a new search for a replacement season pack.` }
  if (acquired > 0) return { status: "confirm", message: `Season ${season} is partly in Plex (${acquired}${of} episodes). Adding it starts a season-pack search alongside what is already there.` }
  const activeEpisodes = (rollup?.episodeWorkflows ?? []).filter((workflow) => !finished(workflow)).length
  if (activeEpisodes > 0) return { status: "confirm", message: `Season ${season} already has ${activeEpisodes} episode workflow${activeEpisodes === 1 ? "" : "s"} in progress. Adding the season also searches for a season pack.` }
  return { status: "ready" }
}
