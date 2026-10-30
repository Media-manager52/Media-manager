"use client"

import * as React from "react"
import type { Page } from "@/lib/media-manager-api"
import { LIVE_REFRESH_MS, useAutoRefresh } from "@/hooks/use-auto-refresh"

/**
 * Incremental list loader for large owner read models. The first page is loaded
 * immediately; "load more" appends only the next server page. Auto-refresh is
 * deliberately limited to the single-page state so a background refresh never
 * turns a paged view back into an unbounded download. Manual refresh resets the
 * list to the first page.
 */
export function usePagedList<T>(
  loadPage: (offset: number, limit: number, signal: AbortSignal) => Promise<Page<T>>,
  pageSize = 100,
  options?: { autoRefresh?: boolean; refreshMs?: number; keyOf?: (item: T) => string },
) {
  const keyOf = React.useMemo(() => options?.keyOf ?? ((item: T) => (item as { id: string }).id), [options?.keyOf])
  const [items, setItems] = React.useState<T[]>([])
  const [hasMore, setHasMore] = React.useState(true)
  const [loading, setLoading] = React.useState(true)
  const [loadingMore, setLoadingMore] = React.useState(false)
  const [error, setError] = React.useState("")
  const [revision, setRevision] = React.useState(0)

  React.useEffect(() => {
    const controller = new AbortController()
    loadPage(0, pageSize, controller.signal)
      .then((page) => {
        setItems(page.items)
        setHasMore(page.hasMore)
        setError("")
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Could not load live data")
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [loadPage, pageSize, revision])

  const refresh = React.useCallback(() => {
    setLoading(true)
    setRevision((current) => current + 1)
  }, [])
  const loadMore = React.useCallback(async () => {
    if (loadingMore || loading || !hasMore) return
    setLoadingMore(true)
    try {
      const page = await loadPage(items.length, pageSize, new AbortController().signal)
      setItems((current) => {
        const seen = new Set(current.map(keyOf))
        return [...current, ...page.items.filter((item) => !seen.has(keyOf(item)))]
      })
      setHasMore(page.hasMore)
      setError("")
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Could not load more records")
    } finally {
      setLoadingMore(false)
    }
  }, [hasMore, items.length, keyOf, loadPage, loading, loadingMore, pageSize])

  useAutoRefresh(
    refresh,
    options?.refreshMs ?? LIVE_REFRESH_MS,
    Boolean(options?.autoRefresh) && items.length <= pageSize,
  )

  return { items, hasMore, loading, loadingMore, error, refresh, loadMore, loaded: items.length }
}
