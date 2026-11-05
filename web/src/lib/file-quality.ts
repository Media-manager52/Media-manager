import type { APIManagedFile } from "@/api/client"
import { languageName } from "@/i18n"

export type FileQuality = { resolution?: string; video?: string; audio: string[]; subtitles: string[] }

const codecNames: Record<string, string> = { h264: "H.264", avc: "H.264", hevc: "H.265", h265: "H.265", av1: "AV1", mpeg2video: "MPEG-2", vc1: "VC-1", vp9: "VP9" }

function names(codes: unknown): string[] {
  if (!Array.isArray(codes)) return []
  const seen = new Set<string>()
  for (const code of codes) {
    if (typeof code !== "string" || !code.trim()) continue
    seen.add(languageName(code))
  }
  return [...seen]
}

/**
 * What a library file holds, read from what Media Manager measured in the file
 * itself (or, before that, from the release name). Unknown parts are left out.
 */
export function fileQuality(file: Pick<APIManagedFile, "quality_evidence">): FileQuality {
  const facts = (file.quality_evidence?.facts ?? {}) as Record<string, unknown>
  const resolution = typeof facts.resolution === "string" && facts.resolution ? (facts.resolution === "2160p" ? "4K" : facts.resolution) : undefined
  const codec = typeof facts.video_codec === "string" ? facts.video_codec.toLowerCase() : ""
  return { resolution, video: codec ? codecNames[codec] ?? codec.toUpperCase() : undefined, audio: names(facts.audio_languages), subtitles: names(facts.subtitle_languages) }
}
