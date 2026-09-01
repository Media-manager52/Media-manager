"use client"

import * as React from "react"
import Image from "next/image"

import type { APIMetadataSource } from "@/lib/media-manager-api"

const simklMark = "https://us.simkl.in/img_favicon/v2/favicon-192x192.png"

export function MetadataArtwork({ src, fallback, alt, width, height, className }: { src?: string; fallback: string; alt: string; width: number; height: number; className?: string }) {
  const [failedSource, setFailedSource] = React.useState<string | null>(null)
  const selected = src && failedSource !== src ? src : fallback
  return <Image src={selected} alt={alt} width={width} height={height} onError={() => setFailedSource(selected)} className={className} />
}

export function MetadataSourceLink({ source }: { source: APIMetadataSource }) {
  const label = source.provider === "tvdb" ? "TheTVDB" : source.provider === "tmdb" ? "TMDB" : source.provider === "simkl" ? "Simkl" : source.provider.toUpperCase()
  const content = <>{source.provider === "simkl" ? <Image src={simklMark} alt="" width={14} height={14} className="size-3.5 rounded-[3px]" /> : null}<span>{label}{source.provider_id ? ` · ${source.provider_id}` : ""}{source.url ? " ↗" : ""}</span></>
  return source.url ? <a href={source.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-[#b7a4ed] underline-offset-2 hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[#7c3aed]">{content}</a> : <span className="inline-flex items-center gap-1 text-[#b7a4ed]">{content}</span>
}
