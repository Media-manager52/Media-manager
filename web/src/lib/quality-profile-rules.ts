/**
 * Pure quality-profile rule logic for the profile editor.
 *
 * A profile's `policy.rules` mixes two different things that must never be
 * confused:
 *
 *  - required rules are hard gates (a resolution or audio codec the release must
 *    match). Only the owner removes or changes them, explicitly.
 *  - preferred rules are soft scores (the "Best available" ladders, the preset
 *    ladders and a preferred source).
 *
 * They coexist: a Balanced preset can sit on top of a required 1080p gate. The
 * editor therefore models the requirement and the preference as independent
 * choices, derives both from the stored rules, and an unchanged choice never
 * touches the stored rules. The merge below only edits what the owner changed,
 * so an unrelated save cannot drop a gate, narrow it, or discard a preset's
 * preference ladder.
 */

export type RuleRecord = Record<string, unknown>

export type ResolutionRequirement = "none" | "2160p" | "1080p" | "720p" | "custom"
export type AudioRequirement = "none" | "custom"
// "none" keeps whatever preferences are stored; there is no "remove" because an
// owner replaces a ladder (Best available) rather than deleting it.
export type PreferenceChoice = "best_available" | "none"

export type QualitySelections = {
  resolutionRequirement: ResolutionRequirement
  resolutionPreference: PreferenceChoice
  audioRequirement: AudioRequirement
  audioPreference: PreferenceChoice
  // "none" keeps the stored source preferences; otherwise a source value.
  source: string
}

const standardResolutions = ["2160p", "1080p", "720p"] as const

export const bestAvailableResolutionRules = [
  { label: "2160p", value: "2160p", weight: 50000 },
  { label: "1080p", value: "1080p", weight: 40000 },
  { label: "720p", value: "720p", weight: 30000 },
  { label: "576p", value: "576p", weight: 20000 },
  { label: "480p", value: "480p", weight: 10000 },
]

export const bestAvailableAudioRules = [
  { label: "TrueHD Atmos", value: "truehd_atmos", weight: 5300 },
  { label: "DTS:X", value: "dts_x", weight: 5100 },
  { label: "TrueHD", value: "truehd", weight: 5000 },
  { label: "DTS-HD MA", value: "dts_hd_ma", weight: 4800 },
  { label: "DTS-HD", value: "dts_hd", weight: 4700 },
  { label: "FLAC", value: "flac", weight: 4600 },
  { label: "LPCM", value: "lpcm", weight: 4400 },
  { label: "E-AC-3 Atmos", value: "eac3_atmos", weight: 3300 },
  { label: "E-AC-3", value: "eac3", weight: 3000 },
  { label: "DTS", value: "dts", weight: 2800 },
  { label: "AC-3", value: "ac3", weight: 2000 },
  { label: "Opus", value: "opus", weight: 1800 },
  { label: "AAC", value: "aac", weight: 1500 },
  { label: "MP3", value: "mp3", weight: 1000 },
]

type Ladder = typeof bestAvailableResolutionRules
type QualityFact = "resolution" | "audio_codec"

// The Go evaluator lower-cases and trims fact names, kinds and values; the
// editor must classify rules the same way or it would mistake a gate for a
// preference.
function normalized(value: unknown) { return typeof value === "string" ? value.trim().toLowerCase() : "" }
function isFact(rule: RuleRecord, fact: string) { return normalized(rule.fact) === fact }
function isKind(rule: RuleRecord, kind: "required" | "preferred") { return normalized(rule.kind) === kind }
function allowedValues(rule: RuleRecord) { return Array.isArray(rule.allowed_values) ? rule.allowed_values.map(normalized).filter(Boolean) : [] }
function requiredRules(rules: RuleRecord[], fact: QualityFact) { return rules.filter((rule) => isFact(rule, fact) && isKind(rule, "required")) }
function preferredRules(rules: RuleRecord[], fact: string) { return rules.filter((rule) => isFact(rule, fact) && isKind(rule, "preferred")) }

function ladderRules(fact: QualityFact, values: Ladder): RuleRecord[] {
  const prefix = fact === "resolution" ? "Best available resolution" : "Best available audio"
  return values.map((item) => ({ name: `${prefix}: ${item.label}`, fact, allowed_values: [item.value], kind: "preferred", weight: item.weight, unknown: "allow" }))
}

function hasLadder(rules: RuleRecord[], fact: QualityFact, values: Ladder) {
  const preferred = preferredRules(rules, fact)
  return values.every((item) => preferred.some((rule) => rule.weight === item.weight && allowedValues(rule).length === 1 && allowedValues(rule)[0] === item.value))
}

export function resolutionRequirement(rules: RuleRecord[]): ResolutionRequirement {
  const required = requiredRules(rules, "resolution")
  if (required.length === 0) return "none"
  const values = required.length === 1 ? allowedValues(required[0]) : []
  const only = values.length === 1 ? values[0] : ""
  return (standardResolutions as readonly string[]).includes(only) ? only as ResolutionRequirement : "custom"
}

export function audioRequirement(rules: RuleRecord[]): AudioRequirement {
  return requiredRules(rules, "audio_codec").length ? "custom" : "none"
}

export function resolutionPreference(rules: RuleRecord[]): PreferenceChoice {
  return hasLadder(rules, "resolution", bestAvailableResolutionRules) ? "best_available" : "none"
}

export function audioPreference(rules: RuleRecord[]): PreferenceChoice {
  return hasLadder(rules, "audio_codec", bestAvailableAudioRules) ? "best_available" : "none"
}

// A single preferred source rule with one value is the editor's own shape and can
// be edited in place. A ladder (a preset) or a multi-value rule is richer than the
// select can show, so the editor reports "keep" and leaves it alone.
function editableSourceRule(rules: RuleRecord[]) {
  const preferred = preferredRules(rules, "source")
  return preferred.length === 1 && allowedValues(preferred[0]).length === 1 ? preferred[0] : undefined
}

export function sourceSelection(rules: RuleRecord[]) {
  const rule = editableSourceRule(rules)
  return rule ? allowedValues(rule)[0] : "none"
}

/** Selections that describe the stored rules exactly: saving them changes nothing. */
export function deriveSelections(rules: RuleRecord[]): QualitySelections {
  return {
    resolutionRequirement: resolutionRequirement(rules),
    resolutionPreference: resolutionPreference(rules),
    audioRequirement: audioRequirement(rules),
    audioPreference: audioPreference(rules),
    source: sourceSelection(rules),
  }
}

/** Selections for a brand-new profile. */
export function defaultSelections(): QualitySelections {
  return { resolutionRequirement: "none", resolutionPreference: "best_available", audioRequirement: "none", audioPreference: "best_available", source: "webdl" }
}

/** Display text for the values of the required rules the editor cannot name. */
export function requiredValues(rules: RuleRecord[], fact: QualityFact) {
  const seen: string[] = []
  for (const rule of requiredRules(rules, fact)) for (const value of allowedValues(rule)) if (!seen.includes(value)) seen.push(value)
  return seen
}

export function resolutionLabel(rules: RuleRecord[]) {
  const requirement = resolutionRequirement(rules)
  const required = requirement === "none" ? "" : `Require ${requiredValues(rules, "resolution").join(" / ") || "custom"}`
  if (resolutionPreference(rules) === "best_available") return required ? `${required}, prefer best available` : "Best available"
  if (required) return required
  return preferredRules(rules, "resolution").length ? "Custom preferences" : "None"
}

export function audioLabel(rules: RuleRecord[]) {
  const required = audioRequirement(rules) === "none" ? "" : `Require ${requiredValues(rules, "audio_codec").join(" / ") || "custom"}`
  if (audioPreference(rules) === "best_available") return required ? `${required}, prefer best available` : "Best available"
  if (required) return required
  return preferredRules(rules, "audio_codec").length ? "Custom preferences" : "None"
}

function withoutRequired(rules: RuleRecord[], fact: string) { return rules.filter((rule) => !(isFact(rule, fact) && isKind(rule, "required"))) }
function withoutPreferred(rules: RuleRecord[], fact: string) { return rules.filter((rule) => !(isFact(rule, fact) && isKind(rule, "preferred"))) }

const resolutionRequirements: readonly string[] = ["none", "2160p", "1080p", "720p", "custom"]
const audioRequirements: readonly string[] = ["none", "custom"]

/**
 * A selection that is not one of the known choices (an empty control value, a
 * stale string) must never write a rule. Requirements fall back to "keep what is
 * stored" and preferences and the source to "leave as is".
 */
function sanitizeSelections(selections: QualitySelections): QualitySelections {
  return {
    resolutionRequirement: resolutionRequirements.includes(selections.resolutionRequirement) ? selections.resolutionRequirement : "custom",
    resolutionPreference: selections.resolutionPreference === "best_available" ? "best_available" : "none",
    audioRequirement: audioRequirements.includes(selections.audioRequirement) ? selections.audioRequirement : "custom",
    audioPreference: selections.audioPreference === "best_available" ? "best_available" : "none",
    source: typeof selections.source === "string" && selections.source.trim() ? selections.source.trim() : "none",
  }
}

/**
 * Apply the owner's selections to the stored rules.
 *
 * Every selection is compared with what the stored rules already say; an equal
 * selection leaves those rules byte-for-byte alone (names, weights, unknown
 * policies, ordering). Only a differing selection edits its own kind of rule for
 * its own fact. Unrelated facts and the other kind of rule are never touched.
 */
export function mergeQualityRules(existing: RuleRecord[], requested: QualitySelections): RuleRecord[] {
  let rules = existing.map((rule) => ({ ...rule }))
  const current = deriveSelections(existing)
  const selections = sanitizeSelections(requested)

  // "custom" means "keep the requirement that is stored", so it never edits.
  if (selections.resolutionRequirement !== "custom" && selections.resolutionRequirement !== current.resolutionRequirement) {
    rules = withoutRequired(rules, "resolution")
    if (selections.resolutionRequirement !== "none") rules.push({ name: "Required resolution", fact: "resolution", kind: "required", allowed_values: [selections.resolutionRequirement], weight: 0, unknown: "hold" })
  }
  if (selections.audioRequirement === "none" && current.audioRequirement !== "none") rules = withoutRequired(rules, "audio_codec")

  if (selections.resolutionPreference === "best_available" && current.resolutionPreference !== "best_available") {
    rules = [...withoutPreferred(rules, "resolution"), ...ladderRules("resolution", bestAvailableResolutionRules)]
  }
  if (selections.audioPreference === "best_available" && current.audioPreference !== "best_available") {
    rules = [...withoutPreferred(rules, "audio_codec"), ...ladderRules("audio_codec", bestAvailableAudioRules)]
  }

  if (selections.source !== "none" && selections.source !== current.source) {
    const editable = editableSourceRule(rules)
    if (editable) {
      // The rule is a fresh copy made above, so it can be edited in place.
      editable.allowed_values = [selections.source]
    } else {
      // A preset ladder or a multi-value preference is replaced as a whole by an
      // explicit choice; a lone changed rung would score the same source twice.
      rules = [...withoutPreferred(rules, "source"), { name: "Preferred source", fact: "source", kind: "preferred", allowed_values: [selections.source], weight: 100, unknown: "allow" }]
    }
  }
  return rules
}
