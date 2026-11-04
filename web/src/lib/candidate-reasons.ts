import type { APIDecisionReason } from "@/api/client"

const reasonTexts: Record<string, string> = {
  "observed evidence does not satisfy the rule": "opfylder ikke kravet",
  "the provider supplied no conclusive evidence": "ukendt, udgivelsen oplyser det ikke",
}

/**
 * The reasons that kept a release back, for one line under the release.
 *
 * A preferred rule only ranks releases (a 2160p release "fails" the 1080p
 * preference), so it is never shown as a problem. Only a failed required rule,
 * or an unknown one on a held release, explains why a release was not used.
 */
export function candidateReasonText(verdict: string | undefined, reasons: APIDecisionReason[], limit = 2): string {
  const blocking = reasons.filter((reason) => reason.rule_kind !== "preferred" && (reason.decision === "fail" || (verdict === "held" && reason.decision === "unknown")))
  return blocking
    .slice(0, limit)
    .map((reason) => {
      const text = reasonTexts[reason.reason] ?? reason.reason
      return reason.rule ? `${reason.rule}: ${text}` : text
    })
    .join(" · ")
}
