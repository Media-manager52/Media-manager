// Loopback same-origin acceptance proxy.
//
// Reproduces the Cosmos Cloud route shape for local browser acceptance without
// the public edge: /api/* and /health/* are forwarded to the API with the owner
// bearer token injected server-side, and every other path is forwarded to the
// UI container. It binds to 127.0.0.1 only and never logs headers or tokens.
//
// This is test tooling, not a production component.

import http from "node:http"
import process from "node:process"

const token = process.env.MM_OWNER_TOKEN ?? ""
const apiTarget = process.env.MM_API_TARGET ?? "127.0.0.1:28081"
const uiTarget = process.env.MM_UI_TARGET ?? ""
const port = Number(process.env.MM_PROXY_PORT ?? "4175")

if (!token) throw new Error("MM_OWNER_TOKEN is required")
if (!uiTarget.includes(":")) throw new Error("MM_UI_TARGET must be host:port")
if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error("MM_PROXY_PORT must be a valid TCP port")

function parseTarget(value) {
  const index = value.lastIndexOf(":")
  return { host: value.slice(0, index), port: Number(value.slice(index + 1)) }
}

const api = parseTarget(apiTarget)
const ui = parseTarget(uiTarget)

const hopByHop = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailers",
  "transfer-encoding",
  "upgrade",
])

function forward(request, response, target, injectOwnerToken) {
  const headers = {}
  for (const [name, value] of Object.entries(request.headers)) {
    if (hopByHop.has(name.toLowerCase())) continue
    headers[name] = value
  }
  headers["accept-encoding"] = "identity"
  if (injectOwnerToken) {
    headers["authorization"] = `Bearer ${token}`
  } else {
    delete headers["authorization"]
  }

  const upstream = http.request(
    {
      host: target.host,
      port: target.port,
      method: request.method,
      path: request.url,
      headers,
    },
    (upstreamResponse) => {
      response.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers)
      upstreamResponse.pipe(response)
    },
  )
  upstream.on("error", () => {
    if (!response.headersSent) response.writeHead(502)
    response.end()
  })
  request.pipe(upstream)
}

const server = http.createServer((request, response) => {
  const path = request.url ?? "/"
  if (path.startsWith("/api/") || path.startsWith("/health/")) {
    forward(request, response, api, true)
  } else {
    forward(request, response, ui, false)
  }
})

server.listen(port, "127.0.0.1", () => {
  process.stderr.write(`acceptance proxy listening on 127.0.0.1:${port}\n`)
})
