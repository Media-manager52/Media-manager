import { describe, expect, it } from "vitest"

import type { APIAltMountServerStatus, APIAltMountStream, APIPlexAltMountView } from "@/api/client"
import { plexAltMountLine, plexAltMountSummary, quotaPercent, serverState, streamProgress, streamStatusLabel } from "@/lib/altmount-status"

const view = (state: APIPlexAltMountView["state"], plex_name = "Stue"): APIPlexAltMountView => ({ plex_id: "p1", plex_name, path: "/mnt/altmount", state })
const live = { id: "provider_1", host: "news.example.test", state: "active", used_connections: 0, max_connections: 10, error_count: 0, failure_reason: "", current_speed_bytes_per_sec: 0, ping_ms: 10, missing_warning: false, byte_count_24h: 0, quota_bytes: 0, quota_used: 0, quota_exceeded: false }
const server = (overrides: Partial<APIAltMountServerStatus> = {}, liveOverrides: Partial<typeof live> = {}): APIAltMountServerStatus => ({ id: "provider_1", host: "news.example.test", enabled: true, is_backup_provider: false, quota_set: false, live: { ...live, ...liveOverrides }, ...overrides })

describe("Plex's view of AltMount", () => {
  it("says what is wrong and how to fix it", () => {
    expect(plexAltMountLine(view("ok"))).toEqual({ tone: "good", label: "Stue ser AltMounts filer" })
    const missing = plexAltMountLine(view("missing"))
    expect(missing.tone).toBe("bad")
    expect(missing.label).toBe("Stue mangler AltMounts filer i /mnt/altmount")
    expect(missing.detail).toContain("rslave")
    expect(plexAltMountLine(view("unreadable")).tone).toBe("bad")
    expect(plexAltMountLine(view("empty")).tone).toBe("neutral")
    expect(plexAltMountLine(view("unknown")).tone).toBe("neutral")
  })

  it("summarises for the overview only when something was checked", () => {
    expect(plexAltMountSummary(undefined)).toBeUndefined()
    expect(plexAltMountSummary([])).toBeUndefined()
    expect(plexAltMountSummary([view("ok"), view("ok", "Kontor")])).toEqual({ ok: true, detail: "Ja" })
    expect(plexAltMountSummary([view("ok"), view("missing", "Kontor")])).toEqual({ ok: false, detail: "Kontor kan ikke se filerne" })
    expect(plexAltMountSummary([view("unreadable"), view("missing", "Kontor")])).toEqual({ ok: false, detail: "2 Plex-servere kan ikke se filerne" })
    expect(plexAltMountSummary([view("empty")])).toEqual({ ok: true, detail: "Endnu ikke kontrolleret" })
  })
})

describe("AltMount servers and streams", () => {
  it("shows quota use only for servers with a quota", () => {
    expect(quotaPercent(server())).toBeUndefined()
    expect(quotaPercent(server({}, { quota_bytes: 1000, quota_used: 400 }))).toBe(40)
    expect(quotaPercent(server({}, { quota_bytes: 1000, quota_used: 5000 }))).toBe(100)
    expect(quotaPercent(server({ live: undefined }))).toBeUndefined()
  })

  it("names a server's state, worst first", () => {
    expect(serverState(server({ enabled: false }))).toEqual({ tone: "neutral", label: "Slået fra" })
    expect(serverState(server({ live: undefined }))).toEqual({ tone: "neutral", label: "Ikke i brug" })
    expect(serverState(server({}, { quota_exceeded: true, missing_warning: true }))).toEqual({ tone: "bad", label: "Kvoten er brugt op" })
    expect(serverState(server({}, { failure_reason: "auth failed" })).tone).toBe("bad")
    expect(serverState(server({}, { missing_warning: true })).tone).toBe("warn")
    expect(serverState(server({}, { used_connections: 3 }))).toEqual({ tone: "good", label: "Henter" })
    expect(serverState(server())).toEqual({ tone: "good", label: "Klar" })
  })

  it("shows how far a stream has come", () => {
    const stream: APIAltMountStream = { file: "a.mkv", source: "FUSE", status: "Streaming", started_at: "", last_activity: "", total_size: 2000, current_offset: 500, bytes_per_second: 1 }
    expect(streamProgress(stream)).toBe(25)
    expect(streamProgress({ ...stream, total_size: 0 })).toBe(0)
    expect(streamStatusLabel("Streaming")).toBe("Afspiller")
    expect(streamStatusLabel("Stalled")).toBe("Står stille")
    expect(streamStatusLabel("")).toBe("Ukendt")
  })
})
