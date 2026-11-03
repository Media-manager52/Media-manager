import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ChevronDown, ExternalLink, RefreshCw } from "lucide-react"
import { useMemo, useState } from "react"
import { Link, useParams } from "react-router"

import { loadMediaDetail, refreshMediaMetadata, type APIManagedFile, type APIMediaDetail, type APIMediaSeason, type APIWorkflow } from "@/api/client"
import { Poster, sizedArtwork } from "@/components/poster"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, CardHeader, cx, Dot, Empty, ErrorBox, Skeleton } from "@/components/ui"
import { WorkflowActions } from "@/components/workflow-actions"
import { formatBytes, formatDate, formatRelative } from "@/format"
import { attentionLabel, kindLabel, languageName, stateLabel, stateTone, translateDetail } from "@/i18n"
import { describeVariant, workflowHref } from "@/lib/links"
import { episodeStatus, rollupSeriesSeasons, type EpisodeStatus, type SeriesSeasonRollup } from "@/lib/series-variants"
import type { Tone } from "@/lib/tone"

export function TitlePage() {
  const { id = "" } = useParams()
  const detail = useQuery({ queryKey: ["media", id, "detail"], queryFn: ({ signal }) => loadMediaDetail(id, signal), refetchInterval: 60_000 })
  if (detail.error) return <ErrorBox error={detail.error} retry={() => detail.refetch()} />
  if (!detail.data) return <TitleSkeleton />
  return <TitleView detail={detail.data} />
}

function TitleView({ detail }: { detail: APIMediaDetail }) {
  const { media, workflows, seasons, files } = detail
  const meta = media.metadata
  const backdrop = sizedArtwork(meta?.backdrop_url, "w1280")
  const rating = meta?.ratings?.find((entry) => entry.source === "tmdb") ?? meta?.ratings?.[0]
  const facts = [
    media.year,
    kindLabel(media.kind),
    meta?.runtime_minutes ? `${meta.runtime_minutes} min` : null,
    meta?.original_language ? `Original: ${languageName(meta.original_language)}` : null,
    rating ? `★ ${rating.value.toFixed(1)}` : null,
  ].filter(Boolean)
  return (
    <>
      <div className="relative -mx-4 -mt-6 mb-8 overflow-hidden sm:-mx-6 lg:-mx-10 lg:-mt-10">
        {backdrop ? <img src={backdrop} alt="" className="absolute inset-0 size-full object-cover opacity-30" /> : null}
        <div className="absolute inset-0 bg-gradient-to-t from-bg via-bg/80 to-bg/40" />
        <div className="relative flex flex-col gap-6 px-4 pb-6 pt-8 sm:flex-row sm:px-6 lg:px-10 lg:pt-12">
          <Poster src={sizedArtwork(meta?.poster_url, "w342")} title={media.title} kind={media.kind} className="w-32 shrink-0 shadow-card sm:w-44" />
          <div className="min-w-0 self-end">
            <Link to={`/bibliotek?type=${media.kind === "series" ? "serier" : "film"}`} className="text-sm text-text-3 hover:underline">← Bibliotek</Link>
            <h1 className="mt-1 text-3xl font-semibold tracking-tight sm:text-4xl">{media.title}</h1>
            {meta?.original_title && meta.original_title !== media.title ? <div className="text-text-2">{meta.original_title}</div> : null}
            <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-sm text-text-2">{facts.map((fact) => <span key={String(fact)}>{fact}</span>)}</div>
            {meta?.genres?.length ? <div className="mt-3 flex flex-wrap gap-1.5">{meta.genres.map((genre) => <Badge key={genre}>{genre}</Badge>)}</div> : null}
            {meta?.overview ? <p className="mt-4 max-w-3xl text-text-2">{meta.overview}</p> : null}
            <div className="mt-4 flex flex-wrap items-center gap-3">
              <RefreshMetadata mediaId={media.id} />
              {uniqueSources(meta?.sources).map((source) => (
                <a key={`${source.provider}-${source.provider_id}`} href={source.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sm text-text-2 hover:text-text">
                  {providerName(source.provider)} <ExternalLink aria-hidden className="size-3.5" />
                </a>
              ))}
            </div>
          </div>
        </div>
      </div>
      {media.kind === "movie" ? <MovieStatus workflows={workflows} files={files} /> : <SeriesSeasons seasons={seasons} workflows={workflows} files={files} />}
      <FilesCard files={files} />
    </>
  )
}

function RefreshMetadata({ mediaId }: { mediaId: string }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const mutation = useMutation({
    mutationFn: () => refreshMediaMetadata(mediaId),
    onSuccess: async () => {
      toast("good", "Oplysningerne er hentet igen fra TMDB/TVDB.")
      await queryClient.invalidateQueries({ queryKey: ["media"] })
    },
    onError: (error) => toast("bad", error instanceof Error ? error.message : String(error)),
  })
  return <Button variant="secondary" busy={mutation.isPending} onClick={() => mutation.mutate()} className="h-8 text-xs"><RefreshCw aria-hidden className="size-3.5" />Opdatér oplysninger</Button>
}

function MovieStatus({ workflows, files }: { workflows: APIWorkflow[]; files: APIManagedFile[] }) {
  const current = [...workflows].sort((a, b) => b.updated_at.localeCompare(a.updated_at))
  const inPlex = files.some((file) => file.state === "current" && file.plex_confirmed)
  return (
    <Card className="mb-6">
      <CardHeader title="Status" description={inPlex ? "Filmen ligger i Plex." : "Filmen er ikke i Plex endnu."} />
      {current.length === 0 ? <Empty title="Intet arbejde for denne film" /> : null}
      <ul className="divide-y divide-line">
        {current.map((workflow) => <WorkflowRow key={workflow.id} workflow={workflow} />)}
      </ul>
    </Card>
  )
}

function WorkflowRow({ workflow }: { workflow: APIWorkflow }) {
  return (
    <li className="px-5 py-4">
      <div className="flex flex-wrap items-center gap-3">
        <Badge tone={stateTone(workflow.state)}>{stateLabel(workflow.state)}</Badge>
        <span className="text-sm text-text-2">{describeVariant(workflow)}{workflow.quality_profile ? ` · ${workflow.quality_profile}` : ""}</span>
        <span className="text-sm text-text-3">ændret {formatRelative(workflow.updated_at)}</span>
        <Link to={workflowHref(workflow)} className="ml-auto text-sm font-medium text-accent-strong hover:underline">Detaljer</Link>
      </div>
      {workflow.attention_code ? <p className="mt-2 text-sm text-warn">{attentionLabel(workflow.attention_code)}</p> : null}
      {workflow.attention_detail ? <p className="mt-1 break-words text-sm text-text-2">{translateDetail(workflow.attention_detail)}</p> : null}
      {workflow.terminal_at ? null : <div className="mt-3"><WorkflowActions workflow={workflow} compact /></div>}
    </li>
  )
}

const episodeTone: Record<EpisodeStatus, Tone> = { acquired: "good", acquiring: "info", attention: "warn", unaired: "neutral", undated: "neutral", missing: "bad" }
const episodeText: Record<EpisodeStatus, string> = { acquired: "I Plex", acquiring: "Hentes", attention: "Kræver handling", unaired: "Ikke sendt endnu", undated: "Ingen dato", missing: "Mangler" }

function SeriesSeasons({ seasons, workflows, files }: { seasons: APIMediaSeason[]; workflows: APIWorkflow[]; files: APIManagedFile[] }) {
  const rollups = useMemo(() => rollupSeriesSeasons(workflows, files), [workflows, files])
  const ordered = [...seasons].sort((a, b) => a.number - b.number)
  return (
    <Card className="mb-6">
      <CardHeader title="Sæsoner" description="Status for hver sæson og hvert afsnit." />
      {ordered.length === 0 ? <Empty title="Ingen sæsoner overvåges endnu" /> : null}
      <ul className="divide-y divide-line">
        {ordered.map((season) => <SeasonRow key={season.number} season={season} rollup={rollups.find((entry) => entry.number === season.number)} />)}
      </ul>
    </Card>
  )
}

function SeasonRow({ season, rollup }: { season: APIMediaSeason; rollup?: SeriesSeasonRollup }) {
  const [open, setOpen] = useState(false)
  const episodes = season.episodes ?? []
  const statuses = episodes.map((episode) => ({ episode, ...episodeStatus(episode, rollup) }))
  const acquired = statuses.filter((entry) => entry.status === "acquired").length
  const attention = statuses.filter((entry) => entry.status === "attention").length
  const total = season.expected_episodes || episodes.length
  const allDone = total > 0 && acquired >= total
  const tone: Tone = attention > 0 ? "warn" : allDone ? "good" : acquired > 0 ? "info" : "neutral"
  const pack = rollup?.packWorkflow
  return (
    <li>
      <button type="button" onClick={() => setOpen((value) => !value)} aria-expanded={open} className="flex w-full items-center gap-3 px-5 py-4 text-left hover:bg-surface-2">
        <Dot tone={tone} />
        <div className="min-w-0 flex-1">
          <div className="font-medium">{season.number === 0 ? "Specials" : `Sæson ${season.number}`}</div>
          <div className="text-sm text-text-2">
            {total > 0 ? `${acquired} af ${total} afsnit i Plex` : "Antal afsnit ukendt"}
            {attention > 0 ? ` · ${attention} kræver handling` : ""}
            {season.waiting_reason && !allDone ? ` · ${translateDetail(season.waiting_reason)}` : ""}
          </div>
        </div>
        {season.next_search_at && !allDone ? <span className="hidden text-xs text-text-3 sm:block">Næste søgning {formatRelative(season.next_search_at)}</span> : null}
        <ChevronDown aria-hidden className={cx("size-4 shrink-0 text-text-3 transition-transform", open && "rotate-180")} />
      </button>
      {open ? (
        <div className="border-t border-line bg-surface-2/50 px-5 py-4">
          {pack ? (
            <div className="mb-4 rounded-xl border border-line bg-surface p-3">
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span className="font-medium">Sæsonpakke</span>
                <Badge tone={stateTone(pack.state)}>{stateLabel(pack.state)}</Badge>
                {pack.attention_code ? <span className="text-warn">{attentionLabel(pack.attention_code)}</span> : null}
                <Link to={workflowHref(pack)} className="ml-auto font-medium text-accent-strong hover:underline">Detaljer</Link>
              </div>
              {pack.terminal_at ? null : <div className="mt-3"><WorkflowActions workflow={pack} compact /></div>}
            </div>
          ) : null}
          {statuses.length === 0 ? <p className="text-sm text-text-2">Afsnittene kendes ikke endnu.</p> : null}
          <ol className="grid grid-cols-1 gap-1.5 sm:grid-cols-2 xl:grid-cols-3">
            {statuses.map(({ episode, status, workflow }) => (
              <li key={episode.number} className="flex items-center gap-2 rounded-lg bg-surface px-3 py-2 text-sm">
                <span className="tabular w-7 shrink-0 text-text-3">{episode.number}</span>
                <span className="min-w-0 flex-1 truncate" title={episode.title}>{episode.title || `Afsnit ${episode.number}`}</span>
                {workflow ? (
                  <Link to={workflowHref(workflow)}><Badge tone={episodeTone[status]}>{episodeText[status]}</Badge></Link>
                ) : (
                  <Badge tone={episodeTone[status]}>{status === "unaired" && episode.air_date ? formatDate(episode.air_date) : episodeText[status]}</Badge>
                )}
              </li>
            ))}
          </ol>
        </div>
      ) : null}
    </li>
  )
}

function FilesCard({ files }: { files: APIManagedFile[] }) {
  const current = files.filter((file) => file.state === "current")
  if (current.length === 0) return null
  const sorted = [...current].sort((a, b) => a.destination_relative.localeCompare(b.destination_relative, "da", { numeric: true }))
  return (
    <Card>
      <CardHeader title="Filer i biblioteket" description={`${current.length} ${current.length === 1 ? "fil" : "filer"}`} />
      <ul className="divide-y divide-line">
        {sorted.slice(0, 200).map((file) => (
          <li key={file.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-5 py-3 text-sm">
            <Dot tone={file.plex_confirmed ? "good" : "info"} />
            <span className="min-w-0 flex-1 break-all">{file.destination_relative}</span>
            <span className="text-text-3">{file.root_name}</span>
            <span className="tabular text-text-3">{formatBytes(file.source_size_bytes)}</span>
          </li>
        ))}
      </ul>
    </Card>
  )
}

function providerName(provider: string): string {
  return { tmdb: "TMDB", tvdb: "TheTVDB", imdb: "IMDb", simkl: "Simkl" }[provider] ?? provider.toUpperCase()
}

function TitleSkeleton() {
  return (
    <div className="flex flex-col gap-6 sm:flex-row">
      <Skeleton className="aspect-[2/3] w-44" />
      <div className="flex-1 space-y-3 pt-10">
        <Skeleton className="h-9 w-2/3" />
        <Skeleton className="h-4 w-1/3" />
        <Skeleton className="h-20 w-full" />
      </div>
    </div>
  )
}

function uniqueSources<T extends { provider: string; url?: string }>(sources: T[] | undefined): T[] {
  const seen = new Set<string>()
  return (sources ?? []).filter((source) => {
    if (!source.url || seen.has(source.provider)) return false
    seen.add(source.provider)
    return true
  })
}
