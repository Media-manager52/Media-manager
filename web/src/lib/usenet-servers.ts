import type { APIUsenetServer, APIUsenetServerInput } from "@/api/client"

/** What the owner types for one Usenet server. Numbers stay text until saved. */
export type UsenetServerDraft = {
  name: string
  host: string
  port: string
  username: string
  password: string
  connections: string
  tls: boolean
  insecureTLS: boolean
  backup: boolean
  quotaGB: string
  quotaHours: string
  expires: string
}

export const newUsenetServerDraft: UsenetServerDraft = {
  name: "", host: "", port: "563", username: "", password: "", connections: "10",
  tls: true, insecureTLS: false, backup: false, quotaGB: "", quotaHours: "", expires: "",
}

const gigabyte = 1_000_000_000
const hostPattern = /^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/

export function draftFromServer(server: APIUsenetServer): UsenetServerDraft {
  return {
    name: server.name ?? "",
    host: server.host,
    port: String(server.port),
    username: server.username,
    password: "",
    connections: String(server.max_connections),
    tls: server.tls,
    insecureTLS: server.insecure_tls,
    backup: server.is_backup_provider,
    quotaGB: server.quota_bytes > 0 ? String(Math.round((server.quota_bytes / gigabyte) * 100) / 100) : "",
    quotaHours: server.quota_period_hours > 0 ? String(server.quota_period_hours) : "",
    expires: server.account_expiration_date ?? "",
  }
}

/**
 * Accepts what providers write in their welcome mail: "news.example.com",
 * "news.example.com:563" or "nntps://news.example.com". Returns the host and,
 * when one was typed, the port and whether it implies TLS.
 */
export function splitServerAddress(value: string): { host: string; port?: string; tls?: boolean } {
  let host = value.trim()
  let tls: boolean | undefined
  const scheme = host.match(/^(nntps?|snews|news):\/\//i)
  if (scheme) {
    tls = /^(nntps|snews)$/i.test(scheme[1])
    host = host.slice(scheme[0].length)
  }
  host = host.replace(/\/+$/, "")
  const withPort = host.match(/^(.*):(\d{1,5})$/)
  if (withPort) return { host: withPort[1], port: withPort[2], tls: tls ?? (withPort[2] === "563" || withPort[2] === "443" ? true : withPort[2] === "119" ? false : undefined) }
  return { host, tls }
}

/** The first problem with a draft in plain Danish, or "" when it can be saved. */
export function usenetDraftProblem(draft: UsenetServerDraft, creating: boolean): string {
  const host = draft.host.trim()
  const port = Number(draft.port)
  const connections = Number(draft.connections)
  if (!host) return "Skriv serverens adresse, fx news.eksempel.dk."
  if (/^[a-z]+:\/\//i.test(host) || host.includes("/")) return "Skriv kun serverens navn, fx news.eksempel.dk, uden http:// eller /."
  if (!hostPattern.test(host)) return "Adressen må kun indeholde bogstaver, tal, punktum og bindestreg."
  if (!Number.isInteger(port) || port < 1 || port > 65535) return "Porten skal være et tal mellem 1 og 65535. Den står i mailen fra din udbyder (ofte 563)."
  if (!draft.username.trim()) return "Skriv dit brugernavn hos udbyderen."
  if (creating && !draft.password) return "Skriv din adgangskode hos udbyderen."
  if (!Number.isInteger(connections) || connections < 1 || connections > 500) return "Antal forbindelser skal være et tal mellem 1 og 500."
  if (draft.quotaGB.trim() && !(Number(draft.quotaGB.replace(",", ".")) >= 0)) return "Kvoten skal være et tal i GB."
  if (draft.quotaHours.trim() && !(Number.isInteger(Number(draft.quotaHours)) && Number(draft.quotaHours) >= 0)) return "Kvoteperioden skal være et helt antal timer."
  return ""
}

function quotaBytes(draft: UsenetServerDraft): number {
  const value = Number(draft.quotaGB.replace(",", "."))
  return draft.quotaGB.trim() && value > 0 ? Math.round(value * gigabyte) : 0
}

/** Every field of a new server, ready for the API. */
export function draftToCreate(draft: UsenetServerDraft): APIUsenetServerInput {
  return {
    name: draft.name.trim() || undefined,
    host: draft.host.trim(),
    port: Number(draft.port),
    username: draft.username.trim(),
    password: draft.password,
    max_connections: Number(draft.connections),
    tls: draft.tls,
    insecure_tls: draft.tls && draft.insecureTLS,
    is_backup_provider: draft.backup,
    enabled: true,
    quota_bytes: quotaBytes(draft),
    quota_period_hours: draft.quotaHours.trim() ? Number(draft.quotaHours) : 0,
    account_expiration_date: draft.expires || undefined,
  }
}

/** Only what changed; no password unless a new one was typed (an empty one keeps the stored password). */
export function draftToPatch(original: UsenetServerDraft, draft: UsenetServerDraft): APIUsenetServerInput {
  const next = draftToCreate(draft)
  const before = draftToCreate(original)
  const patch: APIUsenetServerInput = {}
  if ((next.name ?? "") !== (before.name ?? "")) patch.name = next.name ?? ""
  if (next.host !== before.host) patch.host = next.host
  if (next.port !== before.port) patch.port = next.port
  if (next.username !== before.username) patch.username = next.username
  if (draft.password) patch.password = draft.password
  if (next.max_connections !== before.max_connections) patch.max_connections = next.max_connections
  if (next.tls !== before.tls) patch.tls = next.tls
  if (next.insecure_tls !== before.insecure_tls) patch.insecure_tls = next.insecure_tls
  if (next.is_backup_provider !== before.is_backup_provider) patch.is_backup_provider = next.is_backup_provider
  if (next.quota_bytes !== before.quota_bytes) patch.quota_bytes = next.quota_bytes
  if (next.quota_period_hours !== before.quota_period_hours) patch.quota_period_hours = next.quota_period_hours
  if ((next.account_expiration_date ?? "") !== (before.account_expiration_date ?? "")) patch.account_expiration_date = next.account_expiration_date ?? ""
  return patch
}

/** The server IDs with one moved up (-1) or down (+1); unchanged at either end. */
export function moveServer(ids: string[], index: number, delta: -1 | 1): string[] {
  const target = index + delta
  if (index < 0 || index >= ids.length || target < 0 || target >= ids.length) return ids
  const next = [...ids]
  ;[next[index], next[target]] = [next[target], next[index]]
  return next
}

/** True when this server is the only one that is turned on. */
export function isLastActive(servers: APIUsenetServer[], server: APIUsenetServer): boolean {
  return server.enabled && servers.filter((candidate) => candidate.enabled).length === 1
}
