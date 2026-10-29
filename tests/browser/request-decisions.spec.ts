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

test("waiting requests show admission and only their recorded next search", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  const items = [
    { ...pending, id: `${seerrInstance}:781`, request_id: "781", title: "Added waiting movie", seerr_state: "2", lifecycle: "waiting", workflow_count: 1, intake_outcome: "media_manager_added", waiting_reason: "no eligible release yet", next_search_at: new Date(Date.now() + 2 * 60 * 60 * 1000).toISOString() },
    { ...pending, id: `${seerrInstance}:782`, request_id: "782", title: "Release needs owner approval", seerr_state: "2", lifecycle: "needs_attention", current_state: "waiting", workflow_count: 1, attention_count: 1, attention_detail: "A new eligible release needs approval for another round." },
  ]
  await page.route("**/api/v1/requests**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (route.request().method() === "GET" && path === "/api/v1/requests") {
      await route.fulfill({ status: 200, json: { data: items } })
      return
    }
    const item = items.find((entry) => path === `/api/v1/requests/${encodeURIComponent(entry.id)}`)
    if (route.request().method() === "GET" && item) {
      await route.fulfill({ status: 200, json: { data: { request: item, workflows: [], seasons: [], files: [] } } })
      return
    }
    await route.fallback()
  })
  await page.goto("/console/requests")
  const row = page.getByRole("row").filter({ hasText: "Added waiting movie" })
  await expect(row.getByText("Added to Media Manager. No eligible release matched the latest search.", { exact: true })).toBeVisible()
  await expect(row.getByText(/Auto-retry in 2h/)).toBeVisible()
  await row.getByRole("button", { name: "Added waiting movie", exact: true }).click()
  const inspector = page.locator("section").filter({ has: page.getByRole("heading", { name: "Request detail", exact: true }) })
  await expect(inspector.getByText("1 added · 0 Plex confirmed", { exact: true })).toBeVisible()
  await expect(inspector.getByText("Next search", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: /Needs attention/ }).click()
  await expect(page.getByRole("row").filter({ hasText: "Added waiting movie" })).toHaveCount(0)
  await expect(page.getByRole("row").filter({ hasText: "Release needs owner approval" })).toBeVisible()
  expect(recorder.unexpected).toEqual([])
})

test("request messages follow the actual lifecycle instead of always warning", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  const items = [
    { ...pending, id: `${seerrInstance}:778`, request_id: "778", title: "Completed status message", seerr_state: "5", lifecycle: "available", intake_error: "Seerr reports the request completed" },
    { ...pending, id: `${seerrInstance}:779`, request_id: "779", title: "Scheduled retry message", seerr_state: "2", lifecycle: "waiting", attention_detail: "A paced retry is scheduled." },
    { ...pending, id: `${seerrInstance}:780`, request_id: "780", title: "Real attention message", seerr_state: "2", lifecycle: "needs_attention", attention_detail: "Required Danish subtitles were not verified." },
  ]
  await page.route("**/api/v1/requests**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (route.request().method() === "GET" && path === "/api/v1/requests") {
      await route.fulfill({ status: 200, json: { data: items } })
      return
    }
    const item = items.find((entry) => path === `/api/v1/requests/${encodeURIComponent(entry.id)}`)
    if (route.request().method() === "GET" && item) {
      await route.fulfill({ status: 200, json: { data: { request: item, workflows: [], seasons: [], files: [] } } })
      return
    }
    await route.fallback()
  })
  await page.goto("/console/requests")
  const inspector = page.locator("section").filter({ has: page.getByRole("heading", { name: "Request detail", exact: true }) })
  await page.getByRole("button", { name: "Completed status message", exact: true }).click()
  await expect(inspector.getByText("Seerr reports the request completed", { exact: true })).toBeVisible()
  await expect(inspector.getByText(/Needs attention/i)).toHaveCount(0)
  await expect(page.getByRole("row").filter({ hasText: "Completed status message" }).getByText("Seerr reports the request completed", { exact: true })).toHaveClass(/text-fg-subtle/)
  await page.getByRole("button", { name: "Scheduled retry message", exact: true }).click()
  await expect(inspector.getByText("A paced retry is scheduled.", { exact: true })).toBeVisible()
  await expect(inspector.getByText(/Needs attention/i)).toHaveCount(0)
  await page.getByRole("button", { name: "Real attention message", exact: true }).click()
  await expect(inspector.getByText("Required Danish subtitles were not verified.", { exact: true })).toBeVisible()
  await expect(inspector.getByText("Needs Attention", { exact: true })).toBeVisible()
  expect(recorder.unexpected).toEqual([])
})

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

  await page.goto("/console/requests")
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

test("deciding a request from the Overview stays on the Overview", async ({ page }) => {
  const recorder = await installOwnerAPI(page, baseURL)
  const decisions: string[] = []
  await page.route("**/api/v1/requests**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (request.method() === "POST" && /\/api\/v1\/requests\/[^/]+\/(approve|decline)$/.test(path)) {
      decisions.push(path.endsWith("/approve") ? "approved" : "declined")
      await route.fulfill({ status: 202, json: { data: { status: "approved", request_id: "777", seerr_status: 2 } } })
      return
    }
    if (request.method() === "GET" && path === "/api/v1/requests") {
      await route.fulfill({ status: 200, json: { data: [pending] } })
      return
    }
    await route.fallback()
  })

  await page.goto("/console")
  await page.getByRole("button", { name: "Decline Pending Browser Movie" }).click()
  const dialog = page.getByRole("alertdialog", { name: "Decline Pending Browser Movie?" })
  await expect(dialog).toBeVisible()
  await dialog.getByRole("button", { name: "Keep pending" }).click()
  await expect(page).toHaveURL(`${baseURL}/console`)

  await page.getByRole("button", { name: "Approve Pending Browser Movie" }).click()
  await expect.poll(() => decisions).toEqual(["approved"])
  await expect(page).toHaveURL(`${baseURL}/console`)
  await expect(page.getByRole("link", { name: "Pending Browser Movie" })).toHaveAttribute("href", "/console/requests")
  expect(recorder.unexpected).toEqual([])
})
