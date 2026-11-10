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
  duplicate_package_skip_limit: "Samme pakke dukkede op for mange gange",
  altmount_submission_rejected: "AltMount afviste udgivelsen",
  subtitle_verification_failed: "Sprogkontrollen fejlede",
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

const repairReasons: Record<string, string> = {
  corrupted: "Ødelagt fil",
  missing_required_audio: "Mangler dansk tale",
  missing_file: "Fil forsvundet",
  owner_requested: "Ønsket erstattet",
}

export function repairReasonLabel(reason: string): string {
  return repairReasons[reason] ?? humanize(reason)
}

const componentOutcomes: Record<string, string> = {
  success: "OK",
  no_results: "OK",
  rate_limited: "Søgegrænse nået",
  auth_failed: "Login afvist",
  system_failure: "Fejl",
  cancelled: "Afbrudt",
}

const componentKinds: Record<string, string> = {
  newznab: "Indexer",
  oldboys: "Indexer",
  altmount: "Download",
  plex: "Afspiller",
  seerr: "Anmodninger",
  tmdb: "Titeloplysninger",
}

/** What a connection is used for, shown beside its name on the status page. */
export function componentKindLabel(kind: string): string {
  return componentKinds[kind] ?? humanize(kind)
}

/** The last answer from a connection or indexer on the status page. */
export function componentOutcomeLabel(outcome: string): string {
  return componentOutcomes[outcome] ?? humanize(outcome)
}

const activityActions: Record<string, string> = {
  "library.repair_opened": "Reparation startet",
  "upgrade.displaced_link_removed": "Gammelt link fjernet efter udskiftning",
  "library.links_relocated": "Links flyttet til andet bibliotek",
  "library.root_updated": "Biblioteksmappe ændret",
  "media.created": "Titel oprettet",
  "media.archived": "Titel arkiveret",
  "media.unarchived": "Titel gendannet fra arkivet",
  "media.metadata_refreshed": "Titeloplysninger opdateret",
  "request.intake": "Anmodning modtaget",
  "season.airing_coordinated": "Igangværende sæson planlagt",
  "release.nfo_checked": "NFO-fil tjekket",
  "release.blocked": "Udgivelse blokeret",
  "backup.requested": "Backup bestilt",
  "backup.verified": "Backup kontrolleret",
  "seerr.route_saved": "Seerr-rute gemt",
  "newznab.commissioned": "Indexer godkendt",
  "oldboys.commissioned": "Indexer godkendt",
  "oldboys.credentials_saved": "Indexer-login gemt",
  "oldboys.budget_set": "Indexerbudget ændret",
  "prowlarr.backup_imported": "Prowlarr-backup importeret",
  "integration.request_budgets_updated": "Søgebudgetter ændret",
  "integration.settings_changed": "Forbindelse ændret",
  "quality_profile.policy_applied": "Kvalitetsprofil anvendt",
  "quality_profile.deleted": "Kvalitetsprofil slettet",
  "library.root_deleted": "Biblioteksmappe slettet",
  "integration.deleted": "Forbindelse slettet",
  "output.movie_reinspection_requested": "Film sat til ny undersøgelse",
  "subtitle.output_reverification_requested": "Ny sprogkontrol bestilt",
  "subtitle.output_reverified": "Sprogkontrol kørt igen",
  "workflow.created": "Arbejde oprettet",
  "workflow.archive_rejections_revived": "Afviste arkiver fik en ny chance",
  "workflow.plex_reconfirm_requested": "Plex bedt om at bekræfte igen",
  "workflow.access_rejected_release_retried": "Afvist udgivelse prøvet igen",
  "workflow.discovery_requested": "Bedt om at lede efter nye udgivelser",
  "workflow.acquisition_round_opened": "Ny runde startet, fordi en ny upload passede",
  "workflow.quality_policy_applied": "Kvalitetsprofil anvendt på arbejdet",
}

/** One activity line. A workflow's state changes are stored as workflow.<state>. */
export function activityLabel(action: string): string {
  const known = activityActions[action]
  if (known) return known
  if (action.startsWith("workflow.")) return `Status: ${stateLabel(action.slice("workflow.".length))}`
  return humanize(action.replaceAll(".", " "))
}

const activityActors: Record<string, string> = { owner: "Dig", worker: "Baggrundsarbejde", policy: "Regler", seerr: "Seerr", system: "System", migration: "Opdatering" }

export function activityActorLabel(actor: string): string {
  return activityActors[actor] ?? humanize(actor)
}

const queueEvents: Record<string, string> = {
  "workflow.search_requested": "Søgning",
  "workflow.discovery_rescan_requested": "Leder efter nye udgivelser",
  "workflow.transitioned": "Statusskift",
  "seerr.reconcile": "Tjek af Seerr",
  "seerr.status_sync_requested": "Status sendes til Seerr",
  "altmount.reconcile_requested": "Tjek af AltMount",
  "altmount.repair_sweep_requested": "Tjek af ødelagte filer",
  "altmount.movie_reinspect_requested": "Ny undersøgelse af film",
  "altmount.output_reverify_requested": "Ny sprogkontrol",
  "release.retrieve_requested": "Hentning af NZB",
  "library.publish_requested": "Udgivelse til biblioteket",
  "library.displaced_remove_requested": "Fjernelse af udskiftet fil",
  "library.unmanaged_broken_remove_requested": "Fjernelse af ødelagt link",
  "plex.scan_requested": "Plex-scanning",
  "plex.confirm_requested": "Plex-bekræftelse",
  "plex.displaced_refresh_requested": "Plex opdateres efter udskiftning",
  "backup.create_requested": "Backup",
}

export function queueEventLabel(event: string): string {
  return queueEvents[event] ?? humanize(event.replaceAll(".", " "))
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
  por: "Portugisisk", kor: "Koreansk", chi: "Kinesisk", zho: "Kinesisk", rus: "Russisk", tur: "Tyrkisk", ara: "Arabisk", hin: "Hindi",
  ice: "Islandsk", isl: "Islandsk", cze: "Tjekkisk", ces: "Tjekkisk", gre: "Græsk", ell: "Græsk", heb: "Hebraisk", hun: "Ungarsk",
  rum: "Rumænsk", ron: "Rumænsk", ukr: "Ukrainsk", tha: "Thai", vie: "Vietnamesisk", ind: "Indonesisk", hrv: "Kroatisk",
}

export function languageName(code: string | undefined): string {
  if (!code) return ""
  return languageNames[code.toLowerCase()] ?? code.toUpperCase()
}

/**
 * Danish versions of the server's most common explanations. Anything not
 * listed is shown as the server wrote it, so no detail is ever lost.
 */
/** How a used-up title gets its next round: by a fitting new upload or by the owner. */
const nextRoundText = "En ny runde kan starte af sig selv, når en ny upload passer til titlen; du kan også selv starte den."

const detailPatterns: Array<[RegExp, (match: RegExpMatchArray) => string]> = [
  [/^no eligible release yet$/i, () => "Ingen egnet udgivelse fundet endnu."],
  [/^replacement confirmed in Plex; the old (?:file was retired|symlink was removed)$/, () => "Erstatningen er bekræftet i Plex, og den gamle fil er fjernet."],
  [/^the file has no ([a-z]{2,3}) audio track; a dubbed replacement is being acquired$/, (m) => `Filen har ikke ${languageName(m[1]).toLowerCase()} tale. Media Manager henter en udgave med ${languageName(m[1]).toLowerCase()} tale.`],
  [/^no dubbed release was found; the current file stays and is tried again later$/, () => "Ingen udgave med tale fundet. Den nuværende fil bliver liggende, og der prøves igen senere."],
  [/^AltMount reports missing Usenet articles; a replacement is being acquired$/, () => "AltMount melder, at dele af filen mangler hos Usenet. Media Manager henter en erstatning."],
  [/^AltMount no longer has the published file \(its Usenet articles went missing\); a replacement is being acquired$/, () => "AltMount har ikke længere filen, fordi dele af den er forsvundet hos Usenet. Media Manager henter en erstatning."],
  [/^the owner asked for this file to be replaced; it keeps playing until the replacement is confirmed$/, () => "Du har bedt om en erstatning. Filen kan afspilles, indtil erstatningen er bekræftet."],
  [/^the title's original language is ([a-z]{2,3}); its own audio needs no upgrade$/, (m) => `Titlens originalsprog er ${languageName(m[1]).toLowerCase()}, så talen behøver ingen udskiftning.`],
  [/^season (\d+) episode (\d+) could not be verified: Plex and TMDB disagree on the episode \(numbering differs\)$/, (m) => `Sæson ${m[1]} afsnit ${m[2]} kunne ikke bekræftes, fordi Plex og TMDB nummererer afsnittene forskelligt.`],
  [/^Plex has no single item at (.+)$/, (m) => `Plex har ikke ét bestemt emne for filen: ${m[1]}`],
  [/^All (\d+) acquisition slots in this round were used\. Scheduled discovery will wait for a newly observed eligible release; opening another round requires the owner\.$/,
    (m) => `Alle ${m[1]} forsøg i runden er brugt. Media Manager holder øje med nye udgivelser. ${nextRoundText}`],
  [/^All (\d+) acquisition slots in this round were used\. Scheduled discovery will wait for a newly observed eligible release; another round is opened by the owner, or for a new upload that fits this title when automatic rounds are on\.$/,
    (m) => `Alle ${m[1]} forsøg i runden er brugt. Media Manager holder øje med nye udgivelser. ${nextRoundText}`],
  [/^Two release-attributable acquisition slots were used\. Scheduled discovery will wait for a newly observed eligible release; opening another round requires the owner\.$/,
    () => `To forsøg i runden er brugt. Media Manager holder øje med nye udgivelser. ${nextRoundText}`],
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
  not_published: "Filen er ikke længere en aktuel AltMount-fil i biblioteket. Genindlæs titlen, før du prøver igen.",
  publication_in_flight: "Filen er stadig ved at blive udgivet. Vent, til arbejdet er færdigt, og prøv igen.",
  invalid_path: "Filens AltMount-sti er ugyldig. Genindlæs titlen; vælg ikke en anden filsti selv.",
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
  library_root_in_use: "Mappen bruges stadig af titler eller Seerr-regler. En mappe, der har haft titler, kan slås fra, men ikke slettes.",
  library_root_disabled: "Mappen er slået fra.",
  secret_storage_unavailable: "Serveren kan ikke gemme nøgler endnu (SECRET_ENCRYPTION_KEY mangler).",
  metadata_unavailable: "TMDB er ikke sat op. Tilføj TMDB under Indstillinger → Forbindelser.",
  seerr_unavailable: "Seerr svarer ikke lige nu. Prøv igen om lidt.",
  invalid_configuration: "Biblioteket og kvalitetsprofilen passer ikke sammen, eller et af dem er slået fra.",
  execution_failed: "Overtagelsen kunne ikke startes, og ingen filer er flyttet. Tjek, at titlerne ikke er i gang med noget andet.",
  unsafe_source_root: "Mappen skal være bibliotekets mappe eller en mappe inde i den.",
  scan_limit: "Mappen har mere end 50.000 filer. Vælg en undermappe.",
  scan_failed: "Mappen kunne ikke læses. Tjek, at den findes, og at Media Manager kan se den.",
  adoption_plan_failed: "Planen kunne ikke oprettes ud fra mappen. Prøv en undermappe.",
  review_failed: "Filen kunne ikke godkendes. Tjek titel, navn og sæson. Navnet må ikke allerede være i brug.",
  approval_failed: "Planen kunne ikke låses. Alle filer skal være gennemgået, og højst 25 må være godkendt.",
  confirmation_required: "Bekræftelsen skal skrives præcist som vist.",
  replacement_confirmation_required: "Overtagelsen kan erstatte en fil, du allerede har, og kræver en ekstra bekræftelse.",
  relocation_in_progress: "Titlen er allerede ved at blive flyttet. En afbrudt flytning skal gøres færdig: vælg det samme bibliotek igen.",
  quality_profile_changed: "Profilen er blevet ændret imens, så intet er gemt. Luk vinduet, åbn profilen igen, og lav ændringen igen.",
  quality_profile_in_use: "Profilen bruges af titler i biblioteket eller en Seerr-regel. Vælg en anden profil, de skal flyttes til, eller slå profilen fra under Rediger.",
  quality_profile_replacement_invalid: "Den valgte profil kan ikke overtage. Den skal være slået til og gælde samme slags (film eller serier).",
  library_root_is_default: "Standardmappen kan ikke slettes. Gør først en anden mappe til standard.",
  integration_enabled: "Slå den fra, før den slettes.",
  integration_in_use: "Den kan ikke slettes, fordi den har været brugt: dens søgninger, downloads eller anmodninger er en del af titlernes historik. Lad den være slået fra i stedet.",
  invalid_name: "Navnet er tomt eller bruges allerede af en anden mappe af samme slags.",
  quality_profile_is_default: "Standardprofilen kan ikke slettes. Gør først en anden profil til standard.",
  policy_application_failed: "Opgaverne ændrede sig imens, så intet blev flyttet over på profilen. Prøv igen.",
}

/** A replacement request is an acquisition attempt, never proof of a new file. */
export function replacementOutcomeText(outcome: string): string {
  const messages: Record<string, string> = {
    opened: "Søgningen efter en erstatning er sat i gang. Den nuværende fil beholdes, indtil erstatningen er bekræftet i Plex.",
    attached: "Der søges allerede efter en erstatning til titlen. Filen er knyttet til det arbejde.",
    already_open: "Der er allerede en erstatningssag for denne fil.",
    needs_attention: "Filen kan ikke erstattes automatisk. Se årsagen under System → Reparationer.",
  }
  return messages[outcome] ?? "Svaret kunne ikke genkendes. Se status under System → Reparationer, før du prøver igen."
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

export function externalCopyLabel(state: string): string {
  const labels: Record<string, string> = {
    confirmed: "Ekstern kopi bekræftet",
    failed: "Ekstern kopiering fejlede",
    pending: "Afventer ekstern kopi",
    unknown: "Ekstern kopi mangler fuld bekræftelse",
    not_configured: "Ingen ekstern kopi dokumenteret",
  }
  return labels[state] ?? "Ekstern kopistatus er ukendt"
}

export function backupStateLabel(state: string, localFileDeleted = false): string {
  if (localFileDeleted) return "Filen er slettet"
  return ({ verified: "Database kontrolleret", failed: "Backup fejlet", queued: "I kø", running: "Kører" } as Record<string, string>)[state] ?? "Ukendt backupstatus"
}
