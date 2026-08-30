"use client"

import type { MediaRecord } from "@/lib/media-manager-data"

export const liveMode = process.env.NEXT_PUBLIC_MEDIA_MANAGER_MODE === "live"

export type APIIdentity = { namespace: string; value: string; state?: "verified" | "candidate" | "rejected"; source?: string }
export type APIMedia = { id: string; kind: "movie" | "series"; title: string; year?: number; identities: APIIdentity[] }
export type APIWorkflow = { id: string; media_id: string; media_kind: "movie" | "series"; title: string; year?: number; variant: string; state: string; revision: number; attention_code?: string; attention_detail?: string; terminal_at?: string; created_at: string; updated_at: string }
export type APIIntegration = { id: string; kind: string; name: string; enabled: boolean; is_default: boolean; config: Record<string, unknown>; secret_ref?: string }
export type APILibraryRoot = { id: string; media_kind: "movie" | "series"; name: string; destination_root: string; plex_visible_root: string; source_visible_root: string; plex_instance_id?: string; plex_library_id?: string; is_default: boolean; enabled: boolean }
export type APIQualityProfile = { id: string; media_kind: "movie" | "series"; name: string; policy_version: number; policy: Record<string, unknown>; upgrades_enabled: boolean; is_default: boolean; enabled: boolean }
export type APIRecycleEntry = { id: string; media_id: string; media_title: string; library_link_version_id: string; root_id: string; original_relative: string; recycle_relative: string; altmount_source_retained: boolean; purge_after: string; state: string; confirmation_required: boolean; created_at: string }
export type APIProwlarrCandidate = { id: string; import_id: string; name: string; implementation: string; base_url: string; categories: number[]; sanitized_config: Record<string, unknown>; secret_ref?: string; review_state: string; attention_detail?: string; created_instance_id?: string; api_key?: string }
export type APIProwlarrImport = { id: string; state: string; parser_version: string; created_at: string; completed_at?: string; candidates: APIProwlarrCandidate[] }
type Envelope<T> = { data: T }

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...init,
    headers: { Accept: "application/json", ...(init?.body ? { "Content-Type": "application/json" } : {}), ...init?.headers },
  })
  if (!response.ok) {
    let message = `Media Manager returned HTTP ${response.status}`
    try {
      const body = await response.json() as { error?: { message?: string } }
      if (body.error?.message) message = body.error.message
    } catch { /* Keep the bounded HTTP error. */ }
    throw new Error(message)
  }
  if (response.status === 204) return undefined as T
  const envelope = await response.json() as Envelope<T>
  return envelope.data
}

export async function loadMedia(signal?: AbortSignal): Promise<MediaRecord[]> {
	const items = await requestJSON<APIMedia[] | null>("/api/v1/media?limit=200", { signal }) ?? []
	return items.map((item) => ({
    id: item.id,
    kind: item.kind === "movie" ? "Movie" : "Series",
    title: item.title,
    year: item.year ?? 0,
    poster: item.kind === "movie" ? "/media/movie-placeholder.svg" : "/media/series-placeholder.svg",
    state: "Cataloged",
    profile: "Open workflow details",
    root: "Open workflow details",
    quality: "Persisted workflow state is shown in details",
    externalIds: (item.identities ?? []).map((identity) => `${identity.namespace.toUpperCase()} ${identity.value}`),
  }))
}

export const loadMediaItem = (id: string, signal?: AbortSignal) => requestJSON<APIMedia>(`/api/v1/media/${encodeURIComponent(id)}`, { signal })
export const loadWorkflows = async (active = true, signal?: AbortSignal) => await requestJSON<APIWorkflow[] | null>(`/api/v1/workflows?limit=300&active=${active}`, { signal }) ?? []
export const loadIntegrations = async (signal?: AbortSignal) => await requestJSON<APIIntegration[] | null>("/api/v1/settings/integrations", { signal }) ?? []
export const loadLibraryRoots = async (signal?: AbortSignal) => await requestJSON<APILibraryRoot[] | null>("/api/v1/settings/library-roots", { signal }) ?? []
export const loadQualityProfiles = async (signal?: AbortSignal) => await requestJSON<APIQualityProfile[] | null>("/api/v1/settings/quality-profiles", { signal }) ?? []
export const loadRecycle = async (signal?: AbortSignal) => await requestJSON<APIRecycleEntry[] | null>("/api/v1/recycle", { signal }) ?? []

export type CreateMediaInput = {
  kind: "movie" | "series"
  title: string
  year?: number
  identities: Array<APIIdentity & { state: "verified"; source: "manual" }>
  search_immediately: boolean
  root_id?: string
  quality_profile_id?: string
  download_client_id?: string
  seasons?: Array<{ number: number; monitored: boolean; search_immediately: boolean; prefer_season_pack: boolean; episodes: Array<{ number: number }> }>
}

export const createMedia = (input: CreateMediaInput) => requestJSON<{ media: APIMedia; workflows: APIWorkflow[] }>("/api/v1/media", { method: "POST", body: JSON.stringify(input) })
export const recycleMedia = (id: string) => requestJSON<APIRecycleEntry[]>(`/api/v1/media/${encodeURIComponent(id)}`, { method: "DELETE" })
export const restoreRecycle = (id: string) => requestJSON<{ status: string; id: string }>(`/api/v1/recycle/${encodeURIComponent(id)}/restore`, { method: "POST" })
export const createIntegration = (input: Omit<APIIntegration, "id">) => requestJSON<APIIntegration>("/api/v1/settings/integrations", { method: "POST", body: JSON.stringify(input) })
export const commissionIntegration = (id: string) => requestJSON<APIIntegration>(`/api/v1/settings/integrations/${encodeURIComponent(id)}/commission`, { method: "POST" })
export const createLibraryRoot = (input: Omit<APILibraryRoot, "id">) => requestJSON<APILibraryRoot>("/api/v1/settings/library-roots", { method: "POST", body: JSON.stringify(input) })
export const createQualityProfile = (input: Omit<APIQualityProfile, "id">) => requestJSON<APIQualityProfile>("/api/v1/settings/quality-profiles", { method: "POST", body: JSON.stringify(input) })
export const createExistingWorkflow = (mediaId: string, input: Record<string, unknown>) => requestJSON<Record<string, unknown>>(`/api/v1/media/${encodeURIComponent(mediaId)}/workflows`, { method: "POST", body: JSON.stringify(input) })
export const searchWorkflow = (id: string) => requestJSON<{ status: string; id: string }>(`/api/v1/workflows/${encodeURIComponent(id)}/search`, { method: "POST" })
export const cancelWorkflow = (id: string) => requestJSON<Record<string, unknown>>(`/api/v1/workflows/${encodeURIComponent(id)}/cancel`, { method: "POST" })
export const uploadProwlarrBackup = (file: File) => requestJSON<APIProwlarrImport>("/api/v1/imports/prowlarr", { method: "POST", headers: { "Content-Type": "application/zip" }, body: file })
export const approveProwlarrImport = (id: string, candidates: Array<{ id: string; secret_ref: string }>) => requestJSON<APIProwlarrImport>(`/api/v1/imports/prowlarr/${encodeURIComponent(id)}/approve`, { method: "POST", body: JSON.stringify({ candidates }) })
