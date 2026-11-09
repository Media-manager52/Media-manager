import { expect, it } from "vitest"

import type { APIBackupRecord } from "@/api/client"
import { confirmedExternalCopy, externalCopyState, latestExternalCopy } from "./backup-evidence"

const local: APIBackupRecord = { id: "backup", state: "verified", format: "mmbackup", attempts: 1, created_at: "2026-01-01T00:00:00Z" }

it("requires completed copy evidence rather than configured storage or a pending job", () => {
  expect(confirmedExternalCopy(local)).toBe(false)
  expect(externalCopyState(local, true)).toBe("pending")
  expect(externalCopyState(local, false)).toBe("not_configured")
  for (const at of [undefined, "", "not-a-date", "123"]) {
    const record = { ...local, offsite_state: "uploaded" as const, offsite_uploaded_at: at }
    expect(confirmedExternalCopy(record)).toBe(false)
    expect(externalCopyState(record, true)).toBe("unknown")
  }
  expect(confirmedExternalCopy({ ...local, state: "running", offsite_state: "uploaded", offsite_uploaded_at: local.created_at })).toBe(false)
  expect(externalCopyState({ ...local, offsite_state: "failed", offsite_uploaded_at: local.created_at }, true)).toBe("failed")
})

it("keeps historical success evidence even when copying is now disabled and ignores later failures", () => {
  const old = { ...local, offsite_state: "uploaded" as const, offsite_uploaded_at: "2026-01-02T00:00:00Z" }
  const newer = { ...old, id: "new", offsite_uploaded_at: "2026-01-03T00:00:00Z" }
  const failed = { ...local, offsite_state: "failed" as const, offsite_uploaded_at: "2026-01-04T00:00:00Z" }
  expect(latestExternalCopy([failed, old, newer])).toBe(newer.offsite_uploaded_at)
  expect(externalCopyState(newer, false)).toBe("confirmed")
  expect(latestExternalCopy([failed, local])).toBeUndefined()
})
