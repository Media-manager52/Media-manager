import { describe, expect, it } from "vitest"

import type { APISubtitleVerification, APIWorkflow, APIWorkflowCandidate } from "@/api/client"
import { explainWorkflow, indexerLanguageNote, languageCheckLines } from "@/lib/workflow-explanation"

const workflow = (patch: Partial<APIWorkflow>) => ({ id: "w", media_id: "m", media_kind: "movie", title: "T", variant: "default", state: "waiting", revision: 1, adopted_replacement_confirmation_required: false, created_at: "", updated_at: "", ...patch }) as APIWorkflow
const candidate = (patch: Partial<APIWorkflowCandidate>) => ({ id: "c", title: "R", indexer_id: "i", indexer_name: "I", indexer_kind: "newznab", ...patch }) as APIWorkflowCandidate
const rejected = (reason: string) => candidate({ decision: { verdict: "rejected", decision_explanation: { reasons: [{ decision: "fail", rule_kind: "required", rule: reason, reason: "observed evidence does not satisfy the rule" }] } as never } })
const now = Date.parse("2026-11-09T12:00:00Z")

describe("explainWorkflow", () => {
  const usedUp = { state: "waiting", search_allowed: false, retry_discovery_state: "waiting" } as const
  const eligible = (id: string) => candidate({ id, decision: { verdict: "eligible" } as APIWorkflowCandidate["decision"] })

  it("counts eligible releases in correct Danish", () => {
    expect(explainWorkflow(workflow({}), [eligible("a")], now).why).toBe("1 egnet udgivelse venter på at blive prøvet.")
    expect(explainWorkflow(workflow({}), [eligible("a"), eligible("b")], now).why).toBe("2 egnede udgivelser venter på at blive prøvet.")
  })

  it("does not call a used-up round finished or blocked", () => {
    const later = new Date(now + 3_600_000).toISOString()
    const watching = explainWorkflow(workflow({ ...usedUp, next_rescan_at: later }), [], now)
    expect(watching.now).toContain("Den holder øje med nye udgivelser og kigger igen")
    expect(watching.now).not.toContain("spærret")
    expect(watching.you).toBe("Ikke lige nu. En ny runde kan starte af sig selv, når der findes en ny udgivelse, der passer til titlen. Ellers starter du den selv.")
    expect(explainWorkflow(workflow({ ...usedUp }), [], now).now).toBe("Alle forsøg er brugt. Den holder øje med, om en ny egnet udgivelse dukker op.")
  })

  it("offers known releases only when that action is really available", () => {
    const withAction = (available: boolean) => workflow({ ...usedUp, available_actions: [{ action: "open_round_from_known", available }] as APIWorkflow["available_actions"] })
    expect(explainWorkflow(withAction(true), [], now).you).toBe("Ikke nødvendigt. Du kan prøve en kendt udgivelse nu. Ellers venter den på en ny.")
    expect(explainWorkflow(withAction(false), [], now).you).toBe("Ikke lige nu. En ny runde kan starte af sig selv, når der findes en ny udgivelse, der passer til titlen. Ellers starter du den selv.")
  })

  it("still says searching is blocked when it is blocked for another reason", () => {
    expect(explainWorkflow(workflow({ search_allowed: false }), [], now).now).toBe("Den søger ikke lige nu, fordi søgning er spærret for denne opgave.")
  })

  it("never calls unconfirmed work finished", () => {
    expect(explainWorkflow(workflow({ state: "plex_scanning" }), []).why).toContain("ikke færdig")
    expect(explainWorkflow(workflow({ state: "plex_confirmed" }), []).why).toContain("bekræftet")
  })

  it("says plainly when nothing was found", () => {
    expect(explainWorkflow(workflow({}), [], now).why).toBe("Ingen indexer har fundet en udgivelse til den endnu.")
  })

  it("does not claim nothing was found when the list could not load", () => {
    expect(explainWorkflow(workflow({}), undefined, now).why).not.toContain("Ingen indexer")
  })

  it("names the most common reason and what was tried", () => {
    const result = explainWorkflow(workflow({}), [rejected("Dansk"), rejected("Dansk"), rejected("1080p"), candidate({ exclusion: { reason: "x", created_at: "" } })], now)
    expect(result.why).toContain("4 udgivelser")
    expect(result.why).toContain("Hyppigste grund: Dansk: opfylder ikke kravet.")
    expect(result.why).toContain("1 er prøvet og sorteret fra.")
  })

  it("asks the owner only when persisted facts say so", () => {
    expect(explainWorkflow(workflow({ attention_detail: "waiting for a new release" }), [], now).you).toContain("Nej")
    expect(explainWorkflow(workflow({ owner_action_required: true, attention_code: "x" }), [], now).you).toContain("Ja")
    expect(explainWorkflow(workflow({ new_release_ready: true }), [], now).you).toContain("Ja")
  })

  it("tells when it searches next and does not invent a plan", () => {
    expect(explainWorkflow(workflow({ next_rescan_at: "2026-11-09T15:00:00Z" }), [], now).now).toMatch(/Søger igen om 3 timer/)
    expect(explainWorkflow(workflow({ next_rescan_at: "2026-11-09T11:00:00Z" }), [], now).now).toContain("snart")
    expect(explainWorkflow(workflow({}), [], now).now).toBe("Den søger ikke selv videre.")
  })

  it("shows real download progress only", () => {
    expect(explainWorkflow(workflow({ state: "downloading", download_percent: 41.6 }), [], now).now).toContain("42 %")
    expect(explainWorkflow(workflow({ state: "downloading" }), [], now).now).not.toContain("%")
  })
})

describe("languageCheckLines", () => {
  const verification = (status: "verified" | "unverified", video: object, videos = 1) => ({ status, required_languages: ["da"], evidence: { videos: Array(videos).fill(video) } }) as unknown as APISubtitleVerification

  it("names the embedded track", () => {
    expect(languageCheckLines(verification("verified", { languages: [{ kind: "embedded", canonical: "da", track_index: 4 }] }))).toEqual(["Dansk tekst fundet i filens spor 4."])
  })

  it("prefers audio as the proof", () => {
    expect(languageCheckLines(verification("verified", { audio_languages: ["da"], languages: [] }))[0]).toBe("Dansk tale fundet i filen.")
  })

  it("explains a complete track with a forced flag", () => {
    expect(languageCheckLines(verification("verified", { languages: [{ kind: "embedded", canonical: "da", track_index: 2, forced: true, full_dialogue: true }] }))[0]).toContain("mærket forced, men indeholder en fuld tekst")
  })

  it("does not count a real forced track", () => {
    expect(languageCheckLines(verification("unverified", { languages: [{ kind: "embedded", canonical: "da", track_index: 2, forced: true, full_dialogue: false }] }))[0]).toContain("tæller ikke")
  })

  it("names a bundled subtitle and missing Danish", () => {
    expect(languageCheckLines(verification("verified", { languages: [{ kind: "external", canonical: "da" }] }))[0]).toContain("medfølgende tekstfil")
    expect(languageCheckLines(verification("unverified", { languages: [] }))[0]).toBe("Dansk blev ikke fundet i filen.")
  })

  it("says how many files were checked", () => {
    expect(languageCheckLines(verification("verified", { audio_languages: ["da"] }, 8))).toContain("Kontrolleret i 8 filer. Linjerne viser den første.")
  })
})

describe("indexerLanguageNote", () => {
  const candidate = (patch: object, facts: Array<{ fact: string; state: string; values: string[] }>) => ({ id: "c", title: "T", indexer_id: "i", indexer_name: "I", indexer_kind: "newznab", decision: { verdict: "eligible", decision_explanation: { version: 1, verdict: "eligible", score: 1, reasons: [], evidence_scope: "current_linked_observation", evidence: facts.map((fact) => ({ category: "language", ...fact })) } }, ...patch })
  it("says a claim is not yet checked", () => {
    expect(indexerLanguageNote([candidate({}, [{ fact: "subtitle_languages", state: "observed", values: ["da"] }])])).toBe("Dansk nævnt af indexeren, men endnu ikke kontrolleret.")
  })
  it("prefers the selected release and joins languages once", () => {
    const other = candidate({ id: "o" }, [{ fact: "audio_languages", state: "observed", values: ["sv"] }])
    const selected = candidate({ selected: true }, [{ fact: "audio_languages", state: "observed", values: ["da", "en"] }, { fact: "subtitle_languages", state: "inferred", values: ["DA"] }])
    expect(indexerLanguageNote([other, selected])).toBe("Dansk og engelsk nævnt af indexeren, men endnu ikke kontrolleret.")
  })
  it("says nothing without a real claim", () => {
    expect(indexerLanguageNote(undefined)).toBeUndefined()
    expect(indexerLanguageNote([candidate({}, [{ fact: "subtitle_languages", state: "unknown", values: ["da"] }])])).toBeUndefined()
    expect(indexerLanguageNote([candidate({}, [{ fact: "audio_languages", state: "observed", values: ["und"] }])])).toBeUndefined()
    expect(indexerLanguageNote([candidate({ exclusion: { reason: "x", created_at: "" } }, [{ fact: "subtitle_languages", state: "observed", values: ["da"] }])])).toBeUndefined()
  })
})
