import { describe, expect, it } from "vitest"

import { actionLabel, attentionLabel, humanize, stateLabel, stateTone, translateDetail, workflowDisplayState } from "@/i18n"

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

  it("translates the most common attention explanations and keeps file names and counts", () => {
    expect(translateDetail("8 failed outputs are retained (limit 8); owner storage review is required before another NZB request."))
      .toBe("8 fejlede downloads er gemt (grænse 8). Ryd op i lageret, før der hentes igen.")
    expect(translateDetail("bounded media-info inspection failed for Show.S01E02.mkv: media-info probe failed"))
      .toBe("Filen kunne ikke undersøges (media-info fejlede): Show.S01E02.mkv")
    expect(translateDetail("The next eligible release resolved to an exact package already submitted in this workflow, and no unexcluded eligible candidate remains."))
      .toMatch(/^Den næste egnede udgivelse/)
    expect(translateDetail("AltMount reported completion, but its read-only output was still unavailable after five minutes. Verify the shared-folder mount before retrying."))
      .toMatch(/^AltMount meldte færdig/)
  })

  it("shows unknown server text unchanged, so no detail is lost", () => {
    expect(translateDetail("bounded media-info inspection failed for x.mkv")).toBe("bounded media-info inspection failed for x.mkv")
    expect(translateDetail(undefined)).toBe("")
  })
})

describe("workflowDisplayState", () => {
  it("shows a season whose every episode is in Plex as in Plex", () => {
    expect(workflowDisplayState({ state: "waiting", covered_by_episodes: true })).toBe("plex_confirmed")
    expect(workflowDisplayState({ state: "waiting" })).toBe("waiting")
    expect(workflowDisplayState({ state: "needs_attention", covered_by_episodes: true })).toBe("needs_attention")
  })
})

describe("release exclusion reasons", () => {
  it("are shown in Danish", () => {
    expect(translateDetail("exact NZB package was already submitted in this workflow")).toBe("Præcis samme pakke er allerede prøvet i dette arbejde.")
    expect(translateDetail("selected release NZB access was rejected after one bounded request (newznab_http_403)")).toBe("Indexeren afviste adgang til NZB-filen (newznab_http_403).")
  })

  it("name the real season-pack problem", () => {
    expect(translateDetail("season pack is incomplete; 2 expected episodes are missing")).toBe("Sæsonpakken er ufuldstændig; 2 afsnit mangler.")
    expect(translateDetail("season pack is needs_review; first problem: duplicate (S01E02) and 1 more")).toBe("Sæsonpakken skal tjekkes; første problem: et afsnit findes mere end én gang (S01E02) og 1 til.")
    // Older rows said "0 expected episodes are missing" for a pack held for review.
    expect(translateDetail("season pack is needs_review; 0 expected episodes are missing")).toBe("Sæsonpakken skal tjekkes.")
  })
})
