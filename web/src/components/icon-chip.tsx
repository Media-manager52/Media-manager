import type { ReactNode } from "react"

import { cx } from "@/components/ui"

const chipColors = ["#2563eb", "#7c3aed", "#0d9488", "#db2777", "#ea580c", "#16a34a", "#0891b2", "#4f46e5"]

/** A stable colour for a name, so each indexer keeps its colour between visits. */
export function chipColor(name: string): string {
  let hash = 0
  for (const char of name) hash = (hash * 31 + char.charCodeAt(0)) >>> 0
  return chipColors[hash % chipColors.length]
}

/** A coloured square with an icon or initials, like the chips on HolyArrs' cards. */
export function IconChip({ color, children, className }: { color: string; children: ReactNode; className?: string }) {
  return <span aria-hidden className={cx("flex size-9 shrink-0 items-center justify-center rounded-xl text-sm font-bold text-white", className)} style={{ background: color }}>{children}</span>
}

/** One or two letters for a name: "NZBgeek" → "N", "Nzb.life" → "N", "treasure-maps" → "TM". */
export function initials(name: string): string {
  const words = name.replace(/\{[^}]*\}/g, "").split(/[\s\-_]+/).filter(Boolean)
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase()
  return (words[0]?.[0] ?? "?").toUpperCase()
}
