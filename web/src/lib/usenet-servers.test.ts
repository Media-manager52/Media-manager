import { describe, expect, it } from "vitest"

import type { APIUsenetServer } from "@/api/client"
import { draftFromServer, draftToCreate, draftToPatch, isLastActive, moveServer, newUsenetServerDraft, splitServerAddress, usenetDraftProblem } from "@/lib/usenet-servers"

const server: APIUsenetServer = {
  id: "provider_1", name: "Udbyder", host: "news.example.test", port: 563, username: "me", max_connections: 20, tls: true, insecure_tls: false,
  password_set: true, enabled: true, is_backup_provider: false, last_rtt_ms: 40, quota_bytes: 500_000_000_000, quota_period_hours: 720, account_expiration_date: "2027-01-31",
}

describe("Usenet server forms", () => {
  it("reads the address forms providers write in their welcome mail", () => {
    expect(splitServerAddress("news.example.test")).toEqual({ host: "news.example.test", tls: undefined })
    expect(splitServerAddress(" news.example.test:563 ")).toEqual({ host: "news.example.test", port: "563", tls: true })
    expect(splitServerAddress("news.example.test:119")).toEqual({ host: "news.example.test", port: "119", tls: false })
    expect(splitServerAddress("nntps://news.example.test")).toEqual({ host: "news.example.test", tls: true })
    expect(splitServerAddress("nntp://news.example.test:8000/")).toEqual({ host: "news.example.test", port: "8000", tls: false })
  })

  it("names the first problem in Danish and needs a password only for a new server", () => {
    const filled = { ...newUsenetServerDraft, host: "news.example.test", username: "me", password: "pw" }
    expect(usenetDraftProblem(filled, true)).toBe("")
    expect(usenetDraftProblem({ ...filled, host: "" }, true)).toMatch(/^Skriv serverens adresse/)
    expect(usenetDraftProblem({ ...filled, host: "https://news.example.test" }, true)).toMatch(/uden http/)
    expect(usenetDraftProblem({ ...filled, port: "70000" }, true)).toMatch(/^Porten/)
    expect(usenetDraftProblem({ ...filled, connections: "0" }, true)).toMatch(/forbindelser/)
    expect(usenetDraftProblem({ ...filled, password: "" }, true)).toMatch(/adgangskode/)
    expect(usenetDraftProblem({ ...filled, password: "" }, false)).toBe("")
  })

  it("sends every field for a new server, with quota in bytes", () => {
    const input = draftToCreate({ ...newUsenetServerDraft, host: " news.example.test ", username: "me", password: "pw", quotaGB: "1,5", quotaHours: "720" })
    expect(input).toMatchObject({ host: "news.example.test", port: 563, username: "me", password: "pw", max_connections: 10, tls: true, insecure_tls: false, enabled: true, quota_bytes: 1_500_000_000, quota_period_hours: 720 })
  })

  it("sends only what changed and never an empty password", () => {
    const original = draftFromServer(server)
    expect(original.password).toBe("")
    expect(original.quotaGB).toBe("500")
    expect(draftToPatch(original, original)).toEqual({})
    expect(draftToPatch(original, { ...original, connections: "30" })).toEqual({ max_connections: 30 })
    expect(draftToPatch(original, { ...original, password: "new" })).toEqual({ password: "new" })
    expect(draftToPatch(original, { ...original, tls: false })).toEqual({ tls: false })
    expect(draftToPatch(original, { ...original, expires: "" })).toEqual({ account_expiration_date: "" })
  })

  it("moves a server one place and knows the last active one", () => {
    expect(moveServer(["a", "b", "c"], 2, -1)).toEqual(["a", "c", "b"])
    expect(moveServer(["a", "b", "c"], 0, -1)).toEqual(["a", "b", "c"])
    const backup = { ...server, id: "provider_2", enabled: false }
    expect(isLastActive([server, backup], server)).toBe(true)
    expect(isLastActive([server, backup], backup)).toBe(false)
    expect(isLastActive([server, { ...backup, enabled: true }], server)).toBe(false)
  })
})
