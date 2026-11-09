import { describe, expect, it } from "vitest"

import type { APIDecisionReason } from "@/api/client"
import { candidateReasonText } from "@/lib/candidate-reasons"

const reason = (rule: string, decision: string, ruleKind: "required" | "preferred", text = "observed evidence does not satisfy the rule"): APIDecisionReason => ({ rule, decision, rule_kind: ruleKind, reason: text })

describe("candidate reasons", () => {
  it("never shows preferred rules a chosen release did not match", () => {
    const reasons = [reason("Best available resolution: 1080p", "fail", "preferred"), reason("Best available audio: TrueHD", "fail", "preferred"), reason("Danish subtitles", "pass", "required")]
    expect(candidateReasonText("eligible", reasons)).toBe("")
  })

  it("names the required rule that failed, in Danish", () => {
    const reasons = [reason("Best available resolution: 1080p", "fail", "preferred"), reason("Danish subtitles", "fail", "required")]
    expect(candidateReasonText("rejected", reasons)).toBe("Danish subtitles: opfylder ikke kravet")
  })

  it("shows unknown required facts only for a held release", () => {
    const reasons = [reason("Requested episode", "unknown", "required", "the provider supplied no conclusive evidence")]
    expect(candidateReasonText("held", reasons)).toBe("Requested episode: ukendt, udgivelsen oplyser det ikke")
    expect(candidateReasonText("eligible", reasons)).toBe("")
  })

  it("keeps identity reasons without a rule kind and other server texts", () => {
    const reasons: APIDecisionReason[] = [{ rule: "requested title", decision: "fail", reason: "release title does not begin with a saved canonical, original or alias title" }]
    expect(candidateReasonText("rejected", reasons)).toBe("requested title: release title does not begin with a saved canonical, original or alias title")
  })
})

describe("candidateReasonText rule names", () => {
  const failed = (rule: string) => ({ rule, rule_kind: "required" as const, decision: "fail" as const, reason: "observed evidence does not satisfy the rule" })

  it("names the generated language rules in Danish", () => {
    expect(candidateReasonText("rejected", [failed("Required subtitle or audio da")])).toBe("Dansk tale eller undertekst: opfylder ikke kravet")
    expect(candidateReasonText("rejected", [failed("Required audio da")])).toBe("Dansk tale: opfylder ikke kravet")
  })

  it("keeps the name of a rule the owner wrote", () => {
    expect(candidateReasonText("rejected", [failed("Danske undertekster")])).toBe("Danske undertekster: opfylder ikke kravet")
  })
})
