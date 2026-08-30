"use client"

import type { MediaRecord } from "@/lib/media-manager-data"

export const liveMode = process.env.NEXT_PUBLIC_MEDIA_MANAGER_MODE === "live"

export type APIIdentity = { namespace: string; value: string; state?: "verified" | "candidate" | "rejected"; source?: string }
export type APIMedia = { id: string; kind: "movie" | "series"; title: string; year?: number; identities: APIIdentity[] }
export type APIWorkflow = { id: string; media_id: string; media_kind: "movie" | "series"; title: string; year?: number; variant: string; state: string; revision: number; attention_code?: string; attention_detail?: string; terminal_at?: string; created_at: string; updated_at: string }
export type APIIntegration = { id: string; kind: string; name: string; enabled: boolean; is_default: boolean; config: Record<string, unknown>; secret_ref?: string; created_at: string; updated_at: string }
export type APILibraryRoot = { id: string; media_kind: "movie" | "series"; name: string; destination_root: string; plex_visible_root: string; source_visible_root: string; plex_instance_id?: string; plex_library_id?: string; is_default: boolean; enabled: boolean }
export type APIQualityProfile = { id: string; media_kind: "movie" | "series"; name: string; policy_version: number; policy: Record<string, unknown>; upgrades_enabled: boolean; is_default: boolean; enabled: boolean }
export type APIRecycleEntry = { id: string; media_id: string; media_title: string; library_link_version_id: string; root_id: string; original_relative: string; recycle_relative: string; altmount_source_retained: boolean; purge_after: string; state: string; confirmation_required: boolean; created_at: string }
export type APIProwlarrCandidate = { id: string; import_id: string; name: string; implementation: string; base_url: string; categories: number[]; sanitized_config: Record<string, unknown>; secret_ref?: string; review_state: string; attention_detail?: string; created_instance_id?: string; api_key?: string }
export type APIProwlarrImport = { id: string; state: string; parser_version: string; created_at: string; completed_at?: string; candidates: APIProwlarrCandidate[] }
export type APIActivity = { id: string; kind: "audit" | "workflow_transition"; actor: string; action: string; aggregate_type: string; aggregate_id?: string; detail: Record<string, unknown>; occurred_at: string }
export type APIDashboard = {
  counts: { movies: number; series: number; active_workflows: number; needs_attention: number; current_files: number; recycle_entries: number; pending_events: number }
  recent_workflows: APIWorkflow[]
  recent_activity: APIActivity[]
  generated_at: string
}
export type APIManagedFile = { id: string; media_id: string; media_kind: "movie" | "series"; title: string; year?: number; root_name: string; destination_relative: string; source_relative: string; plex_visible_target: string; state: string; generation: number; plex_confirmed: boolean; verified_at?: string; displaced_at?: string; removed_at?: string }
export type APIReleaseCandidate = { id: string; workflow_id: string; media_id: string; media_kind: "movie" | "series"; media_title: string; media_year?: number; variant: string; title: string; indexer_id: string; indexer_name: string; size_bytes?: number; published_at?: string; verdict?: string; score?: number; reasons: unknown[]; normalized_facts: Record<string, unknown>; problems: unknown[]; selected: boolean; observed_at: string }
export type APISourceStatus = APIIntegration & { configuration_state: "configured" | "disabled" | "awaiting_commission"; minimum_interval_ms?: number; next_allowed_at?: string; cooldown_until?: string; consecutive_failure: number; last_outcome?: string; budget_updated_at?: string }
export type APISystemHealth = { database_state: string; database_latency_ms: number; schema_version: number; expected_schema: number; outbox: { pending: number; due: number; retrying: number; oldest_due_at?: string }; components: APISourceStatus[]; checked_at: string }
export type APIMediaSeason = { number: number; monitored: boolean; prefer_season_pack: boolean; search_immediately: boolean; state: string; expected_episodes: number; workflow_id?: string; workflow_state?: string; updated_at: string }
export type APIMediaDetail = { media: APIMedia; workflows: APIWorkflow[]; seasons: APIMediaSeason[]; files: APIManagedFile[] }
export type APIWantedItem = { workflow_id: string; media_id: string; media_kind: "movie" | "series"; title: string; year?: number; variant: string; gap: "missing" | "upgrade"; current_state: string; target_profile: string; upgrades_enabled: boolean; attention_detail?: string; updated_at: string }
export type APITaskStatus = { event_type: string; pending: number; due: number; retrying: number; completed: number; highest_attempts: number; oldest_due_at?: string; latest_at: string }
export type APITasks = { event_types: APITaskStatus[]; recurring: Array<{ name: string; schedule: string; automatic: boolean }> }
export type APISystemInfo = { version: string; owner_only: boolean; browser_auth: string; api_auth: string; seerr_webhook_enabled: boolean; altmount_source_policy: string; publication_type: string; upgrade_replacement_gate: string; recycle_retention_days: number; public_edge_owner: string; machine_api_keys: boolean; api_prefix: string; checked_at: string }
export type APIDiscoverySearchResult = { id: number; mediaType: "movie" | "tv"; title?: string; name?: string; releaseDate?: string; firstAirDate?: string; overview?: string; posterPath?: string }
export type APIDiscoverySearchPage = { page: number; totalPages: number; totalResults: number; results: APIDiscoverySearchResult[] }
export type APIDiscoverySeason = { number: number; episodes: Array<{ number: number; provider_id: string; title: string; air_date: string }> }
export type APIDiscoveryDetail = { kind: "movie" | "series"; title: string; year?: number; identities: APIIdentity[]; tmdb_id: number; season?: APIDiscoverySeason }
export type APIReleaseBlock = { id: string; scope: "native_release" | "pack_release"; indexer_instance_id?: string; indexer_name?: string; media_id?: string; media_title?: string; variant?: string; native_key?: string; release_title: string; reason: string; expires_at?: string; created_at: string; created_by: string }
export type APIAdoptionItem = { id: string; batch_id: string; original_relative: string; proposed_relative?: string; media_id?: string; root_id?: string; quality_profile_id?: string; monitoring_enabled?: boolean; search_immediately?: boolean; recognized_companions: string[]; unknown_companions: string[]; review_state: "pending" | "approved" | "rejected" | "needs_attention"; execution_state: string }
export type APIAdoptionBatch = { id: string; source_root: string; media_kind: "movie" | "series"; default_root_id: string; default_quality_profile_id: string; monitoring_enabled: boolean; search_immediately: boolean; state: string; created_at: string; approved_at?: string; completed_at?: string; pending_items: number; approved_items: number; rejected_items: number; attention_items: number; items?: APIAdoptionItem[] }
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
	const items = await loadCatalogMedia(signal)
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

export const loadCatalogMedia = async (signal?: AbortSignal) => await requestJSON<APIMedia[] | null>("/api/v1/media?limit=500", { signal }) ?? []
export const loadMediaItem = (id: string, signal?: AbortSignal) => requestJSON<APIMedia>(`/api/v1/media/${encodeURIComponent(id)}`, { signal })
export const loadWorkflows = async (active = true, signal?: AbortSignal) => await requestJSON<APIWorkflow[] | null>(`/api/v1/workflows?limit=300&active=${active}`, { signal }) ?? []
export const loadIntegrations = async (signal?: AbortSignal) => await requestJSON<APIIntegration[] | null>("/api/v1/settings/integrations", { signal }) ?? []
export const loadLibraryRoots = async (signal?: AbortSignal) => await requestJSON<APILibraryRoot[] | null>("/api/v1/settings/library-roots", { signal }) ?? []
export const loadQualityProfiles = async (signal?: AbortSignal) => await requestJSON<APIQualityProfile[] | null>("/api/v1/settings/quality-profiles", { signal }) ?? []
export const loadRecycle = async (signal?: AbortSignal) => await requestJSON<APIRecycleEntry[] | null>("/api/v1/recycle", { signal }) ?? []
export const loadDashboard = (signal?: AbortSignal) => requestJSON<APIDashboard>("/api/v1/dashboard", { signal })
export const loadActivity = async (signal?: AbortSignal) => await requestJSON<APIActivity[] | null>("/api/v1/activity?limit=300", { signal }) ?? []
export const loadManagedFiles = async (signal?: AbortSignal) => await requestJSON<APIManagedFile[] | null>("/api/v1/files?limit=300", { signal }) ?? []
export const loadManagedFile = (id: string, signal?: AbortSignal) => requestJSON<APIManagedFile>(`/api/v1/files/${encodeURIComponent(id)}`, { signal })
export const loadReleaseCandidates = async (signal?: AbortSignal) => await requestJSON<APIReleaseCandidate[] | null>("/api/v1/intelligence/releases?limit=300", { signal }) ?? []
export const loadSources = async (signal?: AbortSignal) => await requestJSON<APISourceStatus[] | null>("/api/v1/sources", { signal }) ?? []
export const loadSystemHealth = (signal?: AbortSignal) => requestJSON<APISystemHealth>("/api/v1/system/health", { signal })
export const loadMediaDetail = (id: string, signal?: AbortSignal) => requestJSON<APIMediaDetail>(`/api/v1/media/${encodeURIComponent(id)}/detail`, { signal })
export const loadWanted = async (signal?: AbortSignal) => await requestJSON<APIWantedItem[] | null>("/api/v1/wanted?limit=300", { signal }) ?? []
export const loadTasks = (signal?: AbortSignal) => requestJSON<APITasks>("/api/v1/system/tasks", { signal })
export const loadSystemInfo = (signal?: AbortSignal) => requestJSON<APISystemInfo>("/api/v1/system/info", { signal })
export const searchMetadata = (query: string, page = 1, signal?: AbortSignal) => requestJSON<APIDiscoverySearchPage>(`/api/v1/discovery/search?query=${encodeURIComponent(query)}&page=${page}`, { signal })
export const loadDiscoveryMetadata = (kind: "movie" | "tv", tmdbID: number, season?: number, signal?: AbortSignal) => requestJSON<APIDiscoveryDetail>(`/api/v1/discovery/${kind}/${tmdbID}${season === undefined ? "" : `?season=${season}`}`, { signal })
export const loadReleaseBlocks = async (signal?: AbortSignal) => await requestJSON<APIReleaseBlock[] | null>("/api/v1/blocklist?limit=300", { signal }) ?? []
export const loadAdoptionBatches = async (signal?: AbortSignal) => await requestJSON<APIAdoptionBatch[] | null>("/api/v1/adoption?limit=100", { signal }) ?? []
export const loadAdoptionBatch = (id: string, signal?: AbortSignal) => requestJSON<APIAdoptionBatch>(`/api/v1/adoption/${encodeURIComponent(id)}`, { signal })

export type CreateMediaInput = {
  kind: "movie" | "series"
  title: string
  year?: number
  identities: Array<APIIdentity & { state: "verified"; source: "manual" | "seerr_metadata" }>
  search_immediately: boolean
  root_id?: string
  quality_profile_id?: string
  download_client_id?: string
  seasons?: Array<{ number: number; monitored: boolean; search_immediately: boolean; prefer_season_pack: boolean; episodes: Array<{ number: number }> }>
}

export const createMedia = (input: CreateMediaInput) => requestJSON<{ media: APIMedia; workflows: APIWorkflow[] }>("/api/v1/media", { method: "POST", body: JSON.stringify(input) })
export const recycleMedia = (id: string) => requestJSON<APIRecycleEntry[]>(`/api/v1/media/${encodeURIComponent(id)}`, { method: "DELETE" })
export const restoreRecycle = (id: string) => requestJSON<{ status: string; id: string }>(`/api/v1/recycle/${encodeURIComponent(id)}/restore`, { method: "POST" })
export const createIntegration = (input: Omit<APIIntegration, "id" | "created_at" | "updated_at">) => requestJSON<APIIntegration>("/api/v1/settings/integrations", { method: "POST", body: JSON.stringify(input) })
export const commissionIntegration = (id: string) => requestJSON<APIIntegration>(`/api/v1/settings/integrations/${encodeURIComponent(id)}/commission`, { method: "POST" })
export const createLibraryRoot = (input: Omit<APILibraryRoot, "id">) => requestJSON<APILibraryRoot>("/api/v1/settings/library-roots", { method: "POST", body: JSON.stringify(input) })
export const createQualityProfile = (input: Omit<APIQualityProfile, "id">) => requestJSON<APIQualityProfile>("/api/v1/settings/quality-profiles", { method: "POST", body: JSON.stringify(input) })
export const createExistingWorkflow = (mediaId: string, input: Record<string, unknown>) => requestJSON<Record<string, unknown>>(`/api/v1/media/${encodeURIComponent(mediaId)}/workflows`, { method: "POST", body: JSON.stringify(input) })
export const searchWorkflow = (id: string) => requestJSON<{ status: string; id: string }>(`/api/v1/workflows/${encodeURIComponent(id)}/search`, { method: "POST" })
export const cancelWorkflow = (id: string) => requestJSON<Record<string, unknown>>(`/api/v1/workflows/${encodeURIComponent(id)}/cancel`, { method: "POST" })
export const createReleaseBlock = (input: { candidate_id: string; scope: "native_release" | "pack_release"; reason: string; retention_days?: 30 | 90 }) => requestJSON<APIReleaseBlock>("/api/v1/blocklist", { method: "POST", body: JSON.stringify(input) })
export const deleteReleaseBlock = (id: string) => requestJSON<void>(`/api/v1/blocklist/${encodeURIComponent(id)}`, { method: "DELETE" })
export const createAdoptionBatch = (input: { source_root: string; root_id: string; quality_profile_id: string; monitoring_enabled: boolean; search_immediately: boolean }) => requestJSON<APIAdoptionBatch>("/api/v1/adoption", { method: "POST", body: JSON.stringify(input) })
export const reviewAdoptionItem = (batchID: string, itemID: string, input: { review_state: "approved" | "rejected"; media_id?: string; root_id?: string; quality_profile_id?: string; proposed_relative?: string; monitoring_enabled?: boolean; search_immediately?: boolean }) => requestJSON<APIAdoptionBatch>(`/api/v1/adoption/${encodeURIComponent(batchID)}/items/${encodeURIComponent(itemID)}`, { method: "PATCH", body: JSON.stringify(input) })
export const approveAdoptionBatch = (id: string) => requestJSON<APIAdoptionBatch>(`/api/v1/adoption/${encodeURIComponent(id)}/approve`, { method: "POST" })
export const uploadProwlarrBackup = (file: File) => requestJSON<APIProwlarrImport>("/api/v1/imports/prowlarr", { method: "POST", headers: { "Content-Type": "application/zip" }, body: file })
export const approveProwlarrImport = (id: string, candidates: Array<{ id: string; secret_ref: string }>) => requestJSON<APIProwlarrImport>(`/api/v1/imports/prowlarr/${encodeURIComponent(id)}/approve`, { method: "POST", body: JSON.stringify({ candidates }) })
