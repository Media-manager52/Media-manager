import { expect, test } from "@playwright/test"

import { installOwnerAPI } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"
const seerrInstance = "c2c2c2c2-c2c2-4c2c-8c2c-c2c2c2c2c2c2"

const pending = {
  id: `${seerrInstance}:777`, request_id: "777", seerr_instance_id: seerrInstance, seerr_instance_name: "Seerr",
  media_kind: "movie", title: "Pending Browser Movie", year: 2025, requester: "Family",
  requested_at: "2026-09-29T10:00:00Z", updated_at: "2026-09-29T10:00:00Z",
  seerr_state: "1", intake_outcome: "observed_not_approved", lifecycle: "not_approved",
  workflow_count: 0, confirmed_count: 0, attention_count: 0, season_count: 0, available_seasons: 0,
}

test("owner approves a pending Seerr request and declining asks first", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  const decisions: string[] = []
  await page.route("**/api/v1/requests**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (request.method() === "POST" && /\/api\/v1\/requests\/[^/]+\/(approve|decline)$/.test(path)) {
      const decision = path.endsWith("/approve") ? "approved" : "declined"
      decisions.push(`${decodeURIComponent(path.split("/")[4])} ${decision}`)
      await route.fulfill({ status: 202, json: { data: { status: decision, request_id: "777", seerr_status: decision === "approved" ? 2 : 3 } } })
      return
    }
    if (request.method() === "GET" && path === "/api/v1/requests") {
      await route.fulfill({ status: 200, json: { data: [pending] } })
      return
    }
    if (request.method() === "GET" && path.startsWith("/api/v1/requests/")) {
      await route.fulfill({ status: 200, json: { data: { request: pending, workflows: [], seasons: [], files: [] } } })
      return
    }
    await route.fallback()
  })

  await page.goto("/requests")
  const row = page.getByRole("row").filter({ hasText: "Pending Browser Movie" })
  await row.getByRole("button", { name: "Approve Pending Browser Movie" }).click()
  await expect.poll(() => decisions).toEqual([`${seerrInstance}:777 approved`])
  await expect(page.getByText("Approved in Seerr · intake will pick it up")).toBeVisible()

  await row.getByRole("button", { name: "Decline Pending Browser Movie" }).click()
  const dialog = page.getByRole("alertdialog", { name: "Decline Pending Browser Movie?" })
  await expect(dialog).toBeVisible()
  await dialog.getByRole("button", { name: "Keep pending" }).click()
  expect(decisions).toHaveLength(1)

  await row.getByRole("button", { name: "Decline Pending Browser Movie" }).click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Decline in Seerr" }).click()
  await expect.poll(() => decisions).toEqual([`${seerrInstance}:777 approved`, `${seerrInstance}:777 declined`])
  expect(recorder.unexpected).toEqual([])
})
