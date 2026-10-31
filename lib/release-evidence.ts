/**
 * Fallback rows for the Evidence explorer, used only when the server attached
 * no structured `decision_explanation.evidence` to a candidate.
 *
 * The Go store writes `normalized_facts` as a JSON array of facts
 * (`[{name, values, state, ...}]`). Older stored rows and fixtures use a plain
 * `{fact: value}` object, which carries no per-fact state. The helper reads both
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
