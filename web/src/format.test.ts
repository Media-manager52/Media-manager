import { describe, expect, it } from "vitest"

import { formatBytes, formatRelative, pluralize } from "@/format"

describe("format", () => {
  const now = Date.parse("2026-11-03T12:00:00Z")
  it("formats relative times in Danish", () => {
    expect(formatRelative("2026-11-03T11:59:50Z", now)).toBe("lige nu")
    expect(formatRelative("2026-11-03T15:00:00Z", now)).toBe("om 3 timer")
    expect(formatRelative("2026-11-01T12:00:00Z", now)).toBe("i forgårs")
    expect(formatRelative(undefined, now)).toBe("—")
    expect(formatRelative("not a date", now)).toBe("—")
  })

  it("formats sizes and counts", () => {
    expect(formatBytes(0)).toBe("—")
    expect(formatBytes(1536)).toBe("1,5 KB")
    expect(formatBytes(3.4 * 1024 ** 3)).toBe("3,4 GB")
    expect(pluralize(1, "sag", "sager")).toBe("1 sag")
    expect(pluralize(1200, "sag", "sager")).toBe("1.200 sager")
  })
})
