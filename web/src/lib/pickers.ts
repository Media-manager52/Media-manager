import type { APILibraryRoot, APIQualityProfile } from "@/api/client"
import { languageName } from "@/i18n"
import { resolutionRequirement, type RuleRecord } from "@/lib/quality-profile-rules"

export type PickerOption = {
  value: string
  label: string
  description?: string
  /** A shorter line for the closed field; the description is used otherwise. */
  summary?: string
  badges?: string[]
  group?: string
}

const kindNoun = { movie: "film", series: "serier" } as const

function languageList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === "string" && entry.trim() !== "") : []
}

function names(codes: string[], plural = false): string {
  // Danish adjectives take -e before a plural noun: "danske undertekster".
  return codes.map((code) => {
    const name = languageName(code).toLowerCase()
    return plural && name.endsWith("k") ? `${name}e` : name
  }).join(" eller ")
}

/** One Danish line saying what a quality profile demands. */
export function profileSummary(profile: APIQualityProfile): string {
  const policy = profile.policy ?? {}
  const parts: string[] = []
  const audio = languageList(policy.required_audio_languages)
  const subtitles = languageList(policy.required_subtitle_languages)
  if (audio.length) parts.push(`Kræver ${names(audio)} tale`)
  else if (subtitles.length) parts.push(`Kræver ${names(subtitles, true)} undertekster eller tale`)
  else parts.push("Intet sprogkrav")
  const rules = Array.isArray(policy.rules) ? (policy.rules as RuleRecord[]) : []
  const resolution = resolutionRequirement(rules)
  if (resolution === "2160p" || resolution === "1080p" || resolution === "720p") parts.push(`kræver ${resolution}`)
  else if (resolution === "custom") parts.push("egne kvalitetskrav")
  else parts.push("bedste kvalitet")
  const routing = languageList(policy.original_language_routing)
  if (routing.length) parts.push(`vælges selv til titler på ${names(routing)}`)
  return parts.join(" · ")
}

/**
 * Library choices for one media kind. The automatic choice comes first and
 * names the folder it currently means; 4K folders get their own group because
 * they hold an extra edition.
 */
export function rootOptions(roots: APILibraryRoot[], kind: "movie" | "series", automatic?: { label: string; description: string }): PickerOption[] {
  const fourK = (root: APILibraryRoot) => Number(root.edition === "4k")
  const usable = roots.filter((root) => root.media_kind === kind && root.enabled).sort((a, b) => fourK(a) - fourK(b) || Number(b.is_default) - Number(a.is_default) || a.name.localeCompare(b.name, "da"))
  const options: PickerOption[] = []
  if (automatic) {
    const fallback = usable.find((root) => root.is_default && root.edition !== "4k")
    options.push({ value: "", label: automatic.label, description: fallback ? `${automatic.description} Lige nu: ${fallback.name}.` : automatic.description, summary: fallback ? `Lige nu: ${fallback.name}` : undefined })
  }
  for (const root of usable) {
    const isFourK = root.edition === "4k"
    options.push({
      value: root.id,
      label: root.name,
      description: isFourK ? `Ekstra 4K-udgave af ${kindNoun[kind]} · ${root.plex_visible_root}` : root.plex_visible_root,
      badges: [root.is_default ? "Standard" : "", isFourK ? "4K" : ""].filter(Boolean),
      group: isFourK ? "4K-udgaver" : "Biblioteker",
    })
  }
  return options
}

/** Quality profile choices for one media kind, the automatic one first. */
export function profileOptions(profiles: APIQualityProfile[], kind: "movie" | "series", automatic?: { label: string; description: string }): PickerOption[] {
  const usable = profiles.filter((profile) => profile.media_kind === kind && profile.enabled).sort((a, b) => Number(b.is_default) - Number(a.is_default) || a.name.localeCompare(b.name, "da"))
  const options: PickerOption[] = []
  if (automatic) {
    const fallback = usable.find((profile) => profile.is_default)
    options.push({ value: "", label: automatic.label, description: fallback ? `${automatic.description} Lige nu: ${fallback.name}.` : automatic.description, summary: fallback ? `Lige nu: ${fallback.name}` : undefined })
  }
  for (const profile of usable) {
    options.push({ value: profile.id, label: profile.name, description: profileSummary(profile), badges: profile.is_default ? ["Standard"] : [], group: automatic ? "Profiler" : undefined })
  }
  return options
}
