import { mkdir, mkdtemp, writeFile } from "node:fs/promises"
import type { AddressInfo } from "node:net"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterAll, beforeAll, describe, expect, it } from "vitest"

// The server reads ./dist next to server.mjs; point it at a temp copy.
let base = ""
let close: () => void = () => {}

beforeAll(async () => {
  const dir = await mkdtemp(join(tmpdir(), "mm-web-"))
  await mkdir(join(dir, "dist", "assets"), { recursive: true })
  await writeFile(join(dir, "dist", "index.html"), "<!doctype html><title>t</title>")
  await writeFile(join(dir, "dist", "assets", "app-abc.js"), "console.log(1)")
  await writeFile(join(dir, "secret.txt"), "outside dist")
  const source = await import("node:fs/promises").then((fs) => fs.readFile(new URL("../server.mjs", import.meta.url), "utf8"))
  await writeFile(join(dir, "server.mjs"), source)
  const { createWebServer } = await import(join(dir, "server.mjs"))
  const server = createWebServer()
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve))
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
  close = () => server.close()
})
afterAll(() => close())

describe("static web server", () => {
  it("serves client routes as the app shell with security headers", async () => {
    const response = await fetch(`${base}/bibliotek/123`)
    expect(response.status).toBe(200)
    expect(await response.text()).toContain("<title>t</title>")
    expect(response.headers.get("content-security-policy")).toContain("default-src 'self'")
    expect(response.headers.get("cache-control")).toBe("no-cache")
    expect(response.headers.get("x-content-type-options")).toBe("nosniff")
  })

  it("caches hashed assets forever", async () => {
    const response = await fetch(`${base}/assets/app-abc.js`)
    expect(response.status).toBe(200)
    expect(response.headers.get("cache-control")).toContain("immutable")
  })

  it("never serves files outside dist", async () => {
    for (const path of ["/../secret.txt", "/%2e%2e/secret.txt", "/assets/..%2f..%2fsecret.txt"]) {
      const response = await fetch(`${base}${path}`)
      expect(await response.text()).not.toContain("outside dist")
    }
  })

  it("does not answer for the API or accept writes", async () => {
    expect((await fetch(`${base}/api/v1/dashboard`)).status).toBe(404)
    expect((await fetch(`${base}/`, { method: "POST" })).status).toBe(405)
    expect((await fetch(`${base}/missing.png`)).status).toBe(404)
  })
})
