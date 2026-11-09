import type { APIBackupRecord } from "@/api/client"

/** Configuration alone says nothing about a completed external copy. */
export function confirmedExternalCopy(record: APIBackupRecord): boolean {
  const at = record.offsite_uploaded_at
  return record.state === "verified" && record.offsite_state === "uploaded" && Boolean(at && /^\d{4}-\d{2}-\d{2}T/.test(at) && Number.isFinite(Date.parse(at)))
}

export function latestExternalCopy(records: APIBackupRecord[]): string | undefined {
  return records.filter(confirmedExternalCopy).map((record) => record.offsite_uploaded_at!).sort((a, b) => Date.parse(b) - Date.parse(a))[0]
}

export function externalCopyState(record: APIBackupRecord, configured: boolean): "confirmed" | "failed" | "pending" | "unknown" | "not_configured" {
  if (confirmedExternalCopy(record)) return "confirmed"
  if (record.offsite_state === "failed") return "failed"
  if (record.offsite_state === "uploaded") return "unknown"
  return configured ? "pending" : "not_configured"
}
