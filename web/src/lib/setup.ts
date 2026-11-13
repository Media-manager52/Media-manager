import type { APILibraryRoot, APIRecommendedStep, APISetupStep } from "@/api/client"

/**
 * Suggests where Media Manager should write a new library folder, given the
 * folder Plex sees. Existing roots show how the two views of the disk differ
 * (Plex sees /media/Film, Media Manager writes /srv/media/Film); the most
 * common difference wins. Without existing roots the Plex path is suggested
 * unchanged, and the owner can always edit it.
 */
/** Compares two absolute folders, ignoring trailing slashes. */
export function samePath(a: string, b: string): boolean {
  const clean = (value: string) => value.trim().replace(/\/+$/, "") || "/"
  return clean(a) === clean(b)
}

export function suggestDestination(roots: APILibraryRoot[], plexPath: string): string {
  const path = plexPath.trim()
  if (!path.startsWith("/")) return path
  const counts = new Map<string, number>()
  for (const root of roots) {
    const plex = root.plex_visible_root.split("/")
    const destination = root.destination_root.split("/")
    let shared = 0
    while (shared < plex.length - 1 && shared < destination.length - 1 && plex[plex.length - 1 - shared] === destination[destination.length - 1 - shared]) shared++
    if (shared === 0) continue
    const key = `${plex.slice(0, plex.length - shared).join("/")}\u0000${destination.slice(0, destination.length - shared).join("/")}`
    counts.set(key, (counts.get(key) ?? 0) + 1)
  }
  const ranked = [...counts.entries()].sort((a, b) => b[1] - a[1])
  for (const [key] of ranked) {
    const [plexPrefix, destinationPrefix] = key.split("\u0000")
    if (plexPrefix === "") return destinationPrefix + path
    if (path === plexPrefix || path.startsWith(`${plexPrefix}/`)) return destinationPrefix + path.slice(plexPrefix.length)
  }
  return path
}

/** The AltMount folder as Plex sees it, taken from the roots that already work. */
export function suggestSourceRoot(roots: APILibraryRoot[]): string {
  const counts = new Map<string, number>()
  for (const root of roots) counts.set(root.source_visible_root, (counts.get(root.source_visible_root) ?? 0) + 1)
  return [...counts.entries()].sort((a, b) => b[1] - a[1])[0]?.[0] ?? "/mnt/altmount"
}

const checkPatterns: Array<[RegExp, (match: RegExpMatchArray) => string]> = [
  [/^Plex answered and shows (\d+) libraries$/, (m) => `Plex svarer og har ${m[1]} biblioteker.`],
  [/^AltMount accepted the login and its files are visible$/, () => "AltMount svarer, og filerne kan ses."],
  [/^Seerr accepted the API key$/, () => "Seerr godtog API-nøglen."],
  [/^the Plex token is missing; save it first$/, () => "Plex-token mangler. Gem den først."],
  [/^the Plex token could not be read; save it again$/, () => "Plex-token kunne ikke læses. Gem den igen."],
  [/^the Seerr API key is missing; save it first$/, () => "Seerr-nøglen mangler. Gem den først."],
  [/^the Seerr API key could not be read; save it again$/, () => "Seerr-nøglen kunne ikke læses. Gem den igen."],
  [/^the service could not be reached$/, () => "Tjenesten kunne ikke nås. Tjek adressen."],
  [/address is not valid/, () => "Adressen er ugyldig. Brug https://, eller http:// på dit eget netværk."],
  [/^AltMount answered, but local_read_only_root is empty$/, () => "AltMount svarer, men mappen til AltMount-filerne er ikke udfyldt."],
  [/^AltMount answered, but (.+) is not a folder Media Manager can see$/, (m) => `AltMount svarer, men Media Manager kan ikke se mappen ${m[1]}.`],
  [/^AltMount did not accept the login/, () => "AltMount godtog ikke login. Tjek brugernavn og adgangskode."],
  [/^AltMount settings are incomplete/, () => "AltMount-indstillingerne er ikke komplette. Gem login, eller vælg login uden adgangskode."],
  [/^Plex did not answer with its libraries/, () => "Plex svarede ikke med sine biblioteker. Tjek adresse og token."],
  [/^Seerr .*(401|403|unauthori[sz]ed|forbidden)/i, () => "Seerr afviste API-nøglen."],
  [/^Seerr could not deliver a test notification to that address, so nothing was changed in Seerr/, () => "Seerr kunne ikke nå Media Manager på den adresse. Intet er ændret i Seerr. Tjek adressen."],
  [/^Seerr did not return its webhook settings/, () => "Seerr svarede ikke med sine webhook-indstillinger. Tjek forbindelsen."],
  [/^Seerr saved the webhook, but reading it back/, () => "Seerr gemte webhooken, men den ser ikke rigtig ud bagefter. Prøv igen."],
  [/^the webhook address must be/, () => "Adressen skal starte med http:// eller https://."],
  [/^an indexer is checked by commissioning it$/, () => "En indexer testes ved at godkende den."],
  [/^TMDB accepted the token; title search uses it$/, () => "TMDB godtog nøglen. Søgning efter film og serier bruger den nu."],
  [/^save the TMDB read access token first$/, () => "TMDB-nøglen mangler. Gem den først."],
  [/^the TMDB token is not valid$/, () => "TMDB-nøglen er ugyldig."],
  [/^TMDB did not accept the token/, () => "TMDB afviste nøglen. Brug den lange \"API Read Access Token\", ikke den korte API-nøgle."],
]

/** Danish text for a connection check; anything unknown is shown as the server wrote it. */
export function checkText(detail: string): string {
  for (const [pattern, render] of checkPatterns) {
    const match = detail.match(pattern)
    if (match) return render(match)
  }
  return detail
}

export const setupStepText: Record<APISetupStep["id"], { title: string; todo: string; to?: string }> = {
  plex: { title: "Forbind Plex", todo: "Tryk \"Log ind med Plex\" under Forbindelser.", to: "/indstillinger/forbindelser" },
  altmount: { title: "Forbind AltMount", todo: "Tilføj AltMount, som henter filerne.", to: "/indstillinger/forbindelser" },
  indexer: { title: "Tilføj en indexer", todo: "Tilføj mindst én indexer og godkend den.", to: "/kilder" },
  movie_root: { title: "Standardmappe til film", todo: "Vælg det Plex-bibliotek, nye film skal i.", to: "/indstillinger/biblioteker" },
  series_root: { title: "Standardmappe til serier", todo: "Vælg det Plex-bibliotek, nye serier skal i.", to: "/indstillinger/biblioteker" },
  movie_profile: { title: "Standardprofil til film", todo: "Vælg, hvilken kvalitet og hvilke sprog film skal have.", to: "/indstillinger/profiler" },
  series_profile: { title: "Standardprofil til serier", todo: "Vælg, hvilken kvalitet og hvilke sprog serier skal have.", to: "/indstillinger/profiler" },
  metadata: { title: "Søgning, plakater og beskrivelser (TMDB)", todo: "Tilføj TMDB under Forbindelser. Uden TMDB eller Seerr kan du ikke søge efter film og serier.", to: "/indstillinger/forbindelser" },
  seerr: { title: "Anmodninger fra Seerr", todo: "Forbind Seerr, hvis du vil tage imod anmodninger derfra.", to: "/indstillinger/forbindelser" },
}

/** Setup steps backed by a connection that "Test forbindelser" can reach. */
export const connectionStepKinds: Record<string, string> = { plex: "plex", altmount: "altmount", metadata: "tmdb", seerr: "seerr" }

/** What a step shows under its title: the server's name for a done step, a Danish hint otherwise. */
export function setupStepDetail(step: APISetupStep): string {
  if (step.id === "plex" && !step.done && step.detail === "Save the Plex token") return "Plex er tilføjet. Gem dens token."
  if (step.id === "seerr" && !step.done && step.detail.includes(" is saved; ")) return `${step.detail.split(" is saved; ")[0]} er gemt. Media Manager tager imod anmodninger, når API'en er genstartet.`
  if (!step.done) return setupStepText[step.id]?.todo ?? step.detail
  const commissioned = step.detail.match(/^(\d+) commissioned$/)
  if (commissioned) return `${commissioned[1]} godkendt`
  return step.detail
}

const recommendedTitles: Record<APIRecommendedStep["id"], string> = {
  movie_root: "Bibliotek til film",
  series_root: "Bibliotek til serier",
  movie_profile: "Kvalitetsprofil til film",
  series_profile: "Kvalitetsprofil til serier",
}

export function recommendedTitle(step: APIRecommendedStep): string {
  return recommendedTitles[step.id] ?? step.id
}

/** Danish text for one step of "Brug anbefalet opsætning". */
export function recommendedText(step: APIRecommendedStep): string {
  const isRoot = step.id.endsWith("_root")
  const kindWord = step.id.startsWith("movie") ? "film" : "serie"
  switch (step.state) {
    case "exists":
      return `Findes allerede: ${step.name ?? ""}. Den bliver ikke ændret.`
    case "create":
    case "created": {
      const done = step.state === "created"
      if (!isRoot) return `${done ? "Oprettet" : "Opretter"} "${step.name}" som standard: kræver danske undertekster eller dansk tale.`
      const root = step.root
      if (root && step.creates_plex_library) return `${done ? "Oprettet" : "Opretter"} Plex-biblioteket "${step.name}" (dansk) med mappen ${root.plex_visible_root} og bruger det som standard; Media Manager lægger filerne i ${root.destination_root}.`
      const where = root ? ` Plex-biblioteket "${step.name}" i ${root.plex_visible_root}; Media Manager lægger filerne i ${root.destination_root}.` : ""
      return `${done ? "Oprettet" : "Opretter"} som standard.${where}`
    }
    default:
      switch (step.reason) {
        case "no_plex": return "Forbind Plex først."
        case "plex_unreachable": return "Plex svarede ikke. Test Plex-forbindelsen under Forbindelser."
        case "no_altmount": return "Forbind AltMount først, med mappen hvor Media Manager ser AltMount-filerne."
        case "no_plex_library": return `Plex har intet ${kindWord}bibliotek (4K-biblioteker tæller ikke). Opret et i Plex, eller lad Media Manager rette Plex nedenfor.`
        case "folder_not_shared": return `Media Manager kan ikke skrive i den mappe, Plex bruger (${step.detail}). Lad Media Manager rette Plex nedenfor, giv Media Manager adgang til den samme mappe, eller tilføj biblioteket selv under Biblioteker.`
        case "source_not_visible": return `Plex kan ikke se AltMount-filerne, som Media Manager ser i ${step.detail}. Lad Media Manager rette Plex nedenfor, eller giv Plex adgang til den samme mappe.`
        case "root_not_default": return `Plex-biblioteket har allerede mappen "${step.name}", men den er ikke standard. Vælg den som standard under Biblioteker.`
        case "profile_name_taken": return `Der findes allerede en profil, der hedder "${step.name}", men den er ikke standard. Vælg den som standard under Kvalitetsprofiler.`
        default: return `Kunne ikke oprettes: ${step.detail ?? step.reason ?? "ukendt fejl"}`
      }
  }
}
