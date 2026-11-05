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

/**
 * The state an owner sees for a workflow. A season acquired episode by episode
 * is complete once every episode is in Plex, although its season workflow
 * itself stays waiting.
 */
export function workflowDisplayState(workflow: { state: string; covered_by_episodes?: boolean }): string {
  return workflow.state === "waiting" && workflow.covered_by_episodes ? "plex_confirmed" : workflow.state
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
  // Three-letter codes as files and Plex tag them.
  dan: "Dansk", eng: "Engelsk", swe: "Svensk", nor: "Norsk", nob: "Norsk", fin: "Finsk", ger: "Tysk", deu: "Tysk",
  fre: "Fransk", fra: "Fransk", spa: "Spansk", ita: "Italiensk", dut: "Hollandsk", nld: "Hollandsk", pol: "Polsk", jpn: "Japansk",
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
  [/^The next eligible release resolved to an exact package already submitted in this workflow, and no unexcluded eligible candidate remains\.$/,
    () => "Den næste egnede udgivelse er præcis den samme pakke, som allerede er prøvet, og der er ingen andre egnede kandidater tilbage."],
  [/^AltMount reported completion, but its read-only output was still unavailable after five minutes\. Verify the shared-folder mount before retrying\.$/,
    () => "AltMount meldte færdig, men filen kunne stadig ikke læses efter fem minutter. Tjek den delte mappe, før du prøver igen."],
  [/^(\d+) failed outputs are retained \(limit (\d+)\); owner storage review is required before another NZB request\.$/,
    (m) => `${m[1]} fejlede downloads er gemt (grænse ${m[2]}). Ryd op i lageret, før der hentes igen.`],
  [/^bounded media-info inspection failed for (.+): media-info probe failed$/,
    (m) => `Filen kunne ikke undersøges (media-info fejlede): ${m[1]}`],
  [/^fast-fail segment check failed: no regular files were successfully processed \(all files failed validation\)$/,
    () => "Udgivelsen mangler dele hos Usenet-udbyderne (hurtigtjekket fejlede)."],
  [/^failed to parse NZB file: NZB file contains only PAR2 files\..*$/, () => "NZB-filen indeholder kun reparationsfiler (PAR2). Der mangler dele hos udbyderne."],
  [/^no files were successfully processed \(all files failed validation\)$/, () => "Ingen af filerne kunne bruges; alle fejlede kontrollen."],
  [/^exact NZB package was already submitted in this workflow$/, () => "Præcis samme pakke er allerede prøvet i dette arbejde."],
  [/^NZB validation failed: invalid NZB: total size is zero$/, () => "NZB-filen er ugyldig (størrelsen er 0)."],
  [/^The file is incomplete or missing parts\..*$/, () => "Filen er ufuldstændig: nogle dele findes ikke hos nogen af dine Usenet-udbydere."],
  [/^selected release NZB could not be retrieved after two bounded requests \((.+)\)$/, (m) => `NZB-filen kunne ikke hentes fra indexeren efter to forsøg (${m[1]}).`],
  [/^selected release NZB access was rejected after one bounded request \((.+)\)$/, (m) => `Indexeren afviste adgang til NZB-filen (${m[1]}).`],
  [/^no valid first RAR part found in archive$/, () => "Arkivet mangler sin første RAR-del."],
  [/^no files with allowed extensions found\b.*$/, () => "Udgivelsen indeholder ingen video- eller undertekstfiler."],
  [/^movie release does not have one unambiguous primary media file$/, () => "Udgivelsen har ikke én tydelig filmfil."],
  [/^Releases were observed but none satisfied the required release evidence; a paced retry is scheduled\.$/,
    () => "Der er fundet udgivelser, men ingen opfylder profilens krav endnu. Media Manager søger igen senere."],
  [/^AltMount did not expose the stable upload name within 30 minutes\..*$/,
    () => "AltMount viste ikke uploadens navn inden for 30 minutter. Start ikke en ny søgning, før du har tjekket i AltMount, at den første upload ikke blev modtaget."],
  [/^The bounded exact-package duplicate skip limit was reached; owner review is required\.$/,
    () => "Den samme pakke dukkede op for mange gange. Se på arbejdet, og vælg hvad der skal ske."],
  [/^(\w+) candidates failed NZB retrieval in this round; owner review is required\..*$/,
    (m) => `${({ Four: "Fire", Three: "Tre", Two: "To" } as Record<string, string>)[m[1]] ?? m[1]} udgivelser kunne ikke hentes fra indexeren i denne runde. Se på arbejdet. Rundens forsøg er ikke brugt af disse fejl.`],
  [/^fast-fail segment check inconclusive\b.*$/, () => "AltMounts hurtigtjek af udgivelsen blev ikke færdigt i tide."],
  [/^failed to resolve embedded NZB\b.*430 no such article.*$/s, () => "Udgivelsen mangler dele hos Usenet-udbyderen (430 no such article)."],
  [/^the selected release's indexer is switched off or not commissioned; another release is tried$/,
    () => "Indexeren for den valgte udgivelse er slået fra eller ikke godkendt, så en anden udgivelse prøves."],
  [/^season pack is (incomplete|needs_review|invalid)(?:; (\d+) expected episodes are missing)?(?:; first problem: ([a-z_]+)(?: \((.+?)\))?(?: and (\d+) more)?)?$/,
    (m) => seasonPackText(m)],
]

const packVerdicts: Record<string, string> = { incomplete: "er ufuldstændig", needs_review: "skal tjekkes", invalid: "er ugyldig" }
const packIssues: Record<string, string> = {
  duplicate: "et afsnit findes mere end én gang",
  unexpected: "en fil hører ikke til sæsonen",
  ambiguous: "en fil kunne ikke knyttes til et bestemt afsnit",
  unsafe_path: "en fil har en usikker sti",
  path_collision: "to filer ville få samme navn",
}

function seasonPackText(m: RegExpMatchArray): string {
  const parts = [`Sæsonpakken ${packVerdicts[m[1]] ?? m[1]}`]
  if (m[2] && m[2] !== "0") parts.push(m[2] === "1" ? "1 afsnit mangler" : `${m[2]} afsnit mangler`)
  if (m[3]) parts.push(`første problem: ${packIssues[m[3]] ?? humanize(m[3])}${m[4] ? ` (${m[4]})` : ""}${m[5] ? ` og ${m[5]} til` : ""}`)
  return `${parts.join("; ")}.`
}

export function translateDetail(text: string | undefined | null): string {
  if (!text) return ""
  const trimmed = text.trim()
  for (const [pattern, render] of detailPatterns) {
    const match = trimmed.match(pattern)
    if (match) return render(match)
  }
  return trimmed
}

/**
 * Danish text for the server's common error codes. A code not listed keeps
 * the server's own message, so no detail is lost.
 */
const errorCodes: Record<string, string> = {
  database_unavailable: "Databasen svarer ikke lige nu. Prøv igen om lidt.",
  database_error: "Media Manager kunne ikke læse eller gemme i databasen. Prøv igen om lidt.",
  internal_error: "Der skete en uventet fejl i Media Manager. Prøv igen.",
  not_found: "Det findes ikke (længere). Genindlæs siden.",
  invalid_id: "Adressen er ugyldig.",
  unauthorized: "Du er ikke logget ind. Log ind igen via Cosmos, og genindlæs siden.",
  concurrent_modification: "Det blev ændret imens. Genindlæs siden, og prøv igen.",
  altmount_job_active: "AltMount arbejder stadig på et job. Vent, til det er færdigt.",
  workflow_not_cancellable: "Arbejdet kan ikke annulleres nu, fordi det henter, udgiver eller er færdigt.",
  plex_not_timed_out: "Plex er ikke gået i stå med at bekræfte filen, så det kan ikke prøves igen endnu.",
  no_commissioned_indexer: "Der er ingen godkendt indexer, der kan søge efter titlen. Tilføj eller godkend en under Kilder.",
  no_known_eligible_release: "Alle kendte egnede udgivelser er allerede prøvet.",
  no_release_ready: "Der er ingen ny egnet udgivelse klar endnu.",
  round_already_open: "Der er allerede en runde i gang.",
  round_exhausted: "Runden har brugt alle forsøg.",
  round_attempt_limit_reached: "Runden har brugt alle forsøg.",
  retained_output_limit_reached: "Grænsen for gemte fejlede downloads er nået. Ryd op i lageret først.",
  workflow_in_flight: "Arbejdet er allerede i gang.",
  publication_started: "Udgivelsen til Plex er allerede begyndt.",
  owner_recovery_required: "Arbejdet skal afklares, før det kan fortsætte.",
  library_root_in_use: "Mappen bruges stadig af titler eller Seerr-regler.",
  library_root_disabled: "Mappen er slået fra.",
  secret_storage_unavailable: "Serveren kan ikke gemme nøgler endnu (SECRET_ENCRYPTION_KEY mangler).",
  metadata_unavailable: "TMDB er ikke sat op. Tilføj TMDB under Indstillinger → Forbindelser.",
  seerr_unavailable: "Seerr svarer ikke lige nu. Prøv igen om lidt.",
}

export function errorMessage(error: unknown): string {
  if (typeof error === "object" && error !== null) {
    const code = (error as { code?: unknown }).code
    if (typeof code === "string" && errorCodes[code]) return errorCodes[code]
    // fetch() rejects with a TypeError when the server cannot be reached.
    if (error instanceof TypeError && /fetch|network/i.test(error.message)) return "Media Manager kunne ikke nås. Tjek forbindelsen, og prøv igen."
  }
  return error instanceof Error ? error.message : String(error)
}
