import { describe, expect, it } from "vitest"

import { describeVariant, scopeLabel } from "@/lib/links"

describe("scopeLabel", () => {
  it("names a season or an episode", () => {
    expect(scopeLabel("season:02")).toBe("Sæson 2")
    expect(scopeLabel("season:07:episode:07")).toBe("S07E07")
  })

  it("is empty for films and whole titles", () => {
    expect(scopeLabel("default")).toBe("")
    expect(scopeLabel(undefined)).toBe("")
  })
})

describe("describeVariant", () => {
  it("keeps the wording used on work rows", () => {
    expect(describeVariant({ variant: "default", media_kind: "movie", edition: "standard" })).toBe("Film")
    expect(describeVariant({ variant: "default", media_kind: "series", edition: "standard" })).toBe("Hele serien")
    expect(describeVariant({ variant: "season:01", media_kind: "series", edition: "4k" })).toBe("Sæson 1 · 4K")
  })
})
