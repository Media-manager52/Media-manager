import { useState } from "react"

/** Shows a long list in batches, so a page stays short until someone asks for more. */
export function useShowMore<T>(items: T[], pageSize: number) {
  const [visible, setVisible] = useState(pageSize)
  return {
    shown: items.slice(0, visible),
    remaining: Math.max(0, items.length - visible),
    showMore: () => setVisible((count) => count + pageSize),
  }
}
