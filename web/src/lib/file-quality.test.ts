import { describe, expect, it } from "vitest"

import { fileQuality } from "@/lib/file-quality"

describe("fileQuality", () => {
  it("reads resolution, codec and languages in Danish", () => {
    expect(fileQuality({ quality_evidence: { facts: { resolution: "2160p", video_codec: "hevc", audio_languages: ["da", "eng"], subtitle_languages: ["da", "fin", "nor", "swe", "da"] } } })).toEqual({
      resolution: "4K", video: "H.265", audio: ["Dansk", "Engelsk"], subtitles: ["Dansk", "Finsk", "Norsk", "Svensk"],
    })
  })
  it("leaves out what is unknown", () => {
    expect(fileQuality({ quality_evidence: {} })).toEqual({ resolution: undefined, video: undefined, audio: [], subtitles: [] })
  })
})
