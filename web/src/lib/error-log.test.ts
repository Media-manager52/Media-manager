import { describe, expect, it } from "vitest"

import type { APIErrorLogEntry } from "@/api/client"
import { errorEntryText, errorExplanation, errorLogText } from "@/lib/error-log"

const entry: APIErrorLogEntry = {
  id: "1", area: "language", code: "subtitle_requirements_unverified", title: "Testfilmen", subject: "Testfilmen.2024.1080p.WEB",
  detail: "Testfilmen.mkv has no verified da subtitle", count: 1, open: false, occurred_at: "2026-11-03T12:00:00Z",
}

describe("errorExplanation", () => {
  it("explains a known failure in plain Danish", () => {
    expect(errorExplanation(entry).what).toBe("Filen havde ikke det sprog, profilen kræver.")
  })

  it("names the status code of an indexer server error", () => {
    expect(errorExplanation({ code: "newznab_http_502", area: "search" }).what).toContain("502")
  })

  it("keeps a readable text for a code it does not know", () => {
    expect(errorExplanation({ code: "new_kind_of_failure", area: "download" }).what).toBe("New kind of failure")
  })
})

describe("errorLogText", () => {
  it("writes everything needed to report one failure", () => {
    const text = errorEntryText(entry, "Film")
    for (const part of ["Sprog", "Filen havde ikke", "Titel: Testfilmen · Film", "Udgivelse eller kilde: Testfilmen.2024.1080p.WEB", "Det sker nu:", "Kode: subtitle_requirements_unverified", "Detalje:"]) {
      expect(text).toContain(part)
    }
  })

  it("marks open cases and repeated failures", () => {
    const text = errorEntryText({ ...entry, open: true, count: 3 })
    expect(text).toContain("Kræver dig")
    expect(text).toContain("3 gange")
  })

  it("says when the list is only the newest part", () => {
    expect(errorLogText([entry], 40)).toContain("de 1 nyeste af 40")
    expect(errorLogText([entry], 1)).toContain("1 fejl")
  })
})
