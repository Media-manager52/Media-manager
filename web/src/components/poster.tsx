import { Clapperboard, Tv } from "lucide-react"
import { useState } from "react"

import { cx } from "@/components/ui"

/** Poster with a calm placeholder when artwork is missing or blocked. */
export function Poster({ src, title, kind, className }: { src?: string; title: string; kind?: string; className?: string }) {
  const [failed, setFailed] = useState<string | null>(null)
  const usable = src && failed !== src ? src : undefined
  const Icon = kind === "series" ? Tv : Clapperboard
  return (
    <div className={cx("relative aspect-[2/3] overflow-hidden rounded-xl bg-surface-3", className)}>
      {usable ? (
        <img src={usable} alt="" loading="lazy" decoding="async" onError={() => setFailed(usable)} className="size-full object-cover" />
      ) : (
        <div className="flex size-full flex-col items-center justify-center gap-2 p-3 text-center text-text-2">
          <Icon aria-hidden className="size-6" />
          <span className="line-clamp-3 text-xs font-medium text-text-2">{title}</span>
        </div>
      )}
    </div>
  )
}

/** TMDB artwork in a size suited to the slot, keeping other hosts unchanged. */
export function sizedArtwork(url: string | undefined, size: "w185" | "w342" | "w780" | "w1280"): string | undefined {
  if (!url) return undefined
  return url.replace(/^(https:\/\/image\.tmdb\.org\/t\/p\/)[^/]+\//, `$1${size}/`)
}
