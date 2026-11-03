import { describe, expect, it } from "vitest"

import { actionLabel, attentionLabel, humanize, stateLabel, stateTone, translateDetail } from "@/i18n"

describe("labels", () => {
  it("translates known workflow states and keeps unknown codes readable", () => {
    expect(stateLabel("plex_confirmed")).toBe("I Plex")
    expect(stateLabel("needs_attention")).toBe("Kræver handling")
    expect(stateLabel("brand_new_state")).toBe("Brand new state")
    expect(stateLabel(undefined)).toBe("Ukendt")
  })

  it("never shows a confirmed state as anything but good", () => {
    expect(stateTone("plex_confirmed")).toBe("good")
    expect(stateTone("needs_attention")).toBe("warn")
    expect(stateTone("waiting")).toBe("neutral")
  })

  it("names attention codes and actions in Danish", () => {
    expect(attentionLabel("duplicate_package_no_alternate")).toContain("intet alternativ")
    expect(attentionLabel("")).toBe("")
    expect(actionLabel("reconcile_altmount").label).toBe("Tjek AltMount igen")
    expect(actionLabel("future_action").label).toBe("Future action")
    expect(humanize("a_b")).toBe("A b")
  })
})

describe("translateDetail", () => {
  it("translates the common waiting explanations and keeps numbers", () => {
    expect(translateDetail("All 4 acquisition slots in this round were used. Scheduled discovery will wait for a newly observed eligible release; opening another round requires the owner."))
      .toBe("Alle 4 forsøg i runden er brugt. Media Manager holder øje med nye udgivelser; en ny runde kræver dig.")
    expect(translateDetail("no eligible release yet")).toBe("Ingen egnet udgivelse fundet endnu.")
  })

  it("shows unknown server text unchanged, so no detail is lost", () => {
    expect(translateDetail("bounded media-info inspection failed for x.mkv")).toBe("bounded media-info inspection failed for x.mkv")
    expect(translateDetail(undefined)).toBe("")
  })
})
