import { expect, test } from "@playwright/test"

import { mockApi } from "./mock-api"

test.beforeEach(async ({ page }) => {
  await page.route("**/*", (route) => new URL(route.request().url()).hostname === "127.0.0.1" ? route.fallback() : route.abort())
})

for (const state of ["unconfigured", "pending", "failed", "uploaded", "missing-date", "disabled-after-upload"] as const) {
  test(`backup evidence stays truthful for ${state}`, async ({ page }) => {
    const writes = await mockApi(page)
    const uploaded = state === "uploaded" || state === "disabled-after-upload"
    const configured = state !== "unconfigured" && state !== "disabled-after-upload"
    await page.route("**/api/v1/backups", (route) => route.fulfill({ json: { data: {
      configured: true, format: "mmbackup", restore_verification: "isolated temporary database and exact schema version", automatic_deletion: false, production_restore: false,
      offsite: { configured, kind: configured ? "s3" : "", prefix: "" },
      records: [{ id: "backup1", state: "verified", format: "mmbackup", attempts: 1, created_at: "2026-01-01T00:00:00Z", completed_at: "2026-01-01T01:00:00Z", ...(uploaded ? { offsite_state: "uploaded", offsite_uploaded_at: "2026-01-01T02:00:00Z" } : state === "failed" ? { offsite_state: "failed" } : state === "missing-date" ? { offsite_state: "uploaded" } : {}) }],
    } } }))
    await page.goto("/system/backup")
    await expect(page.getByText("Database kontrolleret", { exact: true })).toBeVisible()
    await expect(page.getByText("Ekstern kopiering er sat op.", { exact: true })).toHaveCount(configured ? 1 : 0)
    await expect(page.getByText("En samlet gendannelse af installation, nøgler, mapper og medier er ikke dokumenteret her.", { exact: false })).toBeVisible()
    await expect(page.getByText("Gem backup-nøglen og nøglen til gemte forbindelser", { exact: false })).toBeVisible()
    await expect(page.getByText("Ekstern kopi bekræftet", { exact: true })).toHaveCount(uploaded ? 1 : 0)
    await expect(page.getByText("Senest bekræftede eksterne kopi:", { exact: false })).toHaveCount(uploaded ? 1 : 0)
    if (!uploaded) await expect(page.getByText("Ingen gennemført ekstern kopi er dokumenteret", { exact: false })).toBeVisible()
    if (state === "failed") await expect(page.getByText("Ekstern kopiering fejlede", { exact: true })).toBeVisible()
    if (state === "pending") await expect(page.getByText("Afventer ekstern kopi", { exact: true })).toBeVisible()
    if (state === "missing-date") await expect(page.getByText("Ekstern kopi mangler fuld bekræftelse", { exact: true })).toBeVisible()
    await expect(page.getByText(/Sidste fulde gendannelse/)).toHaveCount(0)
    expect(writes).toEqual([])
  })
}

test("a deleted backup file is not shown as a checked backup", async ({ page }) => {
  await mockApi(page)
  await page.route("**/api/v1/backups", (route) => route.fulfill({ json: { data: {
    configured: true, format: "mmbackup", restore_verification: "isolated temporary database and exact schema version", automatic_deletion: false, production_restore: false,
    offsite: { configured: false, kind: "", prefix: "" },
    records: [{ id: "deleted1", state: "verified", format: "mmbackup", attempts: 1, created_at: "2026-01-01T00:00:00Z", completed_at: "2026-01-01T01:00:00Z", local_file_deleted: true }],
  } } }))
  await page.goto("/system/backup")
  await expect(page.getByText("Filen er slettet", { exact: true })).toBeVisible()
  await expect(page.getByText("Database kontrolleret", { exact: true })).toHaveCount(0)
  await expect(page.getByText("Ingen prøvegendannelse af databasen er gennemført endnu.", { exact: true })).toBeVisible()
})
