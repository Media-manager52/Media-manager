import { describe, expect, it } from "vitest"

import { altMountBuildText, altMountSwitchErrorText } from "@/lib/altmount-switch"

describe("altMountBuildText", () => {
  it("names the build and its version", () => {
    expect(altMountBuildText({ build: "standard", version: "v0.4.0" })).toBe("Den normale AltMount (v0.4.0)")
    expect(altMountBuildText({ build: "media_manager", version: "v0.4.0-mm.3" })).toBe("Media Managers udgave (v0.4.0-mm.3)")
    expect(altMountBuildText({ build: "unknown" })).toBe("Ukendt udgave")
  })
})

describe("altMountSwitchErrorText", () => {
  it("explains a rollback and a newer AltMount in Danish", () => {
    expect(altMountSwitchErrorText("altmount_switch_rolled_back", "x")).toContain("gamle AltMount er sat tilbage")
    expect(altMountSwitchErrorText("altmount_version_newer", "x")).toContain("nyere")
    expect(altMountSwitchErrorText(undefined, "raw")).toBe("raw")
  })
})
