"use client"

export type APIIdentity = { namespace: string; value: string; state?: "verified" | "candidate" | "rejected"; source?: string }
export type APIMetadataAlias = { name: string; language?: string; source: string }
export type APIMetadataNetwork = { name: string; country?: string; kind?: string; source: string }
export type APIMetadataSource = { provider: string; provider_id?: string; url?: string; role: string }
export type APIMetadataRating = { source: string; value: number; votes: number; provenance?: string }
export type APIMetadataCompany = { name: string; country?: string; kind?: string; source: string }
export type APIMetadataCredit = { name: string; role: string; character?: string; profile_url?: string; source: string }
export type APIMetadataEvent = { type: string; date: string; season_number?: number; episode_number?: number; country?: string; network?: string; confidence?: number; date_conflict: boolean; sources: string[] }
export type APIMediaMetadata = { original_title?: string; overview?: string; tagline?: string; original_language?: string; format?: string; status?: string; release_date?: string; end_date?: string; certification?: string; runtime_minutes?: number; season_count?: number; episode_count?: number; poster_url?: string; backdrop_url?: string; genres: string[]; keywords?: string[]; countries: string[]; networks: APIMetadataNetwork[]; companies?: APIMetadataCompany[]; credits?: APIMetadataCredit[]; aliases: APIMetadataAlias[]; sources: APIMetadataSource[]; ratings: APIMetadataRating[]; events: APIMetadataEvent[]; observed_at: string }
export type APIMedia = { id: string; kind: "movie" | "series"; title: string; year?: number; identities: APIIdentity[]; metadata?: APIMediaMetadata }
export type APIWorkflow = {
  id: string; media_id: string; media_kind: "movie" | "series"; title: string; year?: number; variant: string; state: string; revision: number
  attention_code?: string; attention_detail?: string; acquisition_attempts?: number
  round_ordinal?: number; round_state?: string; round_slots_used?: number; round_attempt_limit?: number
  retry_discovery_state?: "waiting" | "ready" | "paused"; next_rescan_at?: string; last_discovery_at?: string; last_discovery_complete?: boolean
  complete_no_new_scans?: number; new_release_ready?: boolean; ready_candidate_id?: string; ready_release_title?: string; ready_indexer_name?: string; ready_first_observed_at?: string; open_next_round_allowed?: boolean
  retained_output_count?: number; retained_output_bytes?: number; excluded_release_count?: number
  search_allowed?: boolean; search_blocked_reason?: string; adopted_replacement_confirmation_required: boolean
  /** Exactly what AltMount last reported for the in-flight attempt; absent when it reported nothing. */
  download_percent?: number; download_stage?: string; download_observed_at?: string
  owner_action_required?: boolean
  terminal_at?: string; created_at: string; updated_at: string
}
export type APIIntegration = { id: string; kind: string; name: string; enabled: boolean; is_default: boolean; config: Record<string, unknown>; secret_ref?: string; created_at: string; updated_at: string }
export type APISeerrRoute = { id: string; seerr_instance_id: string; media_kind: "movie" | "series"; root_folder: string; library_root_id: string; quality_profile_id?: string; enabled: boolean; created_at: string; updated_at: string }
export type APISeerrObservedFolder = { media_kind: "movie" | "series"; root_folder: string; request_count: number; latest_at: string; route_id?: string; library_root_id?: string; quality_profile_id?: string; routed: boolean }
export type APISeerrRouting = { routes: APISeerrRoute[]; observed_folders: APISeerrObservedFolder[] }
export type APILibraryRoot = { id: string; media_kind: "movie" | "series"; name: string; destination_root: string; plex_visible_root: string; source_visible_root: string; plex_instance_id?: string; plex_library_id?: string; is_default: boolean; enabled: boolean }
export type APIQualityProfile = { id: string; media_kind: "movie" | "series"; name: string; policy_version: number; policy: Record<string, unknown>; upgrades_enabled: boolean; is_default: boolean; enabled: boolean }
export type APISubtitleVerification = { id: string; workflow_id: string; altmount_job_id: string; candidate_id: string; media_title: string; release_title: string; attempt_generation: number; status: "verified" | "unverified"; required_languages: string[]; preferred_languages: string[]; observed_languages: string[]; evidence: Record<string, unknown>; failure_reasons: string[]; retry_state: "not_needed" | "queued" | "succeeded" | "exhausted" | "no_alternate"; can_reverify: boolean; created_at: string }
export type APIRecycleEntry = { id: string; cycle_id: string; media_id: string; media_title: string; library_link_version_id?: string; adopted_file_id?: string; root_id: string; entry_kind: "link" | "real_file"; purpose: "media_delete" | "upgrade_displacement"; original_relative: string; recycle_relative: string; expected_size?: number; altmount_source_retained: boolean; purge_after: string; state: string; confirmation_required: boolean; restore_attempts: number; restore_requested_at?: string; restore_deadline_at?: string; restore_refresh_at?: string; created_at: string }
export type APIProwlarrCandidate = { id: string; import_id: string; name: string; implementation: string; base_url: string; categories: number[]; sanitized_config: Record<string, unknown>; secret_ref?: string; review_state: string; attention_detail?: string; created_instance_id?: string; api_key?: string }
export type APIProwlarrImport = { id: string; state: string; parser_version: string; created_at: string; completed_at?: string; candidates: APIProwlarrCandidate[] }
export type APIActivity = { id: string; kind: "audit" | "workflow_transition"; actor: string; action: string; aggregate_type: string; aggregate_id?: string; detail: Record<string, unknown>; occurred_at: string }
export type APIDashboard = {
  counts: { movies: number; series: number; active_workflows: number; needs_attention: number; current_files: number; recycle_entries: number; pending_events: number }
  recent_workflows: APIWorkflow[]
  recent_activity: APIActivity[]
  generated_at: string
}
export type APIManagedFile = { id: string; media_id: string; media_kind: "movie" | "series"; title: string; year?: number; root_name: string; destination_relative: string; source_relative: string; plex_visible_target: string; state: string; generation: number; plex_confirmed: boolean; verified_at?: string; displaced_at?: string; removed_at?: string; variant: string; origin: "altmount" | "adoption"; quality_profile: string; quality_evidence_state: "media_info" | "release_evidence" | "unassessed"; quality_evidence: Record<string, unknown>; source_size_bytes?: number; source_modified_at?: string; original_source_root?: string; adoption_batch_id?: string; plex_provider_guid?: string }
export type APIReleaseCandidate = { id: string; workflow_id: string; media_id: string; media_kind: "movie" | "series"; media_title: string; media_year?: number; variant: string; title: string; indexer_id: string; indexer_name: string; size_bytes?: number; published_at?: string; verdict?: string; score?: number; reasons: unknown[]; normalized_facts: Record<string, unknown>; problems: unknown[]; selected: boolean; observed_at: string }
export type APISourceStatus = APIIntegration & { configuration_state: "configured" | "disabled" | "awaiting_commission"; minimum_interval_ms?: number; next_allowed_at?: string; cooldown_until?: string; consecutive_failure: number; last_outcome?: string; budget_updated_at?: string }
export type APISystemHealth = { database_state: string; database_latency_ms: number; schema_version: number; expected_schema: number; outbox: { pending: number; due: number; retrying: number; oldest_due_at?: string }; components: APISourceStatus[]; checked_at: string }
export type APIMediaSeason = { number: number; monitored: boolean; prefer_season_pack: boolean; search_immediately: boolean; state: string; expected_episodes: number; released_episodes?: number; future_episodes?: number; workflow_id?: string; workflow_state?: string; waiting_reason?: string; last_search_at?: string; next_search_at?: string; updated_at: string; episodes?: APIMediaEpisode[] }
export type APIMediaEpisode = { number: number; title?: string; air_date?: string }
export type APIMediaDetail = { media: APIMedia; workflows: APIWorkflow[]; seasons: APIMediaSeason[]; files: APIManagedFile[] }
export type APIWantedItem = { workflow_id: string; media_id: string; media_kind: "movie" | "series"; title: string; year?: number; variant: string; gap: "missing" | "upgrade"; current_state: string; target_profile: string; upgrades_enabled: boolean; attention_detail?: string; owner_action_required?: boolean; updated_at: string }
export type APIRequestSeason = { number: number; availability: number; requested: boolean; monitored?: boolean; workflow_id?: string; workflow_state?: string }
export type APIRequest = {
  id: string; request_id: string; seerr_instance_id: string; seerr_instance_name?: string
  media_kind: "movie" | "series" | ""; title: string; year?: number; poster_url?: string; requester?: string
  requested_at?: string; updated_at: string
  seerr_state: string; intake_outcome?: string; intake_error?: string
  media_id?: string
  workflow_count: number; confirmed_count: number; attention_count: number
  current_state?: string; quality_profile?: string; variant?: string
  attention_code?: string; attention_detail?: string
  lifecycle: string
  season_count: number; available_seasons: number
}
export type APIRequestDetail = { request: APIRequest; workflows: APIWorkflow[]; seasons: APIRequestSeason[]; files: APIManagedFile[] }
export type APITaskStatus = { event_type: string; pending: number; due: number; retrying: number; completed: number; highest_attempts: number; oldest_due_at?: string; latest_at: string }
export type APITasks = { event_types: APITaskStatus[]; recurring: Array<{ name: string; schedule: string; automatic: boolean }> }
export type APISystemInfo = { version: string; started_at?: string; owner_only: boolean; browser_auth: string; api_auth: string; seerr_webhook_enabled: boolean; metadata_providers: Record<"tmdb" | "tvdb" | "simkl" | "imdb", string>; altmount_source_policy: string; publication_type: string; upgrade_replacement_gate: string; recycle_retention_days: number; public_edge_owner: string; machine_api_keys: boolean; api_prefix: string; checked_at: string }
export type APIDiscoverySearchResult = { id: number; mediaType: "movie" | "tv"; title?: string; name?: string; releaseDate?: string; firstAirDate?: string; overview?: string; posterPath?: string }
export type APIDiscoverySearchPage = { page: number; totalPages: number; totalResults: number; results: APIDiscoverySearchResult[] }
export type APIDiscoverySeason = { number: number; episodes: Array<{ number: number; provider_id: string; title: string; air_date: string }> }
export type APIDiscoveryDetail = { kind: "movie" | "series"; title: string; year?: number; identities: APIIdentity[]; tmdb_id: number; metadata?: APIMediaMetadata; enrichment_state: "tmdb_via_seerr" | "direct_provider_enriched"; provider_states: Record<string, string>; season?: APIDiscoverySeason }
export type APIReleaseBlock = { id: string; scope: "native_release" | "pack_release"; indexer_instance_id?: string; indexer_name?: string; media_id?: string; media_title?: string; variant?: string; native_key?: string; release_title: string; reason: string; expires_at?: string; created_at: string; created_by: string }
export type APIAdoptionItem = { id: string; batch_id: string; original_relative: string; proposed_relative?: string; media_id?: string; root_id?: string; quality_profile_id?: string; variant?: string; monitoring_enabled?: boolean; search_immediately?: boolean; recognized_companions: string[]; unknown_companions: string[]; review_state: "pending" | "approved" | "rejected" | "needs_attention"; execution_state: string }
export type APIAdoptionBatch = { id: string; source_root: string; media_kind: "movie" | "series"; default_root_id: string; default_quality_profile_id: string; monitoring_enabled: boolean; search_immediately: boolean; replacement_search_authorized: boolean; state: string; created_at: string; approved_at?: string; completed_at?: string; pending_items: number; approved_items: number; rejected_items: number; attention_items: number; items?: APIAdoptionItem[] }
export type APICollection = { id: string; name: string; description: string; created_at: string; updated_at: string; members: APIMedia[] }
export type APICalendarEntry = { media_id: string; media_kind: "movie" | "series"; media_title: string; workflow_id: string; variant: string; season: number; episode: number; episode_title?: string; air_date: string; monitored: boolean; workflow_state: string }
export type APINotificationDestination = { id: string; name: string; endpoint: string; allow_http: boolean; enabled: boolean; events: string[]; secret_ref?: string; created_at: string; updated_at: string }
export type APINotificationDelivery = { id: string; destination_id: string; destination: string; audit_event_id: string; payload: { event_id: string; action: string; aggregate_type: string; aggregate_id?: string; occurred_at: string }; state: string; attempts: number; response_status?: number; last_error?: string; created_at: string; delivered_at?: string }
export type APIImportListItem = { id: string; snapshot_id: string; ordinal: number; media_kind: "movie" | "series" | "unknown"; provider: string; provider_id: string; title: string; year?: number; state: "pending_review" | "already_cataloged" | "unsupported"; attention?: string; existing_media_id?: string }
export type APIImportListSnapshot = { id: string; import_list_id: string; source_digest: string; item_count: number; pending_count: number; existing_count: number; unsupported_count: number; created_at: string; items?: APIImportListItem[] }
export type APIImportList = { id: string; name: string; provider: "imdb_export"; enabled: boolean; movie_root_id?: string; movie_root_name?: string; movie_quality_profile_id?: string; movie_quality_profile?: string; created_at: string; updated_at: string; latest_snapshot?: APIImportListSnapshot }
export type APIBackupRecord = { id: string; state: "queued" | "running" | "verified" | "failed"; format: string; filename?: string; size_bytes?: number; sha256?: string; schema_version?: number; attempts: number; last_error?: string; created_at: string; started_at?: string; completed_at?: string }
export type APIBackupOverview = { configured: boolean; format: string; restore_verification: string; automatic_deletion: boolean; production_restore: boolean; records: APIBackupRecord[] }
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

const catalogPageSize = 200
const catalogMaxPages = 50

export async function loadCatalogMedia(signal?: AbortSignal): Promise<APIMedia[]> {
  const catalog: APIMedia[] = []
  for (let page = 0; page < catalogMaxPages; page += 1) {
    const offset = page * catalogPageSize
    const batch = await requestJSON<APIMedia[] | null>(`/api/v1/media?limit=${catalogPageSize}&offset=${offset}`, { signal }) ?? []
    catalog.push(...batch)
    if (batch.length < catalogPageSize) return catalog
  }
  throw new Error(`Media catalog exceeded the safe ${catalogPageSize * catalogMaxPages}-record load limit`)
}
export const loadMediaItem = (id: string, signal?: AbortSignal) => requestJSON<APIMedia>(`/api/v1/media/${encodeURIComponent(id)}`, { signal })
export const loadWorkflows = async (active = true, signal?: AbortSignal) => await requestJSON<APIWorkflow[] | null>(`/api/v1/workflows?limit=300&active=${active}`, { signal }) ?? []
export const loadIntegrations = async (signal?: AbortSignal) => await requestJSON<APIIntegration[] | null>("/api/v1/settings/integrations", { signal }) ?? []
export const loadLibraryRoots = async (signal?: AbortSignal) => await requestJSON<APILibraryRoot[] | null>("/api/v1/settings/library-roots", { signal }) ?? []
export const loadQualityProfiles = async (signal?: AbortSignal) => await requestJSON<APIQualityProfile[] | null>("/api/v1/settings/quality-profiles", { signal }) ?? []
export const loadSubtitleVerifications = async (signal?: AbortSignal) => await requestJSON<APISubtitleVerification[] | null>("/api/v1/acquisition-verifications?limit=100", { signal }) ?? []
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
export const loadRequests = async (signal?: AbortSignal) => await requestJSON<APIRequest[] | null>("/api/v1/requests?limit=300", { signal }) ?? []
export const loadRequest = (id: string, signal?: AbortSignal) => requestJSON<APIRequestDetail>(`/api/v1/requests/${encodeURIComponent(id)}`, { signal })
export const loadTasks = (signal?: AbortSignal) => requestJSON<APITasks>("/api/v1/system/tasks", { signal })
export const loadSystemInfo = (signal?: AbortSignal) => requestJSON<APISystemInfo>("/api/v1/system/info", { signal })
export const searchMetadata = (query: string, page = 1, signal?: AbortSignal) => requestJSON<APIDiscoverySearchPage>(`/api/v1/discovery/search?query=${encodeURIComponent(query)}&page=${page}`, { signal })
export const loadDiscoveryMetadata = (kind: "movie" | "tv", tmdbID: number, season?: number, signal?: AbortSignal) => requestJSON<APIDiscoveryDetail>(`/api/v1/discovery/${kind}/${tmdbID}${season === undefined ? "" : `?season=${season}`}`, { signal })
export const loadReleaseBlocks = async (signal?: AbortSignal) => await requestJSON<APIReleaseBlock[] | null>("/api/v1/blocklist?limit=300", { signal }) ?? []
export const loadAdoptionBatches = async (signal?: AbortSignal) => await requestJSON<APIAdoptionBatch[] | null>("/api/v1/adoption?limit=100", { signal }) ?? []
export const loadAdoptionBatch = (id: string, signal?: AbortSignal) => requestJSON<APIAdoptionBatch>(`/api/v1/adoption/${encodeURIComponent(id)}`, { signal })
export const loadCollections = async (signal?: AbortSignal) => await requestJSON<APICollection[] | null>("/api/v1/collections?limit=100", { signal }) ?? []
export const loadCalendar = async (signal?: AbortSignal) => await requestJSON<APICalendarEntry[] | null>("/api/v1/calendar?limit=500", { signal }) ?? []
export const loadNotificationDestinations = async (signal?: AbortSignal) => await requestJSON<APINotificationDestination[] | null>("/api/v1/settings/notifications", { signal }) ?? []
export const loadNotificationDeliveries = async (signal?: AbortSignal) => await requestJSON<APINotificationDelivery[] | null>("/api/v1/notifications/deliveries?limit=300", { signal }) ?? []
export const loadImportLists = async (signal?: AbortSignal) => await requestJSON<APIImportList[] | null>("/api/v1/import-lists", { signal }) ?? []
export const loadImportList = (id: string, signal?: AbortSignal) => requestJSON<APIImportList>(`/api/v1/import-lists/${encodeURIComponent(id)}`, { signal })
export const loadBackups = (signal?: AbortSignal) => requestJSON<APIBackupOverview>("/api/v1/backups", { signal })

export type CreateMediaInput = {
  kind: "movie" | "series"
  title: string
  year?: number
  identities: Array<APIIdentity & { state: "verified"; source: string }>
  metadata?: APIMediaMetadata
  search_immediately: boolean
  root_id?: string
  quality_profile_id?: string
  download_client_id?: string
  seasons?: Array<{ number: number; monitored: boolean; search_immediately: boolean; prefer_season_pack: boolean; episodes: Array<{ number: number; provider_id?: string; provider_episode_id?: string; title?: string; air_date?: string }> }>
}

export const createMedia = (input: CreateMediaInput) => requestJSON<{ media: APIMedia; workflows: APIWorkflow[] }>("/api/v1/media", { method: "POST", body: JSON.stringify(input) })
export const refreshMediaMetadata = (id: string) => requestJSON<{ media: APIMedia; enrichment_state: APIDiscoveryDetail["enrichment_state"]; provider_states: Record<string, string> }>(`/api/v1/media/${encodeURIComponent(id)}/metadata/refresh`, { method: "POST" })
export const recycleMedia = (id: string) => requestJSON<APIRecycleEntry[]>(`/api/v1/media/${encodeURIComponent(id)}`, { method: "DELETE" })
export const restoreRecycle = (id: string) => requestJSON<{ status: string; id: string }>(`/api/v1/recycle/${encodeURIComponent(id)}/restore`, { method: "POST" })
export const restoreAdoptedFiles = (id: string, confirmation: string) => requestJSON<{ status: string; id: string }>(`/api/v1/recycle/${encodeURIComponent(id)}/restore-adopted`, { method: "POST", body: JSON.stringify({ confirmation }) })
export const confirmRealFilePurge = (id: string, confirmation: string) => requestJSON<{ status: string; id: string }>(`/api/v1/recycle/${encodeURIComponent(id)}/confirm-purge`, { method: "POST", body: JSON.stringify({ confirmation }) })
export const createIntegration = (input: Omit<APIIntegration, "id" | "created_at" | "updated_at">) => requestJSON<APIIntegration>("/api/v1/settings/integrations", { method: "POST", body: JSON.stringify(input) })
export const commissionIntegration = (id: string) => requestJSON<APIIntegration>(`/api/v1/settings/integrations/${encodeURIComponent(id)}/commission`, { method: "POST" })
export const loadSeerrRouting = (instanceID: string) => requestJSON<APISeerrRouting>(`/api/v1/settings/integrations/${encodeURIComponent(instanceID)}/seerr-routes`)
export const saveSeerrRoute = (instanceID: string, input: { media_kind: "movie" | "series"; root_folder: string; library_root_id: string; quality_profile_id?: string }) => requestJSON<APISeerrRoute>(`/api/v1/settings/integrations/${encodeURIComponent(instanceID)}/seerr-routes`, { method: "POST", body: JSON.stringify(input) })
export const deleteSeerrRoute = (instanceID: string, routeID: string) => requestJSON<void>(`/api/v1/settings/integrations/${encodeURIComponent(instanceID)}/seerr-routes/${encodeURIComponent(routeID)}`, { method: "DELETE" })
export const createLibraryRoot = (input: Omit<APILibraryRoot, "id">) => requestJSON<APILibraryRoot>("/api/v1/settings/library-roots", { method: "POST", body: JSON.stringify(input) })
export const createQualityProfile = (input: Omit<APIQualityProfile, "id">) => requestJSON<APIQualityProfile>("/api/v1/settings/quality-profiles", { method: "POST", body: JSON.stringify(input) })
export const updateQualityProfile = (id: string, input: Omit<APIQualityProfile, "id">) => requestJSON<APIQualityProfile>(`/api/v1/settings/quality-profiles/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(input) })
export const createExistingWorkflow = (mediaId: string, input: Record<string, unknown>) => requestJSON<Record<string, unknown>>(`/api/v1/media/${encodeURIComponent(mediaId)}/workflows`, { method: "POST", body: JSON.stringify(input) })
export const searchWorkflow = (id: string, adoptedReplacementConfirmation = "") => requestJSON<{ status: string; id: string }>(`/api/v1/workflows/${encodeURIComponent(id)}/search`, { method: "POST", body: JSON.stringify({ adopted_replacement_confirmation: adoptedReplacementConfirmation }) })
export const openAcquisitionRound = (id: string) => requestJSON<{ status: string; id: string }>(`/api/v1/workflows/${encodeURIComponent(id)}/acquisition-rounds`, { method: "POST" })
export const reconcileAltMount = (id: string) => requestJSON<{ status: string; id: string }>(`/api/v1/workflows/${encodeURIComponent(id)}/reconcile-altmount`, { method: "POST" })
export const reverifyRetainedOutput = (workflowID: string, jobID: string) => requestJSON<{ status: string; workflow_id: string; job_id: string }>(`/api/v1/workflows/${encodeURIComponent(workflowID)}/reverify-retained-output`, { method: "POST", body: JSON.stringify({ job_id: jobID }) })
export const cancelWorkflow = (id: string) => requestJSON<Record<string, unknown>>(`/api/v1/workflows/${encodeURIComponent(id)}/cancel`, { method: "POST" })
export const createReleaseBlock = (input: { candidate_id: string; scope: "native_release" | "pack_release"; reason: string; retention_days?: 30 | 90 }) => requestJSON<APIReleaseBlock>("/api/v1/blocklist", { method: "POST", body: JSON.stringify(input) })
export const deleteReleaseBlock = (id: string) => requestJSON<void>(`/api/v1/blocklist/${encodeURIComponent(id)}`, { method: "DELETE" })
export const createAdoptionBatch = (input: { source_root: string; root_id: string; quality_profile_id: string; monitoring_enabled: boolean; search_immediately: boolean }) => requestJSON<APIAdoptionBatch>("/api/v1/adoption", { method: "POST", body: JSON.stringify(input) })
export const reviewAdoptionItem = (batchID: string, itemID: string, input: { review_state: "approved" | "rejected"; media_id?: string; root_id?: string; quality_profile_id?: string; proposed_relative?: string; variant?: string; monitoring_enabled?: boolean; search_immediately?: boolean }) => requestJSON<APIAdoptionBatch>(`/api/v1/adoption/${encodeURIComponent(batchID)}/items/${encodeURIComponent(itemID)}`, { method: "PATCH", body: JSON.stringify(input) })
export const approveAdoptionBatch = (id: string) => requestJSON<APIAdoptionBatch>(`/api/v1/adoption/${encodeURIComponent(id)}/approve`, { method: "POST" })
export const executeAdoptionBatch = (id: string, confirmation: string, adoptedReplacementConfirmation = "") => requestJSON<APIAdoptionBatch>(`/api/v1/adoption/${encodeURIComponent(id)}/execute`, { method: "POST", body: JSON.stringify({ confirmation, adopted_replacement_confirmation: adoptedReplacementConfirmation }) })
export const previewCollection = (media_ids: string[]) => requestJSON<APIMedia[]>("/api/v1/collections/preview", { method: "POST", body: JSON.stringify({ media_ids }) })
export const createCollection = (input: { name: string; description: string; media_ids: string[] }) => requestJSON<APICollection>("/api/v1/collections", { method: "POST", body: JSON.stringify(input) })
export const deleteCollection = (id: string) => requestJSON<void>(`/api/v1/collections/${encodeURIComponent(id)}`, { method: "DELETE" })
export const createNotificationDestination = (input: { name: string; endpoint: string; allow_http: boolean; enabled: boolean; events: string[]; secret_ref?: string }) => requestJSON<APINotificationDestination>("/api/v1/settings/notifications", { method: "POST", body: JSON.stringify(input) })
export const setNotificationDestinationEnabled = (id: string, enabled: boolean) => requestJSON<{ id: string; enabled: boolean }>(`/api/v1/settings/notifications/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify({ enabled }) })
export const createImportList = (input: { name: string; provider: "imdb_export"; enabled: boolean; movie_root_id?: string; movie_quality_profile_id?: string }) => requestJSON<APIImportList>("/api/v1/import-lists", { method: "POST", body: JSON.stringify(input) })
export const uploadImportListSnapshot = (id: string, file: File) => requestJSON<APIImportListSnapshot>(`/api/v1/import-lists/${encodeURIComponent(id)}/snapshots`, { method: "POST", headers: { "Content-Type": "text/csv" }, body: file })
export const createBackup = () => requestJSON<APIBackupRecord>("/api/v1/backups", { method: "POST" })
export const uploadProwlarrBackup = (file: File) => requestJSON<APIProwlarrImport>("/api/v1/imports/prowlarr", { method: "POST", headers: { "Content-Type": "application/zip" }, body: file })
export const approveProwlarrImport = (id: string, candidates: Array<{ id: string; secret_ref: string }>) => requestJSON<APIProwlarrImport>(`/api/v1/imports/prowlarr/${encodeURIComponent(id)}/approve`, { method: "POST", body: JSON.stringify({ candidates }) })
