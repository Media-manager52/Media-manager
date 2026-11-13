import type { APIAltMountStatus } from "@/api/client"

/** Which AltMount runs, in plain Danish. */
export function altMountBuildText(status: Pick<APIAltMountStatus, "build" | "version">): string {
  const version = status.version ? ` (${status.version})` : ""
  switch (status.build) {
    case "media_manager":
      return `Media Managers udgave${version}`
    case "standard":
      return `Den normale AltMount${version}`
    default:
      return status.version ? `Ukendt udgave (${status.version})` : "Ukendt udgave"
  }
}

/** The Danish text for a "Skift til Media Managers AltMount" error code. */
export function altMountSwitchErrorText(code: string | undefined, message: string): string {
  switch (code) {
    case "cosmos_unauthorized":
      return "Cosmos tog ikke imod nøglen. Lav en ny under Cosmos → Settings → API (uden \"Read Only\"), og indsæt den."
    case "cosmos_unreachable":
      return "Media Manager kunne ikke nå Cosmos. Åbn Media Manager via din Cosmos-adresse og prøv igen."
    case "altmount_container_not_found":
      return "Media Manager fandt ingen AltMount i Cosmos. AltMount skal køre i samme Cosmos som Media Manager."
    case "altmount_version_newer":
      return "Din AltMount er nyere end Media Managers udgave, så den bliver ikke skiftet. Den virker fint, som den er."
    case "altmount_version_unknown":
      return "Din AltMount oplyser ikke sin version, så Media Manager skifter den ikke."
    case "altmount_folder_refused":
      return "En af AltMounts mapper har en indstilling (SELinux z/Z), som Cosmos ikke kan beholde, så AltMount bliver ikke skiftet. Intet er ændret."
    case "altmount_switch_rolled_back":
      return "Media Managers AltMount startede ikke, så din gamle AltMount er sat tilbage. Intet andet er ændret."
    case "altmount_switch_failed":
      return `Skiftet lykkedes ikke: ${message}`
    case "altmount_not_configured":
      return "Forbind AltMount under Indstillinger → Forbindelser først."
    case "altmount_switch_unavailable":
      return "Skiftet er ikke tilgængeligt på denne server."
    default:
      return message
  }
}
