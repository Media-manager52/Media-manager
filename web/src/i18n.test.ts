import { ApiError } from "@/api/client"
import { describe, expect, it } from "vitest"

import { actionLabel, activityActorLabel, activityLabel, attentionLabel, componentOutcomeLabel, errorMessage, humanize, queueEventLabel, repairReasonLabel, stateLabel, stateTone, translateDetail, workflowDisplayState } from "@/i18n"

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

describe("errorMessage", () => {
  it("shows common server errors in Danish and keeps the rest", () => {
    expect(errorMessage(new ApiError("Database is unavailable", 503, "database_unavailable"))).toBe("Databasen svarer ikke lige nu. Prøv igen om lidt.")
    expect(errorMessage(new ApiError("a very specific reason", 409, "some_new_code"))).toBe("a very specific reason")
    expect(errorMessage(new TypeError("Failed to fetch"))).toBe("Media Manager kunne ikke nås. Tjek forbindelsen, og prøv igen.")
    expect(errorMessage("plain")).toBe("plain")
  })

  it("translates the most common waiting reason", () => {
    expect(translateDetail("Releases were observed but none satisfied the required release evidence; a paced retry is scheduled.")).toContain("ingen opfylder profilens krav")
    expect(translateDetail("Four candidates failed NZB retrieval in this round; owner review is required. Submission slots were not consumed by these failures.")).toMatch(/^Fire udgivelser/)
  })
})

describe("the texts a full walk of the site found in English", () => {
  it("translates repair details and keeps the facts", () => {
    expect(translateDetail("replacement confirmed in Plex; the old file was retired")).toBe("Erstatningen er bekræftet i Plex, og den gamle fil er fjernet.")
    expect(translateDetail("replacement confirmed in Plex; the old symlink was removed")).toBe("Erstatningen er bekræftet i Plex, og den gamle fil er fjernet.")
    expect(translateDetail("the file has no da audio track; a dubbed replacement is being acquired")).toBe("Filen har ikke dansk tale. Media Manager henter en udgave med dansk tale.")
    expect(translateDetail("season 3 episode 7 could not be verified: Plex and TMDB disagree on the episode (numbering differs)")).toBe("Sæson 3 afsnit 7 kunne ikke bekræftes, fordi Plex og TMDB nummererer afsnittene forskelligt.")
    expect(translateDetail("Plex has no single item at /media/Serier/A/a.mkv")).toBe("Plex har ikke ét bestemt emne for filen: /media/Serier/A/a.mkv")
    expect(translateDetail("AltMount no longer has the published file (its Usenet articles went missing); a replacement is being acquired")).toContain("forsvundet hos Usenet")
  })

  it("labels activity, queue, indexer answers and repair reasons", () => {
    expect(attentionLabel("duplicate_package_skip_limit")).toBe("Samme pakke dukkede op for mange gange")
    expect(activityLabel("library.repair_opened")).toBe("Reparation startet")
    expect(activityLabel("workflow.queued")).toBe("Status: Henter")
    expect(activityLabel("workflow.created")).toBe("Arbejde oprettet")
    expect(activityLabel("future.thing_done")).toBe("Future thing done")
    expect(activityActorLabel("owner")).toBe("Dig")
    expect(activityActorLabel("robot_two")).toBe("Robot two")
    expect(queueEventLabel("release.retrieve_requested")).toBe("Hentning af NZB")
    expect(queueEventLabel("future.event")).toBe("Future event")
    expect(componentOutcomeLabel("no_results")).toBe("OK, intet fundet")
    expect(componentOutcomeLabel("success")).toBe("OK")
    expect(repairReasonLabel("missing_required_audio")).toBe("Mangler dansk tale")
    expect(repairReasonLabel("something_new")).toBe("Something new")
  })
})
