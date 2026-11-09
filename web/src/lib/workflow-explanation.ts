import type { APISubtitleVerification, APIWorkflow, APIWorkflowCandidate } from "@/api/client"
import { formatRelative } from "@/format"
import { attentionLabel, languageName, translateDetail } from "@/i18n"
import { candidateReasonText } from "@/lib/candidate-reasons"

export type WorkflowExplanation = { why: string; now: string; you: string }

const inFlight: Record<string, string> = {
  searching: "Media Manager søger efter den lige nu.",
  candidate_selected: "En udgivelse er valgt og er på vej til AltMount.",
  submitting: "En udgivelse er ved at blive sendt til AltMount.",
  queued: "AltMount er i gang med den valgte udgivelse.",
  downloading: "AltMount er i gang med den valgte udgivelse.",
  streaming_ready: "AltMount er i gang med den valgte udgivelse.",
  import_complete: "Filen er klar hos AltMount og skal kontrolleres.",
  verifying: "Filen bliver kontrolleret for kvalitet og sprog.",
  publishing: "Filen er godkendt og er ved at blive lagt i Plex.",
  plex_scanning: "Filen er lagt i Plex, og Media Manager venter på, at Plex bekræfter den.",
}

function candidateSummary(candidates: APIWorkflowCandidate[] | undefined): string {
  if (!candidates) return "Media Manager venter på en udgivelse, der opfylder kravene."
  if (candidates.length === 0) return "Ingen indexer har fundet en udgivelse til den endnu."
  const usable = candidates.filter((candidate) => candidate.decision?.verdict === "eligible" && !candidate.exclusion).length
  if (usable > 0) return `${usable} ${usable === 1 ? "egnet udgivelse venter" : "egnede udgivelser venter"} på at blive prøvet.`
  const excluded = candidates.filter((candidate) => candidate.exclusion).length
  const counts = new Map<string, number>()
  for (const candidate of candidates) {
    if (candidate.exclusion) continue
    const reason = candidateReasonText(candidate.decision?.verdict, candidate.decision?.decision_explanation?.reasons ?? [], 1)
    if (reason) counts.set(reason, (counts.get(reason) ?? 0) + 1)
  }
  const common = [...counts].sort((a, b) => b[1] - a[1])[0]?.[0]
  return [
    `Søgningerne har fundet ${candidates.length} ${candidates.length === 1 ? "udgivelse" : "udgivelser"}, men ingen kan bruges.`,
    common ? `Hyppigste grund: ${common}.` : "",
    excluded ? `${excluded} er prøvet og sorteret fra.` : "",
  ].filter(Boolean).join(" ")
}

/** The round is spent and discovery only watches for a release not tried before. */
function roundUsedUp(workflow: APIWorkflow): boolean {
  return workflow.search_allowed === false && workflow.retry_discovery_state === "waiting"
}

function nextStep(workflow: APIWorkflow, now: number): string {
  if (workflow.new_release_ready || workflow.retry_discovery_state === "ready") return `En ny egnet udgivelse er fundet${workflow.ready_release_title ? `: ${workflow.ready_release_title}` : ""}.`
  if (roundUsedUp(workflow)) {
    const next = workflow.next_rescan_at ? new Date(workflow.next_rescan_at).getTime() : 0
    return next > now ? `Alle forsøg er brugt. Den holder øje med nye udgivelser og kigger igen ${formatRelative(workflow.next_rescan_at, now)}.` : "Alle forsøg er brugt. Den holder øje med, om en ny egnet udgivelse dukker op."
  }
  if (workflow.search_allowed === false) return "Den søger ikke lige nu, fordi søgning er spærret for denne opgave."
  if (workflow.next_rescan_at) {
    const due = new Date(workflow.next_rescan_at).getTime() <= now
    return due ? "Næste søgning er planlagt og kører snart." : `Søger igen ${formatRelative(workflow.next_rescan_at, now)}.`
  }
  if (workflow.retry_discovery_state === "waiting") return "Den holder øje med, om en ny egnet udgivelse dukker op."
  return "Den søger ikke selv videre."
}

/** The owner's three questions, answered only from persisted facts. */
export function explainWorkflow(workflow: APIWorkflow, candidates: APIWorkflowCandidate[] | undefined, now = Date.now()): WorkflowExplanation {
  if (workflow.state === "plex_confirmed") return { why: "Den ligger i Plex, og Plex har bekræftet filen.", now: "Intet. Arbejdet er færdigt.", you: "Nej." }
  if (workflow.terminal_at) return { why: "Arbejdet blev stoppet, før en fil blev bekræftet i Plex.", now: "Intet. Den arbejder ikke videre med denne opgave.", you: "Kun hvis du stadig vil have den. Brug Hent på titelsiden." }
  const you = workflow.owner_action_required
    ? "Ja. Se Hvad kan du gøre? herunder."
    : workflow.new_release_ready || workflow.retry_discovery_state === "ready"
      ? "Ja, hvis du vil prøve den nye udgivelse. Se Hvad kan du gøre? herunder."
      : roundUsedUp(workflow)
        ? workflow.available_actions?.some((action) => action.action === "open_round_from_known" && action.available)
          ? "Ikke nødvendigt. Du kan prøve en kendt udgivelse nu. Ellers venter den på en ny."
          : "Ikke lige nu. Når en ny udgivelse dukker op, skal du starte en ny runde."
        : "Nej. Media Manager klarer det selv."
  const running = inFlight[workflow.state]
  if (running) {
    const percent = workflow.download_percent === undefined ? "" : ` ${Math.round(workflow.download_percent)} % er klar.`
    return { why: "Den er på vej, men ikke færdig endnu.", now: `${running}${percent}`, you }
  }
  if (workflow.attention_code && workflow.owner_action_required) {
    return { why: `${attentionLabel(workflow.attention_code)}.${workflow.attention_detail ? ` ${translateDetail(workflow.attention_detail)}` : ""}`, now: nextStep(workflow, now), you }
  }
  return { why: candidateSummary(candidates), now: nextStep(workflow, now), you }
}

/**
 * What the indexer claims about languages before any file exists to check. A
 * claim is never proof, so the wording says so.
 */
export function indexerLanguageNote(candidates: APIWorkflowCandidate[] | undefined): string | undefined {
  const chosen = candidates?.find((candidate) => candidate.selected) ?? candidates?.find((candidate) => candidate.decision?.verdict === "eligible" && !candidate.exclusion)
  const claims = (chosen?.decision?.decision_explanation?.evidence ?? []).filter((fact) => (fact.fact === "subtitle_languages" || fact.fact === "audio_languages") && (fact.state === "observed" || fact.state === "inferred"))
  const names = [...new Set(claims.flatMap((fact) => fact.values ?? []).map((code) => code.toLowerCase()).filter((code) => code && code !== "und" && code !== "unknown"))].map((code) => languageName(code).toLowerCase())
  if (names.length === 0) return undefined
  const list = names.length > 1 ? `${names.slice(0, -1).join(", ")} og ${names.at(-1)}` : names[0]
  return `${list.charAt(0).toUpperCase()}${list.slice(1)} nævnt af indexeren, men endnu ikke kontrolleret.`
}

type Evidence = { videos?: Array<{ audio_languages?: string[]; languages?: Array<{ kind: string; canonical?: string; forced?: boolean; track_index?: number; full_dialogue?: boolean }> }> }

/** What the last file check really found, one line per required language. */
export function languageCheckLines(verification: APISubtitleVerification): string[] {
  const videos = (verification.evidence as Evidence).videos ?? []
  const first = videos[0]
  const lines = verification.required_languages.map((code) => {
    const name = languageName(code)
    if (first?.audio_languages?.includes(code)) return `${name} tale fundet i filen.`
    const tracks = (first?.languages ?? []).filter((language) => language.canonical === code)
    const full = tracks.find((language) => !language.forced || language.full_dialogue)
    if (full) {
      const where = full.kind === "external" ? "den medfølgende tekstfil" : `filens spor ${full.track_index}`
      return `${name} tekst fundet i ${where}.${full.forced ? " Sporet er mærket forced, men indeholder en fuld tekst." : ""}`
    }
    if (tracks.length > 0) return `Kun en forced-tekst på ${name.toLowerCase()}. Den oversætter ikke hele dialogen og tæller ikke.`
    return verification.status === "verified" ? `Godkendt, fordi lydsporet ikke er sprogmærket, og titlens originalsprog er ${name.toLowerCase()}.` : `${name} blev ikke fundet i filen.`
  })
  if (videos.length > 1) lines.push(`Kontrolleret i ${videos.length} filer. Linjerne viser den første.`)
  return lines.length > 0 ? lines : ["Profilen kræver intet bestemt sprog."]
}
