import type { APIErrorLogEntry } from "@/api/client"
import { attentionLabel, humanize, translateDetail } from "@/i18n"
import { formatDateTime } from "@/format"

export type ErrorLogArea = "" | "download" | "language" | "search" | "repair" | "title" | "connection" | "backup"

export const errorLogAreas: Array<{ value: ErrorLogArea; label: string }> = [
  { value: "", label: "Alle" },
  { value: "download", label: "Download" },
  { value: "language", label: "Sprog" },
  { value: "search", label: "Søgning" },
  { value: "repair", label: "Reparation" },
  { value: "title", label: "Sager" },
  { value: "connection", label: "Forbindelse" },
  { value: "backup", label: "Backup" },
]

const tryNext = "Den udgivelse bruges ikke. Hvis der findes en anden, bliver den prøvet."
const searchLater = "Der bliver søgt igen senere af sig selv."
const openCase = "Åbn sagen for at se, hvad du kan gøre."

const explanations: Record<string, { what: string; next: string }> = {
  subtitle_requirements_unverified: { what: "Filen havde ikke det sprog, profilen kræver.", next: tryNext },
  release_incomplete: { what: "Udgivelsen er ikke længere hel på Usenet.", next: tryNext },
  season_pack_incomplete: { what: "Sæsonpakken manglede afsnit.", next: tryNext },
  archive_unsupported: { what: "Udgivelsen ligger i et arkiv, der ikke kan læses. Det kan være beskadiget eller have en adgangskode.", next: tryNext },
  no_playable_media: { what: "Udgivelsen indeholder ingen videofil.", next: tryNext },
  movie_primary_ambiguous: { what: "Udgivelsen har flere store videofiler, så filmen kan ikke udpeges sikkert.", next: tryNext },
  output_empty: { what: "AltMount gav ingen filer tilbage.", next: tryNext },
  altmount_job_attention: { what: "AltMount meldte fejl på jobbet.", next: tryNext },
  altmount_identity_unresolved: { what: "AltMount-jobbet kunne ikke genkendes.", next: tryNext },
  newznab_transport_failed: { what: "Indexeren kunne ikke nås.", next: searchLater },
  newznab_http_429: { what: "Indexeren afviste søgningen, fordi der er sendt for mange.", next: searchLater },
  newznab_provider_100: { what: "Indexeren afviste API-nøglen.", next: "Tjek nøglen under Kilder." },
  newznab_provider_910: { what: "Indexeren har lukket for API-adgang.", next: "Tjek din konto hos indexeren." },
  indexer_request_failed: { what: "Søgningen hos indexeren fejlede.", next: searchLater },
  missing_required_audio: { what: "Filen mangler det lydspor, biblioteket kræver.", next: openCase },
  corrupted: { what: "Filen er beskadiget eller mangler dele på Usenet.", next: openCase },
  missing_file: { what: "Filen findes ikke længere i AltMount.", next: openCase },
  owner_requested: { what: "Du bad om at få filen erstattet.", next: openCase },
  connection_failed: { what: "Forbindelsen virkede ikke ved seneste test.", next: "Tjek adresse og nøgle, og test forbindelsen igen under Kilder eller Indstillinger." },
  backup_failed: { what: "Backuppen fejlede.", next: "Se detaljen nedenfor, og start en ny backup under System." },
}

export function errorExplanation(entry: Pick<APIErrorLogEntry, "code" | "area">): { what: string; next: string } {
  const known = explanations[entry.code]
  if (known) return known
  const http = entry.code.match(/^newznab_http_(5\d\d)$/)
  if (http) return { what: `Indexeren havde en fejl på sin side (kode ${http[1]}).`, next: searchLater }
  const provider = entry.code.match(/^newznab_provider_(\d+)$/)
  if (provider) return { what: `Indexeren svarede med fejlkode ${provider[1]}.`, next: searchLater }
  if (entry.area === "title") return { what: attentionLabel(entry.code), next: openCase }
  if (entry.area === "search") return { what: humanize(entry.code), next: searchLater }
  return { what: humanize(entry.code), next: entry.area === "download" || entry.area === "language" ? tryNext : openCase }
}

export function errorDetail(entry: Pick<APIErrorLogEntry, "detail">): string {
  return translateDetail(entry.detail)
}

export function errorAreaLabel(area: string): string {
  return errorLogAreas.find((entry) => entry.value === area)?.label ?? humanize(area)
}

/** One entry as plain text, for pasting into a request for help. */
export function errorEntryText(entry: APIErrorLogEntry, scope = ""): string {
  const { what, next } = errorExplanation(entry)
  const title = [entry.title, scope].filter(Boolean).join(" · ")
  return [
    `${formatDateTime(entry.occurred_at)} · ${errorAreaLabel(entry.area)}${entry.open ? " · Kræver dig" : ""}${entry.count > 1 ? ` · ${entry.count} gange` : ""}`,
    what,
    title ? `Titel: ${title}` : "",
    entry.subject ? `Udgivelse eller kilde: ${entry.subject}` : "",
    `Det sker nu: ${next}`,
    `Kode: ${entry.code}`,
    entry.detail ? `Detalje: ${errorDetail(entry)}` : "",
  ].filter(Boolean).join("\n")
}

export function errorLogText(entries: APIErrorLogEntry[], total: number, scope: (entry: APIErrorLogEntry) => string = () => ""): string {
  const heading = total > entries.length ? `Media Manager fejllog: de ${entries.length} nyeste af ${total}` : `Media Manager fejllog: ${entries.length} fejl`
  return [heading, ...entries.map((entry) => errorEntryText(entry, scope(entry)))].join("\n\n") + "\n"
}
