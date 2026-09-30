import AxeBuilder from "@axe-core/playwright"
import { expect, test, type Page, type TestInfo } from "@playwright/test"

import { routeTitles } from "../../lib/media-manager-routes"

type BrowserAudit = {
  blockedExternalAssets: Set<string>
  localFailures: string[]
  mutationAttempts: string[]
  unexpectedExternal: string[]
  consoleErrors: string[]
  pageErrors: string[]
}

const baseOrigin = new URL(`http://127.0.0.1:${process.env.MEDIA_MANAGER_LIVE_PORT}`).origin
const audits = new WeakMap<Page, BrowserAudit>()
const screenshotRoutes = new Set(["/", "/media", "/media/series", "/activity/queue", "/system/health"])

function safeRequestLabel(method: string, rawURL: string) {
  const url = new URL(rawURL)
  return `${method} ${url.origin}${url.pathname}`
}

function routeSlug(path: string) {
  return path === "/" ? "home" : path.slice(1).replaceAll("/", "-")
}

async function settle(page: Page) {
  await page.waitForLoadState("domcontentloaded")
  await page.waitForLoadState("networkidle", { timeout: 10_000 })
}

async function verifyViewport(page: Page) {
  const dimensions = await page.evaluate(() => ({
    body: document.body.scrollWidth,
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }))
  expect(dimensions.document, "document must not overflow the viewport").toBeLessThanOrEqual(dimensions.viewport + 1)
  expect(dimensions.body, "body must not overflow the viewport").toBeLessThanOrEqual(dimensions.viewport + 1)
}

async function captureCoreScreenshot(page: Page, testInfo: TestInfo, path: string) {
  if (!screenshotRoutes.has(path)) return
  const screenshotPath = testInfo.outputPath(`${routeSlug(path)}-${testInfo.project.name}.png`)
  await page.screenshot({ path: screenshotPath, fullPage: true })
  await testInfo.attach(`${routeSlug(path)}-${testInfo.project.name}`, { path: screenshotPath, contentType: "image/png" })
}

async function visit(page: Page, path: string) {
  const response = await page.goto(path, { waitUntil: "domcontentloaded" })
  expect(response, `${path} must return a document response`).not.toBeNull()
  expect(response?.status(), `${path} must return HTTP 200`).toBe(200)
  await settle(page)
}

test.beforeEach(async ({ page }) => {
  const audit: BrowserAudit = {
    blockedExternalAssets: new Set(),
    localFailures: [],
    mutationAttempts: [],
    unexpectedExternal: [],
    consoleErrors: [],
    pageErrors: [],
  }
  audits.set(page, audit)

  await page.route("**/*", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.protocol === "data:" || url.protocol === "blob:") {
      await route.continue()
      return
    }
    const proxiedSource = url.searchParams.get("url")
    if (url.origin === baseOrigin && proxiedSource) {
      try {
        const target = new URL(proxiedSource)
        if (target.origin !== baseOrigin) {
          const label = safeRequestLabel(request.method(), target.href)
          if (request.resourceType() === "image") audit.blockedExternalAssets.add(label)
          else audit.unexpectedExternal.push(`${request.resourceType()} ${label}`)
          await route.abort("blockedbyclient")
          return
        }
      } catch {
        // A relative or non-URL query value cannot identify an external target.
      }
    }
    if (url.origin !== baseOrigin) {
      const label = safeRequestLabel(request.method(), request.url())
      if (request.resourceType() === "image") audit.blockedExternalAssets.add(label)
      else audit.unexpectedExternal.push(`${request.resourceType()} ${label}`)
      await route.abort("blockedbyclient")
      return
    }
    if (request.method() !== "GET" && request.method() !== "HEAD") {
      audit.mutationAttempts.push(safeRequestLabel(request.method(), request.url()))
      await route.abort("blockedbyclient")
      return
    }
    await route.continue()
  })

  page.on("response", (response) => {
    const url = new URL(response.url())
    if (url.origin === baseOrigin && response.status() >= 400) {
      audit.localFailures.push(`${response.status()} ${safeRequestLabel(response.request().method(), response.url())}`)
    }
  })
  page.on("requestfailed", (request) => {
    const url = new URL(request.url())
    if (url.origin === baseOrigin) audit.localFailures.push(`failed ${safeRequestLabel(request.method(), request.url())}`)
  })
  page.on("console", (message) => {
    if (message.type() === "error" && !message.text().includes("ERR_BLOCKED_BY_CLIENT")) {
      audit.consoleErrors.push("browser console error")
    }
  })
  page.on("pageerror", (error) => audit.pageErrors.push(error.name || "page error"))
})

test.afterEach(async ({ page }, testInfo) => {
  const audit = audits.get(page)
  if (!audit) return
  if (audit.blockedExternalAssets.size > 0) {
    await testInfo.attach("blocked-external-assets.json", {
      body: Buffer.from(JSON.stringify([...audit.blockedExternalAssets].sort(), null, 2)),
      contentType: "application/json",
    })
  }
  expect(audit.mutationAttempts, "the read-only suite must never issue a mutation").toEqual([])
  expect(audit.unexpectedExternal, "non-image traffic must remain on loopback").toEqual([])
  expect(audit.localFailures, "all local documents and API requests must succeed").toEqual([])
  expect(audit.consoleErrors, "the page must not log console errors").toEqual([])
  expect(audit.pageErrors, "the page must not raise uncaught errors").toEqual([])
})

for (const [path, metadata] of Object.entries(routeTitles)) {
  test(`renders the live read-only route ${path}`, async ({ page }, testInfo) => {
    await visit(page, path)
    await expect(page.getByRole("heading", { level: 1, name: metadata.title, exact: true })).toBeVisible()
    await verifyViewport(page)

    if (testInfo.project.name === "desktop" || screenshotRoutes.has(path)) {
      const results = await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
        .analyze()
      const serious = results.violations.filter((violation) => violation.impact === "serious" || violation.impact === "critical")
      expect(serious, `${path} must have no serious accessibility violations`).toEqual([])
    }

    await captureCoreScreenshot(page, testInfo, path)
  })
}

for (const library of [
  { path: "/media", title: "Movies", empty: "Your movie catalog is empty" },
  { path: "/media/series", title: "TV Shows", empty: "Your TV show catalog is empty" },
] as const) {
  test(`exercises safe ${library.title} interactions`, async ({ page }) => {
    await visit(page, library.path)
    const mediaKind = library.path === "/media" ? "movie" : "series"
    const section = page.locator(`section[aria-labelledby="${mediaKind}-library-title"]`)

    await section.getByRole("button", { name: "List view" }).click()
    await expect(section.getByRole("button", { name: "List view" })).toHaveAttribute("aria-pressed", "true")
    await section.getByRole("button", { name: "Grid view" }).click()
    await expect(section.getByRole("button", { name: "Grid view" })).toHaveAttribute("aria-pressed", "true")

    const cards = section.locator("button.group[aria-pressed]")
    if (await cards.count() === 0) {
      await expect(section.getByText(library.empty, { exact: true })).toBeVisible()
      return
    }

    await cards.first().focus()
    await page.keyboard.press("Enter")
    const inspector = page.getByRole("complementary", { name: /^Details for / })
    await expect(inspector).toBeVisible()
    for (const tab of ["Files", "Metadata", "History", "Overview"]) {
      await inspector.getByRole("tab", { name: tab }).click()
      await expect(inspector.getByRole("tab", { name: tab })).toHaveAttribute("aria-selected", "true")
    }
    await inspector.getByRole("button", { name: "Close details" }).click()

    const search = section.getByPlaceholder(`Search ${library.title}`)
    await search.fill("__media_manager_live_test_no_match__")
    const noMatch = library.title === "Movies" ? "No movies matched" : "No TV shows matched"
    await expect(section.getByText(noMatch, { exact: true })).toBeVisible()
    await search.fill("")
    await expect(cards.first()).toBeVisible()

    const nextPage = section.getByRole("button", { name: "Next page" })
    if (await nextPage.isEnabled()) await nextPage.click()
    await verifyViewport(page)
  })
}

test("exercises safe Requests interactions", async ({ page }) => {
  await visit(page, "/requests")
  const section = page.locator("section").filter({ hasText: "Seerr requests" }).first()
  await expect(section.getByRole("button", { name: /^All \d+$/ })).toHaveAttribute("aria-pressed", "true")

  await section.getByRole("button", { name: /^Needs attention \d+$/ }).click()
  await expect(section.getByRole("button", { name: /^Needs attention \d+$/ })).toHaveAttribute("aria-pressed", "true")
  await verifyViewport(page)

  const search = page.getByPlaceholder("Search title, requester, ID")
  await search.fill("__media_manager_live_test_no_match__")
  await expect(page.getByText("No requests match the active filter.", { exact: true })).toBeVisible()
  await search.fill("")
  await section.getByRole("button", { name: /^All \d+$/ }).click()

  const rows = section.locator("tbody tr")
  if ((await rows.count()) > 0) {
    await rows.first().click()
    await expect(page.getByText("Request detail", { exact: true })).toBeVisible()
    await expect(page.getByText("Seerr request ID", { exact: true })).toBeVisible()
  }
  await verifyViewport(page)
})

test("opens navigation controls without changing server state", async ({ page }, testInfo) => {
  await visit(page, "/")
  await page.keyboard.press("Control+K")
  await expect(page.getByRole("dialog", { name: "Go to page" })).toBeVisible()
  await page.keyboard.press("Escape")

  if (testInfo.project.name === "mobile") {
    await page.getByRole("button", { name: "Toggle Sidebar" }).click()
    await expect(page.getByRole("link", { name: /Media Manager/ })).toBeVisible()
  }
  await verifyViewport(page)
})

test("validates configured movie and TV add forms without submitting", async ({ page }) => {
  await visit(page, "/discover")

  const mediaType = page.getByLabel("Media type")
  const searchImmediately = page.getByRole("switch", { name: "Search immediately" })
  await expect(mediaType).toContainText("Movie")
  await expect(page.getByRole("button", { name: "Add and search" })).toBeEnabled()

  await searchImmediately.click()
  await expect(searchImmediately).toHaveAttribute("data-state", "unchecked")
  await expect(page.getByRole("button", { name: "Add without search" })).toBeEnabled()

  await mediaType.click()
  await page.getByRole("option", { name: "TV series" }).click()
  await expect(mediaType).toContainText("TV series")
  await expect(page.getByLabel("Exact seasons and episode counts")).toHaveValue("1:10")
  await expect(page.getByRole("button", { name: "Add without search" })).toBeEnabled()
  await verifyViewport(page)
})

test("renders available live detail routes", async ({ page }) => {
  const detailLists = [
    { list: "/media", selector: 'a[href^="/media/movies/"]' },
    { list: "/media/series", selector: 'a[href^="/media/series/"]' },
    { list: "/files", selector: 'a[href^="/files/"]', heading: "File inspector" },
    { list: "/sources/indexers", selector: 'a[href^="/sources/indexers/"]', heading: "Indexer details" },
  ]

  for (const detail of detailLists) {
    await visit(page, detail.list)
    const href = await page.locator(detail.selector).first().getAttribute("href")
    if (!href) continue
    await visit(page, href)
    const heading = page.getByRole("heading", { level: 1 })
    await expect(heading).toBeVisible()
    if (detail.heading) await expect(heading).toHaveText(detail.heading)
    else await expect(heading).not.toHaveText(/details/i)
    await verifyViewport(page)
  }
})
