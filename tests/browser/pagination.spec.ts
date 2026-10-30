import { expect, test } from "@playwright/test"

import { installOwnerAPI } from "./fixtures/owner-api"

const baseURL = "http://127.0.0.1:4174"

test("workflow page two loads without duplicates and keeps stable row identity", async ({ page }) => {
  await installOwnerAPI(page, baseURL)
  const workflows = Array.from({ length: 205 }, (_, index) => {
    const number = String(index + 1).padStart(3, "0")
    return {
      id: `90000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
      media_id: `80000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
      media_kind: "movie",
      title: `Paged Workflow ${number}`,
      year: 2024,
      variant: "default",
      state: "waiting",
      revision: 1,
      owner_action_required: false,
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T00:00:00Z",
    }
  })
  await page.route("**/api/v1/workflows*", async (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("attention") === "true") {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    const limit = Number(url.searchParams.get("limit") ?? "300")
    const offset = Number(url.searchParams.get("offset") ?? "0")
    await route.fulfill({ status: 200, json: { data: workflows.slice(offset, offset + limit) } })
  })

  await page.goto(`${baseURL}/activity/queue`)
  await expect(page.getByText("Paged Workflow 001", { exact: true })).toBeVisible()
  await expect(page.getByText("Paged Workflow 201", { exact: true })).toHaveCount(0)
  await expect(page.getByText("Paged Workflow 001", { exact: true })).toHaveCount(1)

  await page.getByRole("button", { name: "Load more" }).click()
  await expect(page.getByText("Paged Workflow 201", { exact: true })).toBeVisible()
  await expect(page.getByText("Paged Workflow 001", { exact: true })).toHaveCount(1)
  await expect(page.getByText("Paged Workflow 205", { exact: true })).toBeVisible()
})

test("refresh invalidates an in-flight load-more page instead of merging both", async ({ page }) => {
  await installOwnerAPI(page, baseURL)
  const workflows = Array.from({ length: 205 }, (_, index) => {
    const number = String(index + 1).padStart(3, "0")
    return {
      id: `91000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
      media_id: `81000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
      media_kind: "movie",
      title: `Race Workflow ${number}`,
      year: 2024,
      variant: "default",
      state: "waiting",
      revision: 1,
      owner_action_required: false,
      created_at: "2026-09-01T00:00:00Z",
      updated_at: "2026-09-01T00:00:00Z",
    }
  })
  let delayPageTwo = false
  await page.route("**/api/v1/workflows*", async (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("attention") === "true") {
      await route.fulfill({ status: 200, json: { data: [] } })
      return
    }
    const limit = Number(url.searchParams.get("limit") ?? "300")
    const offset = Number(url.searchParams.get("offset") ?? "0")
    if (offset >= 200 && delayPageTwo) await new Promise((resolve) => setTimeout(resolve, 1200))
    await route.fulfill({ status: 200, json: { data: workflows.slice(offset, offset + limit) } })
  })

  await page.goto(`${baseURL}/activity/queue`)
  await expect(page.getByText("Race Workflow 001", { exact: true })).toBeVisible()
  delayPageTwo = true
  await page.getByRole("button", { name: "Load more" }).click()
  await page.getByRole("button", { name: "Refresh", exact: true }).click()
  await page.waitForTimeout(1600)
  await expect(page.getByText("Race Workflow 001", { exact: true })).toHaveCount(1)
  await expect(page.getByText("Race Workflow 201", { exact: true })).toHaveCount(0)
})

test("Wanted loads workflow context for rows outside the first 300 workflows", async ({ page }) => {
  await installOwnerAPI(page, baseURL)
  const attentionID = "92000000-0000-4000-8000-000000000001"
  const wanted = Array.from({ length: 150 }, (_, index) => {
    const number = String(index + 1).padStart(3, "0")
    return {
      workflow_id: index === 0 ? attentionID : `92000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
      media_id: `82000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
      media_kind: "movie",
      title: index === 0 ? "Old Attention Movie" : `Waiting Movie ${number}`,
      year: 2024,
      variant: "default",
      gap: "missing",
      current_state: index === 0 ? "needs_attention" : "waiting",
      target_profile: "1080p",
      upgrades_enabled: true,
      attention_detail: index === 0 ? "Owner must choose a replacement" : undefined,
      owner_action_required: index === 0,
      updated_at: "2026-09-01T00:00:00Z",
    }
  })
  const terminal = Array.from({ length: 300 }, (_, index) => ({
    id: `93000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
    media_id: `83000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
    media_kind: "movie",
    title: `Newer Terminal ${String(index + 1).padStart(3, "0")}`,
    year: 2024,
    variant: "default",
    state: "plex_confirmed",
    revision: 1,
    owner_action_required: false,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-30T00:00:00Z",
  }))
  const detail = {
    id: attentionID,
    media_id: wanted[0].media_id,
    media_kind: "movie",
    title: "Old Attention Movie",
    year: 2024,
    variant: "default",
    state: "needs_attention",
    revision: 4,
    owner_action_required: true,
    attention_code: "release_not_found",
    attention_detail: "Owner must choose a replacement",
    ready_release_title: "Old.Attention.Movie.2024.1080p.WEB-DL",
    ready_indexer_name: "NZB Example",
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-01T00:00:00Z",
  }
  await page.route("**/api/v1/wanted*", async (route) => {
    const url = new URL(route.request().url())
    const offset = Number(url.searchParams.get("offset") ?? "0")
    const limit = Number(url.searchParams.get("limit") ?? "100")
    await route.fulfill({ status: 200, json: { data: wanted.slice(offset, offset + limit) } })
  })
  await page.route("**/api/v1/workflows*", async (route) => {
    const url = new URL(route.request().url())
    if (/^\/api\/v1\/workflows\/[0-9a-f-]{36}$/.test(url.pathname)) return
    if (url.searchParams.get("attention") === "true") return route.fulfill({ status: 200, json: { data: [detail] } })
    const offset = Number(url.searchParams.get("offset") ?? "0")
    const limit = Number(url.searchParams.get("limit") ?? "300")
    await route.fulfill({ status: 200, json: { data: terminal.slice(offset, offset + limit) } })
  })
  await page.route("**/api/v1/workflows/*", async (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-1)
    await route.fulfill({ status: 200, json: { data: id === attentionID ? detail : { ...detail, id, state: "waiting", owner_action_required: false, ready_release_title: undefined, attention_code: "", attention_detail: "" } } })
  })

  await page.goto(`${baseURL}/wanted`)
  await expect(page.getByRole("heading", { name: /Old Attention Movie/ })).toBeVisible()
  await expect(page.getByText("Candidate:")).toContainText("Old.Attention.Movie.2024.1080p.WEB-DL")
  await expect(page.getByText("Waiting Movie 100", { exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "Load more" }).click()
  await page.getByRole("tab", { name: /Missing/ }).click()
  await expect(page.getByRole("row", { name: /Waiting Movie 150/ })).toBeVisible()
})

test("failed first-page refresh discards the old page and refuses load more", async ({ page }) => {
  await installOwnerAPI(page, baseURL)
  const workflows = Array.from({ length: 205 }, (_, index) => ({
    id: `94000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
    media_id: `84000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
    media_kind: "movie",
    title: `Refresh Failure ${String(index + 1).padStart(3, "0")}`,
    year: 2024,
    variant: "default",
    state: "waiting",
    revision: 1,
    owner_action_required: false,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-01T00:00:00Z",
  }))
  let failFirstPage = false
  await page.route("**/api/v1/workflows*", async (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("attention") === "true") return route.fulfill({ status: 200, json: { data: [] } })
    const limit = Number(url.searchParams.get("limit") ?? "300")
    const offset = Number(url.searchParams.get("offset") ?? "0")
    if (offset === 0 && failFirstPage) return route.fulfill({ status: 500, json: { error: { message: "temporary reload failure" } } })
    await route.fulfill({ status: 200, json: { data: workflows.slice(offset, offset + limit) } })
  })
  await page.goto(`${baseURL}/activity/queue`)
  await expect(page.getByText("Refresh Failure 001", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Load more" }).click()
  await expect(page.getByText("Refresh Failure 201", { exact: true })).toBeVisible()
  failFirstPage = true
  await page.getByRole("button", { name: "Refresh", exact: true }).click()
  await expect(page.getByText("temporary reload failure")).toBeVisible()
  await expect(page.getByText("Refresh Failure 001", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Load more" })).toHaveCount(0)
})
