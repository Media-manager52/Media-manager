// Production static server for the Media Manager web UI.
//
// No dependencies: it serves the built files in ./dist, falls back to
// index.html for client routes, and sets strict security headers. The API is
// a separate service; the public edge routes /api there, so this server never
// proxies, stores or sees credentials.
import { createReadStream } from "node:fs"
import { stat } from "node:fs/promises"
import { createServer } from "node:http"
import { extname, join, normalize, sep } from "node:path"
import { fileURLToPath } from "node:url"

const root = join(fileURLToPath(new URL(".", import.meta.url)), "dist")
const port = Number(process.env.PORT ?? 3000)
const host = process.env.HOST ?? "0.0.0.0"

const types = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".webp": "image/webp",
  ".ico": "image/x-icon",
  ".json": "application/json",
  ".woff2": "font/woff2",
  ".txt": "text/plain; charset=utf-8",
}

// Artwork comes from TMDB/Plex image hosts; everything else is same-origin.
export const contentSecurityPolicy = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: https://image.tmdb.org https://artworks.thetvdb.com https://*.thetvdb.com https://us.simkl.in",
  "font-src 'self'",
  "connect-src 'self'",
  "frame-ancestors 'none'",
  "base-uri 'none'",
  "form-action 'self'",
  "object-src 'none'",
].join("; ")

const baseHeaders = {
  "Content-Security-Policy": contentSecurityPolicy,
  "X-Content-Type-Options": "nosniff",
  "Referrer-Policy": "no-referrer",
  "Cross-Origin-Opener-Policy": "same-origin",
  "Permissions-Policy": "camera=(), microphone=(), geolocation=()",
}

async function resolveFile(urlPath) {
  let decoded
  try {
    decoded = decodeURIComponent(urlPath)
  } catch {
    return null
  }
  const candidate = normalize(join(root, decoded))
  if (candidate !== root && !candidate.startsWith(root + sep)) return null
  try {
    const info = await stat(candidate)
    if (info.isFile()) return { path: candidate, size: info.size }
  } catch { /* not a file: client route */ }
  return null
}

export function createWebServer() {
  return createServer(async (request, response) => {
    if (request.method !== "GET" && request.method !== "HEAD") {
      response.writeHead(405, { ...baseHeaders, Allow: "GET, HEAD" })
      response.end()
      return
    }
    const urlPath = new URL(request.url ?? "/", "http://localhost").pathname
    if (urlPath.startsWith("/api/") || urlPath.startsWith("/health/")) {
      // These belong to the API service; answering here would hide a routing mistake.
      response.writeHead(404, { ...baseHeaders, "Content-Type": "application/json" })
      response.end('{"error":{"code":"not_found","message":"The API is served by the API service."}}')
      return
    }
    let file = await resolveFile(urlPath)
    const isAsset = Boolean(file) && urlPath.startsWith("/assets/")
    if (!file) {
      // Unknown file-like paths are a real 404; everything else is a client route.
      if (extname(urlPath) !== "") {
        response.writeHead(404, { ...baseHeaders, "Content-Type": "text/plain; charset=utf-8" })
        response.end("Not found")
        return
      }
      file = await resolveFile("/index.html")
      if (!file) {
        response.writeHead(503, baseHeaders)
        response.end("UI build missing")
        return
      }
    }
    response.writeHead(200, {
      ...baseHeaders,
      "Content-Type": types[extname(file.path)] ?? "application/octet-stream",
      "Content-Length": file.size,
      // Hashed assets never change; the HTML must always be revalidated.
      "Cache-Control": isAsset ? "public, max-age=31536000, immutable" : "no-cache",
    })
    if (request.method === "HEAD") {
      response.end()
      return
    }
    createReadStream(file.path).pipe(response)
  })
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  createWebServer().listen(port, host, () => {
    console.log(`media-manager-web listening on http://${host}:${port}`)
  })
}
