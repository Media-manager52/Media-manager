import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

// In development the API runs separately; MM_API_URL points at it
// (for example http://127.0.0.1:28081). Production serves /api through the
// same public origin, so the bundle always calls relative /api paths.
const apiTarget = process.env.MM_API_URL ?? "http://127.0.0.1:28081"
// Local development only: the public edge normally adds the owner credential.
const devToken = process.env.MM_API_TOKEN
const proxy = { target: apiTarget, changeOrigin: true, headers: devToken ? { Authorization: `Bearer ${devToken}` } : undefined }

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": new URL("./src", import.meta.url).pathname } },
  server: { proxy: { "/api": proxy, "/health": proxy } },
  preview: { proxy: { "/api": proxy, "/health": proxy } },
  build: { outDir: "dist", sourcemap: false, chunkSizeWarningLimit: 700 },
})
