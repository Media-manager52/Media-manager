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
 * Release-group and Usenet-newsgroup evidence from a candidate's stored facts.
 *
 * These are two different things and are never mixed:
 *
 *  - a *release group* is the team that made the release (Newznab's `team`
 *    attribute, "Team doing the release"). It is read only from a fact named
 *    `release_group`. The Newznab adapter does not write that fact today, so
 *    until a source produces it every candidate honestly counts as unknown.
 *  - a *Usenet newsgroup* is where the release was posted (Newznab's `group`
 *    attribute, "NNTP Group(s)", for example `"a.b.group, a.b.teevee"`). The
 *    adapter stores it as the fact `group`. It is not a release group and must
 *    not be presented as one.
 *
 * Facts are read from the structured array the store actually writes
 * (`[{name, values, state}]`); there is no `{group: "X"}` object shape in
 * persisted data, so none is accepted. Evidence stays three-valued, the way the
 * evaluator treats it: absent, `unknown` or value-less facts are unknown, and a
 * `conflicting` fact is conflicting rather than being attributed to one value.
 */
export type ReleaseGroupEvidence =
  | { kind: "group"; name: string }
  | { kind: "unknown" }
  | { kind: "conflicting"; values: string[] }

export type UsenetGroupEvidence =
  | { kind: "groups"; names: string[] }
  | { kind: "unknown" }
  | { kind: "conflicting"; values: string[] }

type StoredFactLike = { name?: unknown; values?: unknown; state?: unknown }
type FactReading = { kind: "unknown" } | { kind: "conflicting"; values: string[] } | { kind: "values"; values: string[] }

// `split` is for newsgroup lists: each value is comma-separated and Usenet names
// are case-insensitive, so they are folded to lower case.
function distinctValues(values: unknown[], split: boolean): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const value of values) {
    if (typeof value !== "string") continue
    for (const part of split ? value.split(",") : [value]) {
      const text = split ? part.trim().toLowerCase() : part.trim()
      if (!text || seen.has(text.toLowerCase())) continue
      seen.add(text.toLowerCase())
      result.push(text)
    }
  }
  return result
}

// One fact, by exact (case-insensitive) name. `split` treats each value as a
// comma-separated list, which is how Newznab lists several newsgroups.
function readFact(normalizedFacts: unknown, name: string, split: boolean): FactReading {
  if (!Array.isArray(normalizedFacts)) return { kind: "unknown" }
  const matching = normalizedFacts.filter((entry): entry is StoredFactLike => Boolean(entry) && typeof entry === "object")
    .filter((fact) => typeof fact.name === "string" && fact.name.trim().toLowerCase() === name)
  if (matching.length === 0) return { kind: "unknown" }
  const valuesOf = (facts: StoredFactLike[]) => distinctValues(facts.flatMap((fact) => Array.isArray(fact.values) ? fact.values : []), split)
  if (matching.some((fact) => fact.state === "conflicting")) return { kind: "conflicting", values: valuesOf(matching) }
  const values = valuesOf(matching.filter((fact) => fact.state !== "unknown"))
  return values.length === 0 ? { kind: "unknown" } : { kind: "values", values }
}

/** The release group, from the `release_group` fact only. Never from `group`. */
export function releaseGroupEvidence(normalizedFacts: unknown): ReleaseGroupEvidence {
  const reading = readFact(normalizedFacts, "release_group", false)
  if (reading.kind !== "values") return reading
  // A release has one group; several distinct values cannot all be it.
  return reading.values.length === 1 ? { kind: "group", name: reading.values[0] } : { kind: "conflicting", values: reading.values }
}

/** The Usenet newsgroups a release was posted to, from the `group` fact. */
export function usenetGroupEvidence(normalizedFacts: unknown): UsenetGroupEvidence {
  const reading = readFact(normalizedFacts, "group", true)
  // Posting to several newsgroups is normal (cross-posting), not a conflict.
  return reading.kind === "values" ? { kind: "groups", names: reading.values } : reading
}

export type ReleaseGroupSummary = {
  key: string
  kind: "group" | "unknown" | "conflicting"
  name: string
  observations: number
  eligible: number
  held: number
  rejected: number
}

type SummarizableCandidate = { normalized_facts?: unknown; verdict?: string }
const groupBucketNames = { unknown: "Unknown", conflicting: "Conflicting evidence" } as const

// Counts candidates per named group. A candidate counts once under each name it
// has. Real names come first, ordered by count; the "Conflicting evidence" and
// "Unknown" buckets follow so missing evidence never reads as a group of its
// own. Keys are kept apart so a group literally named "Unknown" cannot merge
// into the bucket.
function tally(candidates: ReadonlyArray<SummarizableCandidate>, namespace: string, resolve: (candidate: SummarizableCandidate) => { names: string[] } | { bucket: "unknown" | "conflicting" }): ReleaseGroupSummary[] {
  const summaries = new Map<string, ReleaseGroupSummary>()
  for (const candidate of candidates) {
    const resolved = resolve(candidate)
    const targets = "names" in resolved
      ? resolved.names.map((name) => ({ key: `${namespace}:${name}`, kind: "group" as const, name }))
      : [{ key: resolved.bucket, kind: resolved.bucket, name: groupBucketNames[resolved.bucket] }]
    for (const target of targets) {
      const summary = summaries.get(target.key) ?? { ...target, observations: 0, eligible: 0, held: 0, rejected: 0 }
      summary.observations += 1
      if (candidate.verdict === "eligible") summary.eligible += 1
      if (candidate.verdict === "held") summary.held += 1
      if (candidate.verdict === "rejected") summary.rejected += 1
      summaries.set(target.key, summary)
    }
  }
  const rank = { group: 0, conflicting: 1, unknown: 2 } as const
  return Array.from(summaries.values()).sort((left, right) => rank[left.kind] - rank[right.kind] || right.observations - left.observations || left.name.localeCompare(right.name))
}

/** Candidates per observed release group (the `release_group` fact only). */
export function summarizeReleaseGroups(candidates: ReadonlyArray<SummarizableCandidate>): ReleaseGroupSummary[] {
  return tally(candidates, "group", (candidate) => {
    const evidence = releaseGroupEvidence(candidate.normalized_facts)
    return evidence.kind === "group" ? { names: [evidence.name] } : { bucket: evidence.kind }
  })
}

/** Candidates per Usenet newsgroup (the `group` fact). Not release groups. */
export function summarizeUsenetGroups(candidates: ReadonlyArray<SummarizableCandidate>): ReleaseGroupSummary[] {
  return tally(candidates, "usenet", (candidate) => {
    const evidence = usenetGroupEvidence(candidate.normalized_facts)
    return evidence.kind === "groups" ? { names: evidence.names } : { bucket: evidence.kind }
  })
}
