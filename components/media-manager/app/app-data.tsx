"use client"

import * as React from "react"

import { loadAttentionWorkflows, loadCatalogMedia, loadManagedFiles, loadRequests, loadWanted, loadWorkflows, type APIMedia, type APIRequest, type APIWorkflow } from "@/lib/media-manager-api"
import { buildMediaLibraryRecords, type MediaLibraryRecord } from "@/lib/media-library"
import { awaitingApproval, titleStatus, type TitleStatus } from "@/lib/app-presentation"
import { useLiveData } from "../live-operational-screens"

export type AppTitle = MediaLibraryRecord & { status: TitleStatus }

/** One owner decision: a stopped workflow or a Seerr request awaiting approval. */
export type NeedsItem =
  | { kind: "workflow"; key: string; workflow: APIWorkflow; title?: AppTitle }
  | { kind: "approval"; key: string; request: APIRequest }

export type AppSnapshot = {
  media: APIMedia[]
  titles: AppTitle[]
  workflows: APIWorkflow[]
  requests: APIRequest[]
  needs: NeedsItem[]
}

/**
 * Reads the same bounded owner read models as the expert console. Active and
 * attention workflows are merged so every owner decision is present even when
 * it falls outside the active-list window.
 */
async function loadAppSnapshot(signal: AbortSignal): Promise<AppSnapshot> {
  const [media, files, wanted, active, attention, requests] = await Promise.all([
    loadCatalogMedia(signal), loadManagedFiles(signal), loadWanted(signal),
    loadWorkflows(true, signal), loadAttentionWorkflows(signal), loadRequests(signal),
  ])
  const byID = new Map<string, APIWorkflow>()
  for (const workflow of [...active, ...attention]) byID.set(workflow.id, workflow)
  const workflows = [...byID.values()]
  const records = (["movie", "series"] as const).flatMap((kind) => buildMediaLibraryRecords(kind, media, files, wanted, workflows))
  const titles = records.map<AppTitle>((record) => ({ ...record, status: titleStatus(record.workflows, record.available) }))
  const titleByID = new Map(titles.map((title) => [title.id, title]))
  const needs: NeedsItem[] = [
    ...requests.filter(awaitingApproval).map<NeedsItem>((request) => ({ kind: "approval", key: `request:${request.id}`, request })),
    ...titles.flatMap((title) => title.status.stage === "needs_you"
      ? title.workflows.filter((workflow) => titleStatus([workflow], false).stage === "needs_you").map<NeedsItem>((workflow) => ({ kind: "workflow", key: `workflow:${workflow.id}`, workflow, title }))
      : []),
  ]
  // A stopped workflow whose media record is outside the loaded catalog still needs the owner.
  for (const workflow of workflows) {
    if (titleStatus([workflow], false).stage !== "needs_you" || titleByID.has(workflow.media_id)) continue
    needs.push({ kind: "workflow", key: `workflow:${workflow.id}`, workflow })
  }
  return { media, titles, workflows, requests, needs }
}

type AppDataState = {
  data: AppSnapshot | null
  error: string
  refresh: () => void
  notice: string
  notify: (message: string) => void
  clearNotice: () => void
}

const AppDataContext = React.createContext<AppDataState>({ data: null, error: "", refresh: () => undefined, notice: "", notify: () => undefined, clearNotice: () => undefined })

export function AppDataProvider({ children }: { children: React.ReactNode }) {
  const { data, error, refresh } = useLiveData(loadAppSnapshot, { autoRefresh: true })
  const [notice, setNotice] = React.useState("")
  const value = React.useMemo<AppDataState>(() => ({ data, error, refresh, notice, notify: setNotice, clearNotice: () => setNotice("") }), [data, error, refresh, notice])
  return <AppDataContext.Provider value={value}>{children}</AppDataContext.Provider>
}

export function useAppData() {
  return React.useContext(AppDataContext)
}
