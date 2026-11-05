import { describe, expect, it } from "vitest"

import type { APILibraryRoot } from "@/api/client"
import { checkText, setupStepDetail, suggestDestination, suggestSourceRoot } from "@/lib/setup"

const root = (plex: string, destination: string, source = "/mnt/altmount"): APILibraryRoot => ({
  id: plex, media_kind: "movie", name: plex, destination_root: destination, plex_visible_root: plex, source_visible_root: source, is_default: false, enabled: true,
})

describe("library folder suggestions", () => {
  it("maps the Plex path the way most existing roots do", () => {
    const roots = [root("/media/Film", "/srv/media/Film"), root("/media/Serier", "/srv/media/Serier"), root("/mnt/media-manager/libraries/movies", "/libraries/movies")]
    expect(suggestDestination(roots, "/media/Film 4K")).toBe("/srv/media/Film 4K")
  })

  it("maps a shared prefix", () => {
    expect(suggestDestination([root("/data/plex/Film", "/library/Film")], "/data/plex/Dokumentar")).toBe("/library/Dokumentar")
  })

  it("keeps the Plex path when nothing is known", () => {
    expect(suggestDestination([], "/media/Film")).toBe("/media/Film")
    expect(suggestDestination([root("/a/x", "/b/y")], "/media/Film")).toBe("/media/Film")
  })

  it("suggests the AltMount folder the existing roots use", () => {
    expect(suggestSourceRoot([])).toBe("/mnt/altmount")
    expect(suggestSourceRoot([root("/a", "/a", "/mnt/nzb"), root("/b", "/b", "/mnt/nzb"), root("/c", "/c")])).toBe("/mnt/nzb")
  })
})

describe("setup texts", () => {
  it("translates known connection checks and keeps unknown ones", () => {
    expect(checkText("Plex answered and shows 4 libraries")).toBe("Plex svarer og har 4 biblioteker.")
    expect(checkText("AltMount answered, but /mnt/altmount is not a folder Media Manager can see")).toContain("/mnt/altmount")
    expect(checkText("something new")).toBe("something new")
    expect(checkText("TMDB accepted the token; title search uses it")).toContain("TMDB godtog")
    expect(checkText("TMDB did not accept the token; use the long \"API Read Access Token\", not the short API key")).toContain("lange")
  })

  it("points a missing TMDB at Connections and names TMDB via Seerr as done", () => {
    expect(setupStepDetail({ id: "metadata", done: false, optional: false, detail: "Add TMDB under Connections for title search, posters and descriptions" })).toContain("Forbindelser")
    expect(setupStepDetail({ id: "metadata", done: true, optional: true, detail: "TMDB via Seerr" })).toBe("TMDB via Seerr")
  })

  it("shows the server's name for done steps and a Danish hint otherwise", () => {
    expect(setupStepDetail({ id: "plex", done: true, optional: false, detail: "Plex hjemme" })).toBe("Plex hjemme")
    expect(setupStepDetail({ id: "indexer", done: true, optional: false, detail: "3 commissioned" })).toBe("3 godkendt")
    expect(setupStepDetail({ id: "indexer", done: false, optional: false, detail: "Add an indexer and commission it" })).toContain("indexer")
    expect(setupStepDetail({ id: "seerr", done: false, optional: true, detail: "Seerr is saved; Seerr intake starts after the API restarts with it configured" })).toContain("genstartet")
  })
})
