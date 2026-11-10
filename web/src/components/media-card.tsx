import type { ReactNode } from "react"

import { sizedArtwork } from "@/components/poster"
import { cx } from "@/components/ui"

/**
 * A card with the title's wide picture on top and a light panel below, as on
 * HolyArrs' request page. Without wide artwork the poster, blurred, fills the
 * picture; without any artwork a calm dark surface does.
 */
export function MediaCard({ backdrop, poster, title, subtitle, corner, children, className, as: Tag = "li" }: {
  backdrop?: string
  poster?: string
  title: ReactNode
  subtitle?: ReactNode
  corner?: ReactNode
  children?: ReactNode
  className?: string
  as?: "li" | "article" | "div"
}) {
  const wide = sizedArtwork(backdrop, "w780")
  const tall = sizedArtwork(poster, "w342")
  return (
    <Tag className={cx("flex min-w-0 flex-col overflow-hidden rounded-2xl border border-line bg-surface shadow-card", className)}>
      <div className="relative h-36 shrink-0 overflow-hidden bg-[#1d1d1f] sm:h-40">
        {wide ? <img src={wide} alt="" loading="lazy" className="absolute inset-0 size-full object-cover" /> : tall ? <img src={tall} alt="" loading="lazy" className="absolute inset-0 size-full scale-110 object-cover opacity-70 blur-xl" /> : null}
        <div aria-hidden className="absolute inset-0 bg-gradient-to-t from-black/85 via-black/30 to-black/0" />
        {corner ? <div className="absolute right-3 top-3">{corner}</div> : null}
        <div className="absolute inset-x-4 bottom-3 text-white">
          <div className="line-clamp-2 text-lg font-bold leading-tight tracking-tight">{title}</div>
          {subtitle ? <div className="mt-0.5 truncate text-xs text-white/85">{subtitle}</div> : null}
        </div>
      </div>
      {children ? <div className="flex flex-1 flex-col gap-3 p-4">{children}</div> : null}
    </Tag>
  )
}

/** A small frosted link or label for the card's top corner. */
export const cornerClass = "inline-flex items-center gap-1 rounded-full bg-black/55 px-2.5 py-1 text-[11px] font-semibold text-white backdrop-blur hover:bg-black/70"

/** Placeholder cards while a card list loads. */
export function CardGridSkeleton({ count = 6 }: { count?: number }) {
  return (
    <div aria-hidden className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
      {Array.from({ length: count }, (_, index) => (
        <div key={index} className="overflow-hidden rounded-2xl border border-line bg-surface">
          <div className="h-36 animate-pulse bg-surface-3 sm:h-40" />
          <div className="space-y-2 p-4"><div className="h-3 w-3/4 animate-pulse rounded bg-surface-3" /><div className="h-8 w-1/2 animate-pulse rounded-lg bg-surface-3" /></div>
        </div>
      ))}
    </div>
  )
}
