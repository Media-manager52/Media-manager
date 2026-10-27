#!/usr/bin/env node
// Loopback-only, read-only inspection bridge for other AI agents on this host.
// It injects the owner bearer server-side; clients never receive that token.
import { execFileSync } from "node:child_process"
import http from "node:http"

const apiBase = (process.env.MEDIA_MANAGER_API_URL || "http://127.0.0.1:28081").replace(/\/$/, "")
const port = Number(process.env.MEDIA_MANAGER_AI_PORT || "28082")
const container = process.env.MEDIA_MANAGER_API_CONTAINER || "media-manager-api-1"
const logPath = process.env.MEDIA_MANAGER_AI_LOG || ""

function readOwnerToken() {
  if (process.env.MEDIA_MANAGER_OWNER_TOKEN) return process.env.MEDIA_MANAGER_OWNER_TOKEN
  try {
    const env = execFileSync("docker", ["inspect", container, "--format", "{{range .Config.Env}}{{println .}}{{end}}"], {
      encoding: "utf8", stdio: ["ignore", "pipe", "ignore"], timeout: 10_000,
    })
    return env.split("\n").find((line) => line.startsWith("OWNER_TOKEN="))?.slice("OWNER_TOKEN=".length) || ""
  } catch {
    return ""
  }
}

const ownerToken = readOwnerToken()
if (!ownerToken) {
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
  /^\/api\/v1\/integrations\/seerr\/webhook$/,
]

function allowedPath(pathname) {
  if (pathname === "/health/live" || pathname === "/health/ready") return true
  if (!pathname.startsWith("/api/v1/")) return false
  return !blocked.some((pattern) => pattern.test(pathname))
}

function log(entry) {
  const line = JSON.stringify({ ts: new Date().toISOString(), ...entry })
  if (logPath) {
    try { require("node:fs").appendFileSync(logPath, line + "\n", { mode: 0o600 }) } catch { /* journal is the fallback */ }
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
    const upstream = await fetch(`${apiBase}${url.pathname}${url.search}`, {
      method,
      headers: { Authorization: `Bearer ${ownerToken}`, Accept: "application/json" },
      signal: AbortSignal.timeout(90_000),
    })
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

server.listen(port, "127.0.0.1", () => {
  console.log(JSON.stringify({ event: "read_only_proxy_listening", host: "127.0.0.1", port, api: apiBase }))
})
