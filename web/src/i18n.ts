/**
 * Danish owner-facing labels. The API speaks stable English codes; this file is
 * the only place they become words, so a new code shows up here or falls back
 * to a readable form of the code itself — never to a guess.
 */
import type { Tone } from "@/lib/tone"

const stateLabels: Record<string, string> = {
  monitored: "Overvåges",
  waiting: "Venter",
  searching: "Søger",
  candidate_selected: "Udgivelse valgt",
  submitting: "Sendes til AltMount",
  transport_unknown: "Afsendelse uafklaret",
  queued: "Henter",
  downloading: "Henter",
  streaming_ready: "Klar til streaming",
  import_complete: "Importeret",
  verifying: "Kontrolleres",
  publishing: "Udgives",
  plex_scanning: "Plex scanner",
  plex_confirmed: "I Plex",
  needs_attention: "Kræver handling",
  held: "Afventer",
  blocked: "Blokeret",
  quarantined: "I karantæne",
  cancelled: "Annulleret",
  failed: "Fejlet",
  available: "Tilgængelig",
  not_approved: "Afventer godkendelse",
  approved_in_seerr: "Godkendt i Seerr",
  declined: "Afvist",
  deleted: "Slettet i Seerr",
}

export function stateLabel(state: string | undefined | null): string {
  if (!state) return "Ukendt"
  return stateLabels[state] ?? humanize(state)
}

export function stateTone(state: string | undefined | null): Tone {
  switch (state) {
    case "plex_confirmed":
    case "available":
      return "good"
    case "needs_attention":
    case "blocked":
    case "quarantined":
    case "transport_unknown":
    case "not_approved":
      return "warn"
    case "failed":
    case "declined":
      return "bad"
    case "searching":
    case "candidate_selected":
    case "submitting":
    case "queued":
    case "downloading":
    case "streaming_ready":
    case "import_complete":
    case "verifying":
    case "publishing":
    case "plex_scanning":
    case "approved_in_seerr":
      return "info"
    default:
      return "neutral"
  }
}

const attentionLabels: Record<string, string> = {
  duplicate_package_no_alternate: "Kun samme pakke fundet – intet alternativ",
  altmount_output_unavailable: "AltMount-filen kan ikke læses",
  altmount_mount_unavailable: "AltMount-drevet er ikke tilgængeligt",
  output_inspection_failed: "Filen bestod ikke kontrollen",
  retained_failed_output_limit: "Grænsen for gemte fejlede filer er nået",
  release_retrieval_failed: "NZB kunne ikke hentes",
  release_retrieval_limit: "Grænsen for NZB-hentninger er nået",
  altmount_job_attention: "AltMount-jobbet kræver handling",
  altmount_identity_unresolved: "AltMount-jobbet kan ikke genkendes",
  seerr_snapshot_changed: "Seerr har ændret anmodningen",
  seerr_request_revoked: "Anmodningen blev trukket tilbage i Seerr",
  publication_failed: "Udgivelse til biblioteket fejlede",
  plex_timeout: "Plex fandt ikke filen i tide",
  round_exhausted_waiting_new_identity: "Alle forsøg brugt – venter på en ny udgivelse",
  subtitle_requirements_unverified: "Danske undertekster kunne ikke bekræftes",
  release_movie_year_conflict: "Udgivelsens år passer ikke",
  season_expectation_missing: "Sæsonens afsnit kendes ikke",
}

export function attentionLabel(code: string | undefined | null): string {
  if (!code) return ""
  return attentionLabels[code] ?? humanize(code)
}

const actionLabels: Record<string, { label: string; help: string }> = {
  search: { label: "Søg nu", help: "Én søgning hos alle indexere, med normal pacing." },
  discover: { label: "Led efter nye udgivelser", help: "Finder nye udgivelser uden at hente noget." },
  open_next_round: { label: "Start ny runde", help: "Prøver den nye udgivelse som opdagelsen fandt." },
  open_round_from_known: { label: "Prøv kendte udgivelser", help: "Starter en runde med den bedste udgivelse, der endnu ikke er prøvet." },
  reconcile_altmount: { label: "Tjek AltMount igen", help: "Læser AltMount-jobbet igen og fortsætter. Henter ikke en ny NZB." },
  reconfirm_plex: { label: "Bekræft i Plex igen", help: "Beder Plex om at finde den præcise fil igen." },
  cancel: { label: "Annullér", help: "Stopper arbejdet. Filer, der allerede ligger i biblioteket, bliver liggende." },
  retry_access_rejected_release: { label: "Prøv afvist udgivelse igen", help: "Prøver den udgivelse, som indexeren afviste med 401/403." },
  recover_altmount_submission: { label: "Afklar afsendelse", help: "Afklarer en afsendelse til AltMount med ukendt udfald." },
  reverify_retained_output: { label: "Kontrollér gemt fil igen", help: "Kører undertekst-kontrollen igen på en gemt fil." },
  reinspect_retained_movie: { label: "Undersøg film igen", help: "Undersøger den seneste uklare film én gang til." },
  revive_rejected_archives: { label: "Genoplív afviste arkiver", help: "Giver udgivelser afvist som arkiver en ny chance." },
  relocate_links: { label: "Flyt til andet bibliotek", help: "Flytter de udgivne links til en anden biblioteksmappe." },
}

export function actionLabel(action: string): { label: string; help: string } {
  return actionLabels[action] ?? { label: humanize(action), help: "" }
}

export function kindLabel(kind: string | undefined): string {
  if (kind === "movie") return "Film"
  if (kind === "series") return "Serie"
  return "Ukendt"
}

/** Turns an unknown code into readable text without pretending to translate it. */
export function humanize(code: string): string {
  const text = code.replaceAll("_", " ").trim()
  return text ? text[0].toUpperCase() + text.slice(1) : code
}

const languageNames: Record<string, string> = {
  da: "Dansk", en: "Engelsk", sv: "Svensk", no: "Norsk", nb: "Norsk", fi: "Finsk", de: "Tysk", fr: "Fransk",
  es: "Spansk", it: "Italiensk", nl: "Hollandsk", pl: "Polsk", pt: "Portugisisk", ja: "Japansk", ko: "Koreansk",
  zh: "Kinesisk", ru: "Russisk", tr: "Tyrkisk", ar: "Arabisk", hi: "Hindi", is: "Islandsk",
}

export function languageName(code: string | undefined): string {
  if (!code) return ""
  return languageNames[code.toLowerCase()] ?? code.toUpperCase()
}

/**
 * Danish versions of the server's most common explanations. Anything not
 * listed is shown as the server wrote it, so no detail is ever lost.
 */
const detailPatterns: Array<[RegExp, (match: RegExpMatchArray) => string]> = [
  [/^no eligible release yet$/i, () => "Ingen egnet udgivelse fundet endnu."],
  [/^All (\d+) acquisition slots in this round were used\. Scheduled discovery will wait for a newly observed eligible release; opening another round requires the owner\.$/,
    (m) => `Alle ${m[1]} forsøg i runden er brugt. Media Manager holder øje med nye udgivelser; en ny runde kræver dig.`],
  [/^Two release-attributable acquisition slots were used\. Scheduled discovery will wait for a newly observed eligible release; opening another round requires the owner\.$/,
    () => "To forsøg i runden er brugt. Media Manager holder øje med nye udgivelser; en ny runde kræver dig."],
  [/^Completed season has no season pack observed; released episodes are acquired individually\.$/,
    () => "Der er ingen sæsonpakke, så afsnittene hentes hver for sig."],
  [/^Airing season: released episodes are acquired individually as they air; future episodes are not searched before their release date\.$/,
    () => "Sæsonen sendes stadig: afsnit hentes, efterhånden som de bliver sendt."],
]

export function translateDetail(text: string | undefined | null): string {
  if (!text) return ""
  const trimmed = text.trim()
  for (const [pattern, render] of detailPatterns) {
    const match = trimmed.match(pattern)
    if (match) return render(match)
  }
  return trimmed
}
