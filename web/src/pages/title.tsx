import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ChevronDown, RefreshCw } from "lucide-react"
import { useMemo, useState } from "react"
import { Link, useParams } from "react-router"

import { isNotFound, loadMediaDetail, refreshMediaMetadata, type APIManagedFile, type APIMediaDetail, type APIMediaSeason, type APIWorkflow } from "@/api/client"
import { ReplaceFile } from "@/components/replace-file"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, CardHeader, cx, Dot, Empty, ErrorBox, Skeleton } from "@/components/ui"
import { TitleActions } from "@/components/title-actions"
import { CastRow, TitleAbout, TitleHero } from "@/components/title-hero"
import { WorkflowActions } from "@/components/workflow-actions"
import { formatBytes, formatDate, formatRelative } from "@/format"
import { attentionLabel, errorMessage, stateLabel, stateTone, translateDetail, workflowDisplayState } from "@/i18n"
import { fileLabel, shortLanguages } from "@/lib/file-label"
import { fileQuality } from "@/lib/file-quality"
import { describeVariant, workflowHref } from "@/lib/links"
import { episodeStatus, rollupSeriesSeasons, type EpisodeStatus, type SeriesSeasonRollup } from "@/lib/series-variants"
import type { Tone } from "@/lib/tone"

export function TitlePage() {
  const { id = "" } = useParams()
  const detail = useQuery({ queryKey: ["media", id, "detail"], queryFn: ({ signal }) => loadMediaDetail(id, signal), refetchInterval: 60_000 })
  if (isNotFound(detail.error)) return <Card><Empty title="Titlen findes ikke længere">Den kan være slettet, eller linket kan være gammelt. <Link to="/bibliotek" className="font-medium text-accent-strong hover:underline">Gå til Bibliotek</Link></Empty></Card>
  if (detail.error) return <ErrorBox error={detail.error} retry={() => detail.refetch()} />
  if (!detail.data) return <TitleSkeleton />
  return <TitleView detail={detail.data} />
}

function TitleView({ detail }: { detail: APIMediaDetail }) {
  const { media, workflows, seasons, files } = detail
  const meta = media.metadata
  return (
    <>
      <TitleHero media={media} />
      <div className="mt-4 grid grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:items-center">
        <TitleActions detail={detail} />
        <RefreshMetadata mediaId={media.id} />
      </div>
      <TitleAbout media={media} overview={meta?.overview ? <Overview text={meta.overview} /> : undefined} />
      <CastRow credits={meta?.credits} />
      {media.kind === "movie" ? <MovieStatus workflows={workflows} files={files} /> : <SeriesSeasons seasons={seasons} workflows={workflows} files={files} />}
      <FilesCard files={files} />
    </>
  )
}

/** The plot, kept to a few lines on a phone until the reader asks for the rest. */
function Overview({ text }: { text: string }) {
  const [open, setOpen] = useState(false)
  const long = text.length > 220
  return (
    <div className="max-w-3xl text-[15px] leading-relaxed text-text-2">
      <p className={cx(long && !open && "line-clamp-4 sm:line-clamp-none")}>{text}</p>
      {long ? <button type="button" aria-expanded={open} onClick={() => setOpen((value) => !value)} className="mt-1 text-sm font-medium text-accent-strong hover:underline sm:hidden">{open ? "Vis mindre" : "Læs mere"}</button> : null}
    </div>
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
    onError: (error) => toast("bad", errorMessage(error)),
  })
  return <Button variant="secondary" busy={mutation.isPending} onClick={() => mutation.mutate()} className="h-10 text-xs sm:h-8"><RefreshCw aria-hidden className="size-3.5" />Opdatér oplysninger</Button>
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
        <Badge tone={stateTone(workflowDisplayState(workflow))}>{stateLabel(workflowDisplayState(workflow))}</Badge>
        <span className="text-sm text-text-2">{describeVariant(workflow)}{workflow.quality_profile ? ` · ${workflow.quality_profile}` : ""}</span>
        <span className="text-sm text-text-3">ændret {formatRelative(workflow.updated_at)}</span>
        <Link to={workflowHref(workflow)} className="ml-auto text-sm font-medium text-accent-strong hover:underline">Detaljer</Link>
      </div>
      {workflow.attention_code ? <p className="mt-2 text-sm text-warn">{attentionLabel(workflow.attention_code)}</p> : null}
      {workflow.attention_detail ? <p className="mt-1 break-words text-sm text-text-2">{translateDetail(workflow.attention_detail)}</p> : null}
      {hasActions(workflow) ? <div className="mt-3"><WorkflowActions workflow={workflow} compact /></div> : null}
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
  const waitingReason = season.waiting_reason && !allDone ? translateDetail(season.waiting_reason) : ""
  return (
    <li>
      <button type="button" onClick={() => setOpen((value) => !value)} aria-expanded={open} className="flex w-full items-center gap-3 px-5 py-4 text-left hover:bg-surface-2">
        <Dot tone={tone} />
        <div className="min-w-0 flex-1">
          <div className="font-medium">{season.number === 0 ? "Specials" : `Sæson ${season.number}`}</div>
          <div className="text-sm text-text-2">
            {total > 0 ? `${acquired} af ${total} afsnit i Plex` : "Antal afsnit ukendt"}
            {attention > 0 ? ` · ${attention} kræver handling` : ""}
          </div>
          {waitingReason && !open ? <div className="truncate text-xs text-text-3">{waitingReason}</div> : null}
        </div>
        {season.next_search_at && !allDone ? <span className="hidden text-xs text-text-3 sm:block">Næste søgning {formatRelative(season.next_search_at)}</span> : null}
        <ChevronDown aria-hidden className={cx("size-4 shrink-0 text-text-3 transition-transform", open && "rotate-180")} />
      </button>
      {open ? (
        <div className="border-t border-line bg-surface-2/50 px-5 py-4">
          {waitingReason ? <p className="mb-3 text-sm text-text-2">{waitingReason}</p> : null}
          {pack ? (
            <div className="mb-4 rounded-xl border border-line bg-surface px-3 py-2.5">
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span className="font-medium">Sæsonpakke</span>
                <Badge tone={stateTone(workflowDisplayState(pack))}>{stateLabel(workflowDisplayState(pack))}</Badge>
                {pack.attention_code ? <span className="text-warn">{attentionLabel(pack.attention_code)}</span> : null}
                <Link to={workflowHref(pack)} className="ml-auto font-medium text-accent-strong hover:underline">Detaljer</Link>
              </div>
              {hasActions(pack) ? <div className="mt-3"><WorkflowActions workflow={pack} compact /></div> : null}
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
        {sorted.slice(0, 200).map((file) => <FileRow key={file.id} file={file} startOpen={current.length === 1} />)}
      </ul>
    </Card>
  )
}

/** One library file: a short name and what was measured, with the path and the repair action folded away. */
function FileRow({ file, startOpen }: { file: APIManagedFile; startOpen: boolean }) {
  const [open, setOpen] = useState(startOpen)
  const quality = fileQuality(file)
  return (
    <li className="text-sm">
      <button type="button" onClick={() => setOpen((value) => !value)} aria-expanded={open} className="flex w-full items-center gap-3 px-5 py-3 text-left hover:bg-surface-2">
        <Dot tone={file.plex_confirmed ? "good" : "info"} />
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-3">
            <span className="min-w-0 flex-1 truncate font-medium">{fileLabel(file.destination_relative)}</span>
            <span className="tabular shrink-0 text-xs text-text-3">{formatBytes(file.source_size_bytes)}</span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-text-2">
            {quality.resolution ? <Badge tone={quality.resolution === "4K" ? "info" : "neutral"}>{quality.resolution}</Badge> : null}
            {quality.video ? <Badge>{quality.video}</Badge> : null}
            {quality.audio.length ? <span>Tale: {shortLanguages(quality.audio)}</span> : null}
            {quality.subtitles.length ? <span>Tekster: {shortLanguages(quality.subtitles)}</span> : null}
            {file.plex_confirmed ? null : <span className="text-text-3">Venter på Plex</span>}
          </div>
        </div>
        <ChevronDown aria-hidden className={cx("size-4 shrink-0 text-text-3 transition-transform", open && "rotate-180")} />
      </button>
      {open ? (
        <div className="space-y-3 border-t border-line bg-surface-2/50 px-5 py-4">
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs">
            <dt className="text-text-3">Bibliotek</dt>
            <dd>{file.root_name}</dd>
            {quality.audio.length > 2 ? <><dt className="text-text-3">Tale</dt><dd>{quality.audio.join(", ")}</dd></> : null}
            {quality.subtitles.length > 2 ? <><dt className="text-text-3">Tekster</dt><dd>{quality.subtitles.join(", ")}</dd></> : null}
            <dt className="text-text-3">Filsti</dt>
            <dd className="break-all font-mono text-text-2">{file.destination_relative}</dd>
          </dl>
          <ReplaceFile file={file} />
        </div>
      ) : null}
    </li>
  )
}

/** True when the server offers at least one action the owner can take now. */
function hasActions(workflow: APIWorkflow): boolean {
  return !workflow.terminal_at && (workflow.available_actions ?? []).some((action) => action.available)
}

function TitleSkeleton() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-4 w-40" />
      <Skeleton className="h-60 rounded-2xl sm:h-[21rem] lg:h-[25rem]" />
      <Skeleton className="h-8 w-72" />
      <Skeleton className="h-20 w-full max-w-3xl" />
    </div>
  )
}
