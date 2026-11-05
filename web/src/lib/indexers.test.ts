import { describe, expect, it } from "vitest"

import { indexerPresetFor, indexerPresets } from "@/lib/indexers"

describe("indexer presets", () => {
  it("are Newznab API addresses over https with unique names", () => {
    expect(new Set(indexerPresets.map((preset) => preset.name)).size).toBe(indexerPresets.length)
    for (const preset of indexerPresets) expect(preset.url).toMatch(/^https:\/\/[a-z0-9.-]+\/api$/)
  })
  it("recognise their own address", () => {
    expect(indexerPresetFor("https://api.nzbgeek.info/api/")).toBe("NZBgeek")
    expect(indexerPresetFor("https://indexer.example/api")).toBe("")
  })
})
