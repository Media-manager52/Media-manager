#!/usr/bin/env node
// Loopback-only, read-only inspection bridge for other AI agents on this host.
// It injects the owner bearer server-side; clients never receive that token.
import { appendFileSync } from "node:fs"
import { execFileSync } from "node:child_process"
import http from "node:http"

const apiBase = (process.env.MEDIA_MANAGER_API_URL || "http://127.0.0.1:28081").replace(/\/$/, "")
const port = Number(process.env.MEDIA_MANAGER_AI_PORT || "28082")
const container = process.env.MEDIA_MANAGER_API_CONTAINER || "media-manager-api-1"
const logPath = process.env.MEDIA_MANAGER_AI_LOG || ""

let cachedToken = { value: "", readAt: 0 }

function readOwnerToken(force = false) {
  if (!force && cachedToken.value && Date.now() - cachedToken.readAt < 30_000) return cachedToken.value
  let value = process.env.MEDIA_MANAGER_OWNER_TOKEN || ""
  if (!value) {
    try {
      const env = execFileSync("docker", ["inspect", container, "--format", "{{range .Config.Env}}{{println .}}{{end}}"], {
        encoding: "utf8", stdio: ["ignore", "pipe", "ignore"], timeout: 10_000,
      })
      value = env.split("\n").find((line) => line.startsWith("OWNER_TOKEN="))?.slice("OWNER_TOKEN=".length) || ""
    } catch {
      value = ""
    }
  }
  if (value) cachedToken = { value, readAt: Date.now() }
  return cachedToken.value
}

if (!readOwnerToken(true)) {
  console.error("Cannot read OWNER_TOKEN from the Media Manager API container.")
  process.exit(1)
}
if (!Number.isInteger(port) || port < 1 || port > 65535) {
  console.error("MEDIA_MANAGER_AI_PORT must be a valid TCP port.")
  process.exit(2)
}

const blocked = [
  /^\/api\/v1\/backups\/[^/]+\/download$/,
  /^\/api\/v1\/backups\/[^/]+\/offsite$/,
  /^\/api\/v1\/settings\/notifications(?:\/|$)/,
  /^\/api\/v1\/integrations\/seerr\/webhook$/,
]

// Load the live OpenAPI document and allow only GET operations whose
// x-mm-effect is read. Provider-effect GETs (discovery/streaming) are excluded
// even though they use GET. Keep this fail-closed: no allowlist, no server.
function pathPattern(path) {
  const escaped = path.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/\\\{[^/]+\\\}/g, "[^/]+")
  return new RegExp(`^${escaped}$`)
}
const allowlist = []
async function loadAllowlist() {
  const response = await fetch(`${apiBase}/api/v1/openapi.json`, {
    headers: { Authorization: `Bearer ${readOwnerToken(true)}`, Accept: "application/json" },
    signal: AbortSignal.timeout(15_000),
  })
  if (!response.ok) throw new Error(`OpenAPI request failed with HTTP ${response.status}`)
  const document = await response.json()
  for (const [path, operations] of Object.entries(document.paths || {})) {
    for (const [method, operation] of Object.entries(operations || {})) {
      if (method.toUpperCase() !== "GET" || operation["x-mm-effect"] !== "read") continue
      if (blocked.some((pattern) => pattern.test(path))) continue
      allowlist.push(pathPattern(path))
    }
  }
  if (allowlist.length === 0) throw new Error("OpenAPI contains no read operations")
}

function allowedPath(pathname) {
  const lower = pathname.toLowerCase()
  if (pathname.includes("//") || pathname.includes("\\") || lower.includes("%2f") || lower.includes("%5c")) return false
  if (pathname.split("/").some((part) => part === "." || part === "..")) return false
  return allowlist.some((pattern) => pattern.test(pathname))
}

function log(entry) {
  const line = JSON.stringify({ ts: new Date().toISOString(), ...entry })
  if (logPath) {
    try { appendFileSync(logPath, line + "\n", { mode: 0o600 }) } catch { /* journal is the fallback */ }
  }
  console.log(line)
}

const server = http.createServer(async (request, response) => {
  const started = Date.now()
  const method = request.method || "GET"
  if (method !== "GET" && method !== "HEAD") {
    response.writeHead(405, { "content-type": "application/json", "allow": "GET, HEAD" })
    response.end(JSON.stringify({ error: "read_only_proxy" }))
    log({ method, path: request.url, status: 405, ms: Date.now() - started })
    return
  }
  let url
  try { url = new URL(request.url || "/", "http://127.0.0.1") } catch {
    response.writeHead(400).end(); return
  }
  if (!allowedPath(url.pathname)) {
    response.writeHead(403, { "content-type": "application/json" })
    response.end(JSON.stringify({ error: "path_not_available_through_read_only_proxy" }))
    log({ method, path: url.pathname, status: 403, ms: Date.now() - started })
    return
  }
  try {
    async function callUpstream(token) {
      return fetch(`${apiBase}${url.pathname}${url.search}`, {
        method,
        redirect: "manual",
        headers: { Authorization: `Bearer ${token}`, Accept: "application/json" },
        signal: AbortSignal.timeout(90_000),
      })
    }
    let upstream = await callUpstream(readOwnerToken())
    if (upstream.status === 401) upstream = await callUpstream(readOwnerToken(true))
    // Never turn an upstream redirect into a second, unvalidated request.
    if (upstream.status >= 300 && upstream.status < 400) {
      response.writeHead(502, { "content-type": "application/json" })
      response.end(JSON.stringify({ error: "upstream_redirect_refused" }))
      log({ method, path: url.pathname, status: 502, ms: Date.now() - started })
      return
    }
    const body = Buffer.from(await upstream.arrayBuffer())
    response.writeHead(upstream.status, {
      "content-type": upstream.headers.get("content-type") || "application/json; charset=utf-8",
      "cache-control": "no-store",
      "x-content-type-options": "nosniff",
    })
    if (method === "HEAD") response.end(); else response.end(body)
    log({ method, path: url.pathname, status: upstream.status, bytes: body.length, ms: Date.now() - started })
  } catch {
    response.writeHead(502, { "content-type": "application/json" })
    response.end(JSON.stringify({ error: "media_manager_api_unavailable" }))
    log({ method, path: url.pathname, status: 502, ms: Date.now() - started })
  }
})

loadAllowlist().then(() => {
  server.listen(port, "127.0.0.1", () => {
    console.log(JSON.stringify({ event: "read_only_proxy_listening", host: "127.0.0.1", port, api: apiBase, routes: allowlist.length }))
  })
}).catch((error) => {
  console.error(`Cannot load the read-only allowlist: ${error.message}`)
  process.exit(1)
})
