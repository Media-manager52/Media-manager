import type { APIDecisionReason } from "@/api/client"
import { languageName } from "@/i18n"

const reasonTexts: Record<string, string> = {
  "observed evidence does not satisfy the rule": "opfylder ikke kravet",
  "the provider supplied no conclusive evidence": "ukendt, udgivelsen oplyser det ikke",
}

const ruleNames: Array<[RegExp, (language: string) => string]> = [
  [/^Required subtitle or audio ([a-z]{2,3})$/, (language) => `${language} tale eller undertekst`],
  [/^Required audio ([a-z]{2,3})$/, (language) => `${language} tale`],
  [/^Danish record of the release group$/, () => "Udgivergruppen"],
]

/** Server reasons that carry numbers. */
const reasonPatterns: Array<[RegExp, (match: RegExpMatchArray) => string]> = [
  [/^release group (\S+) had the required Danish in (\d+) of (\d+) measured downloads$/,
    (match) => `${match[1]} havde dansk i kun ${match[2]} af ${match[3]} downloads, så den prøves ikke`],
]

function reasonText(reason: string): string {
  for (const [pattern, render] of reasonPatterns) {
    const match = reason.match(pattern)
    if (match) return render(match)
  }
  return reasonTexts[reason] ?? reason
}

/** The server names its generated language rules in English; a saved profile's own rule keeps its name. */
function ruleName(rule: string): string {
  for (const [pattern, render] of ruleNames) {
    const match = rule.match(pattern)
    if (match) return render(languageName(match[1]))
  }
  return rule
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
      const text = reasonText(reason.reason)
      return reason.rule ? `${ruleName(reason.rule)}: ${text}` : text
    })
    .join(" · ")
}
