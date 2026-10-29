"use client"

import * as React from "react"
import Image from "next/image"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ChevronLeft, ChevronRight, CircleAlert, Check } from "lucide-react"

import { loadMediaDetail, type APIManagedFile, type APIMediaDetail, type APIMediaSeason } from "@/lib/media-manager-api"
import { attentionTitle, languageName, offeredActions, posterTone, relativeTime, stageInfo, titleStatus, waitingDetail, workflowStage } from "@/lib/app-presentation"
import { useLiveData } from "../live-operational-screens"
import { useAppData } from "./app-data"
import { AppShell } from "./shell"
import { consoleHref, kindLabel, LoadState, Poster, ProgressSteps, StatusPill, WorkflowActions } from "./parts"

function facts(file: APIManagedFile, key: string): string[] {
  const nested = file.quality_evidence?.facts
  const source = nested && typeof nested === "object" && !Array.isArray(nested) ? nested as Record<string, unknown> : file.quality_evidence ?? {}
  const value = source[key]
  if (Array.isArray(value)) return value.filter((item): item is string => typeof item === "string" && item !== "")
  return typeof value === "string" && value ? [value] : []
}

function unique(values: string[]) {
  return [...new Set(values)]
}

function sizeText(bytes?: number) {
  if (!bytes) return "Unknown"
  return bytes >= 1e9 ? `${(bytes / 1e9).toFixed(1)} GB` : `${Math.round(bytes / 1e6)} MB`
}

function runtimeText(minutes?: number) {
  if (!minutes) return ""
  const hours = Math.floor(minutes / 60)
  return hours ? `${hours} h ${minutes % 60} min` : `${minutes} min`
}

function seasonState(season: APIMediaSeason) {
  if (season.state === "available" || season.workflow_state === "plex_confirmed") return stageInfo("in_plex")
  if (!season.workflow_state) return season.monitored ? stageInfo("waiting") : { ...stageInfo("not_started"), label: "Not requested" }
  return stageInfo(workflowStage({ state: season.workflow_state }))
}

function TitleContent({ id }: { id: string }) {
  const router = useRouter()
  const snapshot = useAppData()
  const loader = React.useCallback((signal: AbortSignal) => loadMediaDetail(id, signal), [id])
  const { data: detail, error } = useLiveData<APIMediaDetail>(loader, { autoRefresh: true })
  const [backdropFailed, setBackdropFailed] = React.useState(false)
  if (!detail) return <div className="mm-stack-tight"><h1 className="mm-sr">Title</h1><LoadState error={error} loading={!error} label="Loading this title…" /></div>

  const media = detail.media
  const metadata = media.metadata
  // The list read model carries available_actions; the detail model does not.
  const listed = new Map((snapshot.data?.workflows ?? []).filter((workflow) => workflow.media_id === media.id).map((workflow) => [workflow.id, workflow]))
  const workflows = detail.workflows.filter((workflow) => !workflow.terminal_at || workflow.state === "plex_confirmed").map((workflow) => listed.get(workflow.id) ?? workflow)
  const currentFiles = detail.files.filter((file) => file.state === "current")
  const inPlex = currentFiles.some((file) => file.plex_confirmed)
  const status = titleStatus(workflows, inPlex)
  const focus = status.workflow
  const actions = offeredActions(focus)
  const statusDetail = status.stage === "in_plex"
    ? (() => { const confirmed = currentFiles.map((file) => file.verified_at ?? "").sort().at(-1); return confirmed ? `Added ${relativeTime(confirmed)}` : "" })()
    : status.stage === "waiting" ? waitingDetail(focus) : focus ? `Updated ${relativeTime(focus.updated_at)}` : ""
  const meta = [kindLabel(media.kind), media.year ? String(media.year) : "", runtimeText(metadata?.runtime_minutes), (metadata?.genres ?? []).slice(0, 3).join(", ")].filter(Boolean).join(" · ")
  const original = metadata?.original_title && metadata.original_title !== media.title ? metadata.original_title : ""
  const have = unique([
    ...unique(currentFiles.flatMap((file) => facts(file, "resolution"))),
    ...unique(currentFiles.flatMap((file) => facts(file, "audio_languages"))).map((code) => `${languageName(code)} audio`),
    ...unique(currentFiles.flatMap((file) => facts(file, "subtitle_languages"))).map((code) => `${languageName(code)} subtitles`),
    ...(inPlex ? ["Confirmed in Plex"] : []),
  ])
  const requests = (snapshot.data?.requests ?? []).filter((request) => request.media_id === media.id && request.requester)
  const firstRequest = requests.sort((a, b) => (a.requested_at ?? "").localeCompare(b.requested_at ?? ""))[0]
  const back = () => { if (window.history.length > 1) router.back(); else router.push("/library") }

  return <div className="mm-stack-tight" style={{ gap: 28 }}>
    <button type="button" className="mm-btn mm-btn-small mm-back" onClick={back}><ChevronLeft size={18} aria-hidden />Back</button>
    <section className="mm-hero" style={{ background: posterTone(media.title) }} aria-label={media.title}>
      {metadata?.backdrop_url && !backdropFailed ? <Image src={metadata.backdrop_url} alt="" fill sizes="(max-width: 1320px) 100vw, 1320px" className="mm-hero-backdrop" onError={() => setBackdropFailed(true)} /> : null}
      <div className="mm-hero-poster"><Poster title={media.title} year={media.year} src={metadata?.poster_url} sizes="200px" /></div>
      <div className="mm-hero-info">
        <div className="mm-hero-meta">{meta}</div>
        <h1 className="mm-hero-title">{media.title}</h1>
        {original ? <div style={{ fontSize: 17, opacity: 0.9 }}>{original}</div> : null}
        <div className="mm-actions"><StatusPill label={status.label} tone={status.tone} />{statusDetail ? <span className="mm-small">{statusDetail}</span> : null}</div>
        <WorkflowActions actions={actions} title={media.title} />
      </div>
    </section>

    {status.stage === "needs_you" && focus ? <div className="mm-note" role="note"><CircleAlert size={22} color="#FFC27A" aria-hidden /><div><strong>{attentionTitle(focus)}</strong>{focus.attention_detail ? <div className="mm-needs-detail">{focus.attention_detail}</div> : null}{!actions.length ? <div style={{ marginTop: 10 }}><Link className="mm-btn mm-btn-small" href={consoleHref(media.kind, media.id)}>See all options</Link></div> : null}</div></div> : null}

    <div className="mm-columns">
      <div>
        <section className="mm-card" aria-labelledby="mm-progress"><h2 id="mm-progress" className="mm-h3">Progress</h2><ProgressSteps status={status} /></section>
        {metadata?.overview || metadata?.tagline ? <section aria-labelledby="mm-about"><h2 id="mm-about" className="mm-h3" style={{ marginBottom: 8 }}>About</h2>{metadata?.tagline ? <p className="mm-prose" style={{ fontStyle: "italic", marginBottom: 8 }}>{metadata.tagline}</p> : null}{metadata?.overview ? <p className="mm-prose">{metadata.overview}</p> : null}</section> : null}
        {media.kind === "series" && detail.seasons.length ? <section className="mm-section" aria-labelledby="mm-seasons"><h2 id="mm-seasons" className="mm-h3">Seasons</h2><div className="mm-list">{detail.seasons.map((season) => {
          const state = seasonState(season)
          return <div key={season.number} className="mm-list-row"><span>Season {season.number}</span><span className="mm-small mm-muted">{season.expected_episodes} {season.expected_episodes === 1 ? "episode" : "episodes"}</span><span className="mm-small" data-tone={state.tone} style={{ fontWeight: 600 }}>{state.label}</span>{season.waiting_reason && state.stage !== "in_plex" ? <span className="mm-small mm-muted" style={{ flexBasis: "100%" }}>{season.waiting_reason}</span> : null}</div>
        })}</div></section> : null}
      </div>
      <div>
        {have.length ? <section className="mm-card" aria-labelledby="mm-have"><h2 id="mm-have" className="mm-h3">What you have</h2><div className="mm-have">{have.map((item) => <span key={item}><Check size={14} color="#8FE0AE" strokeWidth={3} aria-hidden />{item}</span>)}</div></section> : null}
        {firstRequest ? <p className="mm-small mm-muted" style={{ margin: "0 4px" }}>Asked for by {firstRequest.requester}{firstRequest.requested_at ? ` ${relativeTime(firstRequest.requested_at)}` : ""} through Seerr</p> : null}
        <details className="mm-expert">
          <summary>Details for experts<ChevronRight size={18} aria-hidden /></summary>
          <div className="mm-expert-body">
            <dl>
              {currentFiles.map((file) => <React.Fragment key={file.id}>
                <div><dt>File in Plex</dt><dd>{file.plex_visible_target}</dd></div>
                <div><dt>Size · quality setting</dt><dd>{sizeText(file.source_size_bytes)} · {file.quality_profile}</dd></div>
              </React.Fragment>)}
              {workflows.filter((workflow) => workflow.state !== "plex_confirmed").map((workflow) => <React.Fragment key={workflow.id}>
                <div><dt>Workflow state{workflow.variant !== "movie" ? ` · ${workflow.variant}` : ""}</dt><dd>{workflow.state}{workflow.attention_code ? ` · ${workflow.attention_code}` : ""}</dd></div>
                {workflow.round_attempt_limit ? <div><dt>Copies tried this round</dt><dd>{workflow.round_slots_used ?? 0} of {workflow.round_attempt_limit}</dd></div> : null}
              </React.Fragment>)}
              {media.identities.length ? <div><dt>Identity</dt><dd>{media.identities.map((identity) => `${identity.namespace}:${identity.value}`).join(" · ")}</dd></div> : null}
            </dl>
            <Link className="mm-btn mm-btn-small" style={{ marginTop: 12 }} href={consoleHref(media.kind, media.id)}>Open in the expert console</Link>
          </div>
        </details>
      </div>
    </div>
  </div>
}

export function AppTitlePage({ id }: { id: string }) {
  return <AppShell section="library"><TitleContent id={id} /></AppShell>
}
