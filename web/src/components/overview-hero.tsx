import { useQuery } from "@tanstack/react-query"
import { Activity, AlertTriangle, CalendarClock, CheckCircle2, ChevronRight, Film, Sparkles, Tv } from "lucide-react"
import type { ReactNode } from "react"
import { Link } from "react-router"

import { loadCalendar, loadRecentAdditions, type APIDashboard, type APIRecentAddition } from "@/api/client"
import { sizedArtwork } from "@/components/poster"
import { cx, Skeleton } from "@/components/ui"
import { formatNumber, formatRelative, formatWeekday } from "@/format"
import { describeVariant, titleHref } from "@/lib/links"
import { wideTiles, type Tile } from "@/lib/overview-tiles"

const episodeCode = (season: number, episode: number) => `S${String(season).padStart(2, "0")}E${String(episode).padStart(2, "0")}`

/**
 * The picture tiles and the four counts at the top of the overview: the next
 * episodes that air, the newest arrival and what needs the owner.
 */
export function OverviewHero({ counts }: { counts?: APIDashboard["counts"] }) {
  const recent = useQuery({ queryKey: ["media", "recent"], queryFn: ({ signal }) => loadRecentAdditions(signal), refetchInterval: 60_000 })
  const calendar = useQuery({ queryKey: ["calendar", "week"], queryFn: ({ signal }) => loadCalendar(signal) })
  const newest = recent.data?.[0]
  const tiles = wideTiles(
    calendar.data ?? [], recent.data ?? [], new Date(),
    (entry) => `${episodeCode(entry.season, entry.episode)}${entry.episode_title ? ` · ${entry.episode_title}` : ""} · ${formatWeekday(entry.air_date)}`,
    (item) => `${describeVariant(item)} · ${formatRelative(item.added_at)}`,
    titleHref,
  )
  const loading = recent.isPending || calendar.isPending
  return (
    <section aria-label="Lige nu" className="grid gap-3.5 lg:grid-cols-[1.55fr_1fr_1fr] lg:grid-rows-[13rem_9.5rem]">
      {loading ? (
        <>
          <Skeleton className="h-44 rounded-2xl lg:h-auto" />
          <Skeleton className="hidden rounded-2xl lg:row-start-2 lg:block" />
          <Skeleton className="hidden rounded-2xl lg:row-span-2 lg:block" />
        </>
      ) : (
        <>
          {tiles[0] ? <WideTile tile={tiles[0]} large /> : <QuietTile />}
          {tiles[1] ? <WideTile tile={tiles[1]} className="lg:row-start-2" /> : null}
          {newest ? <NewestTile item={newest} /> : null}
        </>
      )}
      <div className="grid grid-cols-2 gap-3.5 lg:col-start-3 lg:row-span-2 lg:row-start-1">
        <CountCard to="/handling" label="Kræver handling" icon={<AlertTriangle className="size-3.5" />} chip="bg-bad" value={counts?.needs_attention} emphasise={Boolean(counts?.needs_attention)} hint={counts ? (counts.needs_attention ? "Venter på en beslutning fra dig" : "Intet venter på dig") : undefined} />
        <CountCard to="/arbejde" label="Arbejde i gang" icon={<Activity className="size-3.5" />} chip="bg-[#0d9488]" value={counts?.active_workflows} hint="Søger, henter og tjekker" />
        <CountCard to="/bibliotek?type=film" label="Film" icon={<Film className="size-3.5" />} chip="bg-[#7c3aed]" value={counts?.movies} hint="I biblioteket" />
        <CountCard to="/bibliotek?type=serier" label="Serier" icon={<Tv className="size-3.5" />} chip="bg-[#2563eb]" value={counts?.series} hint="I biblioteket" />
      </div>
    </section>
  )
}

function Scrim() {
  return <div aria-hidden className="absolute inset-0 bg-gradient-to-t from-black/80 via-black/25 to-black/10" />
}

function Pill({ children }: { children: ReactNode }) {
  return <span className="absolute left-3.5 top-3.5 inline-flex items-center gap-1.5 rounded-full bg-black/55 px-2.5 py-1 text-[11px] font-semibold text-white backdrop-blur">{children}</span>
}

function WideTile({ tile, large = false, className }: { tile: Tile; large?: boolean; className?: string }) {
  const image = sizedArtwork(tile.image, "w780")
  return (
    <Link to={tile.href} className={cx("group relative block h-44 overflow-hidden rounded-2xl bg-[#1d1d1f] shadow-card lg:h-auto", className)}>
      {image ? <img src={image} alt="" loading="lazy" className="absolute inset-0 size-full object-cover transition-transform duration-500 group-hover:scale-[1.03]" /> : null}
      <Scrim />
      <Pill>{tile.label === "Næste afsnit" ? <CalendarClock aria-hidden className="size-3" /> : <Sparkles aria-hidden className="size-3" />}{tile.label}</Pill>
      <div className="absolute inset-x-4 bottom-3.5 text-white">
        <div className={cx("truncate font-bold tracking-tight", large ? "text-2xl" : "text-lg")}>{tile.title}</div>
        <div className="truncate text-[13px] text-white/85">{tile.detail}</div>
      </div>
    </Link>
  )
}

function NewestTile({ item }: { item: APIRecentAddition }) {
  const image = sizedArtwork(item.poster_url ?? item.backdrop_url, "w780")
  return (
    <Link to={titleHref(item.media_id)} className="group relative hidden overflow-hidden rounded-2xl bg-[#1d1d1f] shadow-card lg:row-span-2 lg:block">
      {image ? <img src={image} alt="" loading="lazy" className="absolute inset-0 size-full object-cover transition-transform duration-500 group-hover:scale-[1.03]" /> : null}
      <Scrim />
      <Pill><CheckCircle2 aria-hidden className="size-3" />Kom i biblioteket</Pill>
      <div className="absolute inset-x-4 bottom-4 text-white">
        <div className="line-clamp-2 text-[22px] font-bold leading-tight tracking-tight">{item.title}</div>
        <div className="truncate text-[13px] text-white/85">{describeVariant(item)} · {formatRelative(item.added_at)}</div>
      </div>
    </Link>
  )
}

/** Shown in place of the picture tiles while nothing airs and nothing has arrived yet. */
function QuietTile() {
  return (
    <div className="flex h-44 flex-col justify-end rounded-2xl border border-line bg-surface-2 p-5 lg:row-span-2 lg:h-auto">
      <div className="text-lg font-semibold">Stille lige nu</div>
      <div className="text-sm text-text-2">Ingen afsnit på vej i den kommende uge, og intet nyt er kommet i biblioteket endnu.</div>
    </div>
  )
}

function CountCard({ to, label, icon, chip, value, hint, emphasise = false }: { to: string; label: string; icon: ReactNode; chip: string; value?: number; hint?: string; emphasise?: boolean }) {
  return (
    <Link to={to} className="group flex min-w-0 flex-col gap-1.5 rounded-2xl border border-line bg-surface p-3.5 shadow-card transition-shadow hover:shadow-lift">
      <span className="flex items-center gap-2 text-[13px] font-medium">
        <span aria-hidden className={cx("flex size-[22px] shrink-0 items-center justify-center rounded-md text-white", chip)}>{icon}</span>
        <span className="min-w-0 flex-1 truncate">{label}</span>
        <ChevronRight aria-hidden className="size-3.5 shrink-0 text-text-3 transition-transform group-hover:translate-x-0.5" />
      </span>
      <span className={cx("tabular text-3xl font-bold leading-none tracking-tight", emphasise && "text-bad")}>{value === undefined ? <span aria-hidden className="inline-block h-8 w-16 animate-pulse rounded-lg bg-surface-3" /> : formatNumber(value)}</span>
      {hint ? <span className="line-clamp-2 text-xs text-text-3">{hint}</span> : null}
    </Link>
  )
}
