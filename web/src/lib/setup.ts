import type { APILibraryRoot, APISetupStep } from "@/api/client"

/**
 * Suggests where Media Manager should write a new library folder, given the
 * folder Plex sees. Existing roots show how the two views of the disk differ
 * (Plex sees /media/Film, Media Manager writes /srv/media/Film); the most
 * common difference wins. Without existing roots the Plex path is suggested
 * unchanged, and the owner can always edit it.
 */
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
  [/^an indexer is checked by commissioning it$/, () => "En indexer testes ved at godkende den."],
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
  plex: { title: "Forbind Plex", todo: "Tilføj Plex og gem dens token.", to: "/indstillinger/forbindelser" },
  altmount: { title: "Forbind AltMount", todo: "Tilføj AltMount, som henter filerne.", to: "/indstillinger/forbindelser" },
  indexer: { title: "Tilføj en indexer", todo: "Tilføj mindst én indexer og godkend den.", to: "/kilder" },
  movie_root: { title: "Standardmappe til film", todo: "Vælg det Plex-bibliotek, nye film skal i.", to: "/indstillinger/biblioteker" },
  series_root: { title: "Standardmappe til serier", todo: "Vælg det Plex-bibliotek, nye serier skal i.", to: "/indstillinger/biblioteker" },
  movie_profile: { title: "Standardprofil til film", todo: "Vælg, hvilken kvalitet og hvilke sprog film skal have.", to: "/indstillinger/profiler" },
  series_profile: { title: "Standardprofil til serier", todo: "Vælg, hvilken kvalitet og hvilke sprog serier skal have.", to: "/indstillinger/profiler" },
  metadata: { title: "Plakater og beskrivelser", todo: "Sæt TMDB_ACCESS_TOKEN i serverens opsætning for plakater, beskrivelser og forslag." },
  seerr: { title: "Anmodninger fra Seerr", todo: "Forbind Seerr, hvis du vil tage imod anmodninger derfra.", to: "/indstillinger/forbindelser" },
}

/** What a step shows under its title: the server's name for a done step, a Danish hint otherwise. */
export function setupStepDetail(step: APISetupStep): string {
  if (step.id === "plex" && !step.done && step.detail === "Save the Plex token") return "Plex er tilføjet. Gem dens token."
  if (step.id === "seerr" && !step.done && step.detail.includes(" is saved; ")) return `${step.detail.split(" is saved; ")[0]} er gemt. Media Manager tager imod anmodninger, når API'en er genstartet.`
  if (!step.done) return setupStepText[step.id]?.todo ?? step.detail
  const commissioned = step.detail.match(/^(\d+) commissioned$/)
  if (commissioned) return `${commissioned[1]} godkendt`
  return step.detail
}
