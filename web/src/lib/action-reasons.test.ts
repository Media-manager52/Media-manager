import { describe, expect, it } from "vitest"

import { actionReasonText } from "./action-reasons"

describe("actionReasonText", () => {
  it("translates the server's action reasons", () => {
    expect(actionReasonText("An AltMount job is active; wait for it to settle.")).toBe("AltMount arbejder stadig på et job. Vent, til det er færdigt.")
    expect(actionReasonText("An AltMount job is still active.")).toBe("AltMount arbejder stadig på et job. Vent, til det er færdigt.")
    expect(actionReasonText("The retained failed-output limit (3) is reached; the owner must review storage first.")).toContain("Grænsen på 3 gemte")
    expect(actionReasonText("Retries Film.2025.1080p from NZBgeek after the provider access problem was fixed.")).toBe("Prøver Film.2025.1080p fra NZBgeek igen, nu hvor adgangsproblemet hos indexeren er løst.")
  })

  it("drops a note that only repeats the button help", () => {
    expect(actionReasonText("Evidence-only rescan: finds new releases, never grabs.")).toBe("")
  })

  it("keeps unknown text as written", () => {
    expect(actionReasonText("Something new from the server.")).toBe("Something new from the server.")
    expect(actionReasonText(undefined)).toBe("")
  })
})
