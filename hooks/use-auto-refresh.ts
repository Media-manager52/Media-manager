import * as React from "react"

/** How often read-only owner screens re-read persisted state. */
export const LIVE_REFRESH_MS = 15_000

/**
 * Calls `refresh` every `intervalMs` while the page is visible, and once when
 * it becomes visible again. It pauses in a background tab. Callers pass a
 * read of Media Manager's own persisted state; this never starts work,
 * contacts an integration, or runs while an owner edit is being typed into a
 * form that is derived from the refreshed data.
 */
export function useAutoRefresh(refresh: () => void, intervalMs: number = LIVE_REFRESH_MS, enabled = true) {
  const latest = React.useRef(refresh)
  React.useEffect(() => {
    latest.current = refresh
  })
  React.useEffect(() => {
    if (!enabled || intervalMs <= 0) return
    const visible = () => document.visibilityState === "visible"
    const timer = window.setInterval(() => {
      if (visible()) latest.current()
    }, intervalMs)
    const onVisibility = () => {
      if (visible()) latest.current()
    }
    document.addEventListener("visibilitychange", onVisibility)
    return () => {
      window.clearInterval(timer)
      document.removeEventListener("visibilitychange", onVisibility)
    }
  }, [enabled, intervalMs])
}
