import type { APIAltMountServerStatus, APIAltMountStream, APIPlexAltMountView } from "@/api/client"
import type { Tone } from "@/lib/tone"

/** Where the guide explains how Plex is given AltMount's folder. */
export const plexAltMountGuide = "https://github.com/Media-manager52/Media-manager/blob/main/deploy/cosmos/README.md#altmount"

export type PlexAltMountLine = { tone: Tone; label: string; detail?: string }

/** One Plex server's view of AltMount in plain Danish, with the fix when it is wrong. */
export function plexAltMountLine(view: APIPlexAltMountView): PlexAltMountLine {
  switch (view.state) {
    case "ok":
      return { tone: "good", label: `${view.plex_name} ser AltMounts filer` }
    case "empty":
      return { tone: "neutral", label: "AltMount har ingen filer endnu", detail: "Kontrollen kører, når den første titel er hentet." }
    case "unknown":
      return { tone: "neutral", label: "Kan ikke kontrolleres", detail: "Media Manager kan ikke selv læse AltMount-drevet lige nu (se AltMount-drev)." }
    case "unreadable":
      return { tone: "bad", label: `${view.plex_name} kan ikke åbne ${view.path}`, detail: "Plex kan ikke se AltMounts filer, så nye titler kan ikke afspilles. Brug \"Lad Media Manager rette Plex\" nedenfor, eller giv selv Plex mappen med AltMounts filer (med rslave)." }
    case "missing":
      return {
        tone: "bad",
        label: `${view.plex_name} mangler AltMounts filer i ${view.path}`,
        detail: "Plex har sandsynligvis mistet drevet efter en genstart af AltMount. Brug \"Lad Media Manager rette Plex\" nedenfor: Plex får mappen med rslave, så det ikke sker igen.",
      }
  }
}

/** The overview's one-line answer: does every Plex server see AltMount? Undefined when nothing is checked. */
export function plexAltMountSummary(views: APIPlexAltMountView[] | null | undefined): { ok: boolean; detail: string } | undefined {
  if (!views || views.length === 0) return undefined
  const wrong = views.filter((view) => view.state === "missing" || view.state === "unreadable")
  if (wrong.length > 0) return { ok: false, detail: wrong.length === 1 ? `${wrong[0].plex_name} kan ikke se filerne` : `${wrong.length} Plex-servere kan ikke se filerne` }
  if (views.every((view) => view.state === "ok")) return { ok: true, detail: "Ja" }
  return { ok: true, detail: "Endnu ikke kontrolleret" }
}

/** How full a server's download quota is, 0-100, or undefined without a quota. */
export function quotaPercent(server: APIAltMountServerStatus): number | undefined {
  const live = server.live
  if (!live || !live.quota_bytes || live.quota_bytes <= 0) return undefined
  return Math.min(100, Math.max(0, Math.round((live.quota_used / live.quota_bytes) * 100)))
}

/** A server's state as a colour and a word. */
export function serverState(server: APIAltMountServerStatus): { tone: Tone; label: string } {
  if (!server.enabled) return { tone: "neutral", label: "Slået fra" }
  const live = server.live
  if (!live) return { tone: "neutral", label: "Ikke i brug" }
  if (live.quota_exceeded) return { tone: "bad", label: "Kvoten er brugt op" }
  if (live.failure_reason || live.state === "offline" || live.state === "failed") return { tone: "bad", label: "Fejler" }
  if (live.missing_warning) return { tone: "warn", label: "Mangler mange artikler" }
  if (live.used_connections > 0) return { tone: "good", label: "Henter" }
  return { tone: "good", label: "Klar" }
}

/** How far into the file a stream is, 0-100. */
export function streamProgress(stream: APIAltMountStream): number {
  if (!stream.total_size || stream.total_size <= 0) return 0
  return Math.min(100, Math.max(0, Math.round((stream.current_offset / stream.total_size) * 100)))
}

/** The Danish word for what a stream is doing. */
export function streamStatusLabel(status: string): string {
  switch (status.toLowerCase()) {
    case "streaming":
      return "Afspiller"
    case "buffering":
      return "Henter"
    case "stalled":
      return "Står stille"
    default:
      return status || "Ukendt"
  }
}
