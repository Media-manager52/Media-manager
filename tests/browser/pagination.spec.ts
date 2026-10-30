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
