/**
 * Fallback rows for the Evidence explorer, used only when the server attached
 * no structured `decision_explanation.evidence` to a candidate.
 *
 * The Go store writes `normalized_facts` as a JSON array of facts
 * (`[{name, values, state, ...}]`). A plain `{fact: value}` object, which carries
 * no per-fact state, only comes from hand-written data and the empty `{}` default. The helper reads both
 * shapes and never invents a row or a state: a fact whose state was not stored
 * is labelled "not_recorded", not the provider's "unknown".
 */

export const evidenceStateNotRecorded = "not_recorded"

const recordedStates = new Set(["observed", "inferred", "unknown", "conflicting"])

export type FallbackEvidenceRow = { fact: string; value: string; state: string }

export function formatFactValue(value: unknown): string {
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") return String(value)
  if (Array.isArray(value)) return value.length ? value.map(String).join(", ") : "Unknown"
  return value ? JSON.stringify(value) : "Unknown"
}

export function fallbackEvidenceRows(normalizedFacts: unknown, limit = 20): FallbackEvidenceRow[] {
  if (Array.isArray(normalizedFacts)) {
    const rows: FallbackEvidenceRow[] = []
    for (const entry of normalizedFacts) {
      if (rows.length >= limit) break
      if (!entry || typeof entry !== "object") continue
      const { name, values, state } = entry as { name?: unknown; values?: unknown; state?: unknown }
      if (typeof name !== "string" || name.trim() === "") continue
      rows.push({
        fact: name,
        value: formatFactValue(values ?? []),
        state: typeof state === "string" && recordedStates.has(state) ? state : evidenceStateNotRecorded,
      })
    }
    return rows
  }
  if (normalizedFacts && typeof normalizedFacts === "object") {
    return Object.entries(normalizedFacts).slice(0, limit).map(([fact, value]) => ({ fact, value: formatFactValue(value), state: evidenceStateNotRecorded }))
  }
  return []
}

/**
 * Release-group evidence from a candidate's stored facts.
 *
 * The group is read from the structured fact array (`[{name, values, state}]`)
 * that the store actually writes; there is no `{group: "X"}` object shape in
 * persisted data, so none is accepted here. Evidence stays three-valued, the
 * same way the evaluator treats it:
 *
 *  - a usable fact with exactly one distinct value names the group;
 *  - an absent fact, an `unknown` fact, or one with no values is `unknown`;
 *  - a `conflicting` fact, or one that carries several distinct values, is
 *    `conflicting`. The candidate is not attributed to any one of the values,
 *    and it is not hidden as `unknown` either.
 */
export type ReleaseGroupEvidence =
  | { kind: "group"; name: string }
  | { kind: "unknown" }
  | { kind: "conflicting"; values: string[] }

// `release_group` is preferred over `group`, the name the Newznab adapter writes.
const releaseGroupFactNames = ["release_group", "group"]

function distinctValues(values: unknown): string[] {
  if (!Array.isArray(values)) return []
  const seen = new Set<string>()
  const result: string[] = []
  for (const value of values) {
    const text = typeof value === "string" ? value.trim() : ""
    if (!text || seen.has(text.toLowerCase())) continue
    seen.add(text.toLowerCase())
    result.push(text)
  }
  return result
}

function factEvidence(facts: Array<{ name?: unknown; values?: unknown; state?: unknown }>, name: string): ReleaseGroupEvidence {
  const matching = facts.filter((fact) => typeof fact.name === "string" && fact.name.trim().toLowerCase() === name)
  if (matching.length === 0) return { kind: "unknown" }
  if (matching.some((fact) => fact.state === "conflicting")) return { kind: "conflicting", values: distinctValues(matching.flatMap((fact) => Array.isArray(fact.values) ? fact.values : [])) }
  const usable = matching.filter((fact) => fact.state !== "unknown")
  const values = distinctValues(usable.flatMap((fact) => Array.isArray(fact.values) ? fact.values : []))
  if (values.length === 0) return { kind: "unknown" }
  return values.length === 1 ? { kind: "group", name: values[0] } : { kind: "conflicting", values }
}

export function releaseGroupEvidence(normalizedFacts: unknown): ReleaseGroupEvidence {
  if (!Array.isArray(normalizedFacts)) return { kind: "unknown" }
  const facts = normalizedFacts.filter((entry): entry is { name?: unknown; values?: unknown; state?: unknown } => Boolean(entry) && typeof entry === "object")
  // The first fact name with any conclusive evidence decides, so a conflicting
  // release_group is reported as conflicting rather than overruled by `group`.
  for (const name of releaseGroupFactNames) {
    const evidence = factEvidence(facts, name)
    if (evidence.kind !== "unknown") return evidence
  }
  return { kind: "unknown" }
}

export type ReleaseGroupSummary = {
  key: string
  kind: ReleaseGroupEvidence["kind"]
  name: string
  observations: number
  eligible: number
  held: number
  rejected: number
}

const groupBucketNames = { unknown: "Unknown", conflicting: "Conflicting evidence" } as const

/**
 * Counts candidates per observed release group. Real groups come first, ordered
 * by observation count; the "Conflicting evidence" and "Unknown" buckets follow
 * so that missing evidence never reads as a group of its own. Keys are kept
 * apart so a group literally named "Unknown" cannot merge into the bucket.
 */
export function summarizeReleaseGroups(candidates: ReadonlyArray<{ normalized_facts?: unknown; verdict?: string }>): ReleaseGroupSummary[] {
  const summaries = new Map<string, ReleaseGroupSummary>()
  for (const candidate of candidates) {
    const evidence = releaseGroupEvidence(candidate.normalized_facts)
    const key = evidence.kind === "group" ? `group:${evidence.name}` : evidence.kind
    const name = evidence.kind === "group" ? evidence.name : groupBucketNames[evidence.kind]
    const summary = summaries.get(key) ?? { key, kind: evidence.kind, name, observations: 0, eligible: 0, held: 0, rejected: 0 }
    summary.observations += 1
    if (candidate.verdict === "eligible") summary.eligible += 1
    if (candidate.verdict === "held") summary.held += 1
    if (candidate.verdict === "rejected") summary.rejected += 1
    summaries.set(key, summary)
  }
  const rank = { group: 0, conflicting: 1, unknown: 2 } as const
  return Array.from(summaries.values()).sort((left, right) => rank[left.kind] - rank[right.kind] || right.observations - left.observations || left.name.localeCompare(right.name))
}
