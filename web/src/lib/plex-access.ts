import type { APIPlexAccessChange, APIRecommendedReport } from "@/api/client"

const folderWord: Record<APIPlexAccessChange["purpose"], string> = {
  altmount: "AltMounts filer",
  movies: "Media Managers filmmappe",
  tv: "Media Managers seriemappe",
}

/** One change "Ret Plex" makes, in plain Danish. */
export function plexAccessChangeText(change: APIPlexAccessChange): string {
  const folder = folderWord[change.purpose] ?? change.path
  switch (change.kind) {
    case "add_folder":
      return change.purpose === "altmount"
        ? `Plex får ${folder} i ${change.path}, og følger med, når AltMount genstarter.`
        : `Plex får ${folder} i ${change.path} (kun læse).`
    case "set_propagation":
      return `Plex' mappe med ${folder} (${change.path}) følger nu med, når AltMount genstarter.`
    case "add_library_path":
      return `Plex-biblioteket "${change.library}" får mappen ${change.path}.`
    case "create_library":
      return `Plex-biblioteket "${change.library}" oprettes med mappen ${change.path} (dansk).`
  }
}

/** Reasons "Ret Plex" can solve. */
const fixableReasons = new Set(["source_not_visible", "folder_not_shared", "no_plex_library"])

/** Whether the recommended setup is blocked by something "Ret Plex" can fix. */
export function plexAccessCanHelp(report: APIRecommendedReport | undefined): boolean {
  return report?.steps.some((step) => step.state === "blocked" && fixableReasons.has(step.reason ?? "")) ?? false
}

/** The Danish text for a "Ret Plex" error code. */
export function plexAccessErrorText(code: string | undefined, message: string): string {
  switch (code) {
    case "cosmos_unauthorized":
      return "Cosmos tog ikke imod nøglen. Lav en ny under Cosmos → Settings → API (uden \"Read Only\"), og indsæt den."
    case "cosmos_unreachable":
      return "Media Manager kunne ikke nå Cosmos. Åbn Media Manager via din Cosmos-adresse og prøv igen."
    case "plex_container_not_found":
      return "Media Manager fandt ingen Plex i Cosmos. Plex skal køre i samme Cosmos som Media Manager."
    case "no_plex":
      return "Log ind med Plex først."
    case "plex_access_unavailable":
      return "\"Ret Plex\" er ikke tilgængelig på denne server."
    default:
      return message
  }
}
