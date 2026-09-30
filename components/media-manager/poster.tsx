"use client"

import * as React from "react"
import Image from "next/image"

import { cn } from "@/lib/utils"

/** Stable hue per title so artwork-less titles still read as distinct cards. */
export function titleHue(title: string) {
  let hash = 0
  for (let index = 0; index < title.length; index += 1) hash = (hash * 31 + title.charCodeAt(index)) >>> 0
  return hash % 360
}

/**
 * Poster art with a typographic title card when provider artwork is absent or
 * fails to load. The card never fetches anything; it is pure CSS.
 */
export function Poster({ src, title, subtitle, className, sizes = "180px" }: { src?: string; title: string; subtitle?: string; className?: string; sizes?: string }) {
  const [failed, setFailed] = React.useState<string | null>(null)
  const hue = titleHue(title)
  return <div className={cn("relative aspect-[2/3] overflow-hidden rounded-lg bg-surface-3", className)}>
    {src && failed !== src
      ? <Image src={src} alt="" fill sizes={sizes} onError={() => setFailed(src)} className="object-cover" />
      : <div aria-hidden className="absolute inset-0 flex flex-col justify-end p-3" style={{ background: `linear-gradient(160deg, hsl(${hue} 32% 30%), hsl(${(hue + 40) % 360} 38% 12%))` }}>
        <div className="absolute -right-6 -top-6 size-28 rounded-full bg-white/[0.07]" />
        <div className="line-clamp-4 text-[15px] font-bold uppercase leading-[1.1] tracking-[0.04em] text-white/90">{title}</div>
        {subtitle ? <div className="mt-1 text-[11px] font-medium text-white/60">{subtitle}</div> : null}
      </div>}
  </div>
}

/** Full-bleed backdrop layer: provider artwork, or a soft gradient derived from the title. */
export function Backdrop({ src, title, className }: { src?: string; title: string; className?: string }) {
  const [failed, setFailed] = React.useState<string | null>(null)
  const hue = titleHue(title)
  return <div aria-hidden className={cn("absolute inset-0 overflow-hidden", className)}>
    {src && failed !== src
      ? <Image src={src} alt="" fill priority sizes="100vw" onError={() => setFailed(src)} className="object-cover object-top" />
      : <div className="absolute inset-0" style={{ background: `radial-gradient(ellipse at 70% 20%, hsl(${hue} 45% 32% / .9), transparent 60%), radial-gradient(ellipse at 20% 80%, hsl(${(hue + 60) % 360} 40% 22% / .8), transparent 55%), var(--surface-2)` }} />}
    <div className="absolute inset-0 bg-gradient-to-r from-canvas via-canvas/70 to-transparent" />
    <div className="absolute inset-0 bg-gradient-to-t from-canvas via-canvas/10 to-transparent" />
  </div>
}
