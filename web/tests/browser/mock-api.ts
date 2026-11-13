import type { Page, Request } from "@playwright/test"

/** A day relative to today, so calendar fixtures never fall out of the visible week. */
function recentDay(offset: number): string {
  const day = new Date()
  day.setDate(day.getDate() + offset)
  return `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`
}

export const ids = {
  movie: "11111111-1111-4111-8111-111111111111",
  series: "22222222-2222-4222-8222-222222222222",
  attention: "33333333-3333-4333-8333-333333333333",
  season: "44444444-4444-4444-8444-444444444444",
  indexer: "55555555-5555-4555-8555-555555555555",
  seerr: "66666666-6666-4666-8666-666666666666",
  root: "77777777-7777-4777-8777-777777777777",
  profile: "88888888-8888-4888-8888-888888888888",
  plex: "99999999-9999-4999-8999-999999999999",
}

const now = "2026-11-03T12:00:00Z"

const attentionWorkflow = {
  id: ids.attention, media_id: ids.movie, media_kind: "movie", title: "Testfilmen", year: 2024, variant: "default", state: "needs_attention", revision: 7,
  attention_code: "altmount_job_attention", attention_detail: "AltMount reported the job failed.", owner_action_required: true,
  adopted_replacement_confirmation_required: false, created_at: now, updated_at: now, quality_profile: "Movie Danish Subs",
  available_actions: [
    { action: "reconcile_altmount", operation_id: "reconcileAltMount", method: "POST", path: `/api/v1/workflows/${ids.attention}/reconcile-altmount`, effect: "write", available: true },
    { action: "search", operation_id: "searchWorkflow", method: "POST", path: `/api/v1/workflows/${ids.attention}/search`, effect: "provider", available: false, reason: "An AltMount job is active; wait for it to settle." },
    { action: "cancel", operation_id: "cancelWorkflow", method: "POST", path: `/api/v1/workflows/${ids.attention}/cancel`, effect: "write", available: true },
  ],
}

const seasonWorkflow = {
  id: ids.season, media_id: ids.series, media_kind: "series", title: "Testserien", year: 2020, variant: "season:1", state: "plex_confirmed", revision: 3,
  adopted_replacement_confirmation_required: false, created_at: now, updated_at: now, terminal_at: now, available_actions: [],
}

const media = [
  { id: ids.movie, kind: "movie", title: "Testfilmen", year: 2024, identities: [{ namespace: "tmdb", value: "1" }], metadata: { overview: "En film til tests.", tagline: "En film, der kun findes i tests.", runtime_minutes: 101, credits: [{ name: "Ida Instruktør", role: "Director", source: "tmdb" }, { name: "Anna Skuespiller", role: "Cast", character: "Hovedpersonen", source: "tmdb" }, { name: "Bo Birolle", role: "Cast", character: "Naboen", source: "tmdb" }], original_language: "da", genres: ["Drama"], sources: [{ provider: "tmdb", provider_id: "1", url: "https://www.themoviedb.org/movie/1", role: "primary" }] } },
  { id: ids.series, kind: "series", title: "Testserien", year: 2020, identities: [{ namespace: "tvdb", value: "2" }], metadata: { overview: "En serie til tests.", season_count: 1 } },
]

const routes: Array<[RegExp, (url: URL) => unknown]> = [
  [/^\/api\/v1\/dashboard$/, () => ({ counts: { movies: 1, series: 1, active_workflows: 1, needs_attention: 1, current_files: 3, recycle_entries: 0, pending_events: 2 }, recent_workflows: [attentionWorkflow, seasonWorkflow], recent_activity: [], generated_at: now })],
  [/^\/api\/v1\/workflows$/, (url) => (url.searchParams.get("attention") === "true" || url.searchParams.get("state") === "needs_attention" ? [attentionWorkflow] : url.searchParams.get("state") === "plex_confirmed" ? [seasonWorkflow] : [])],
  [/^\/api\/v1\/workflows\/[0-9a-f-]+\/searches$/, () => [{ id: "run-1", mode: "targeted", state: "complete", started_at: now, completed_at: now, new_candidates: 2, indexers: [{ indexer_id: ids.indexer, indexer_name: "TestIndexer {DK}", indexer_kind: "newznab", state: "succeeded" }] }]],
  [/^\/api\/v1\/workflows\/[0-9a-f-]+\/candidates$/, () => [{ id: "cand-1", title: "Testfilmen.2024.DANiSH.1080p.WEB-DL", indexer_id: ids.indexer, indexer_name: "TestIndexer", indexer_kind: "newznab", size_bytes: 4_000_000_000, attempted: true, decision: { verdict: "eligible", score: 41000 } }]],
  [/^\/api\/v1\/workflows\/[0-9a-f-]+$/, (url) => (url.pathname.endsWith(ids.season) ? seasonWorkflow : attentionWorkflow)],
  [/^\/api\/v1\/problems$/, () => ({ problems: [], checked_at: now })],
  [/^\/api\/v1\/errors$/, () => ({
    entries: [{ id: "job-1", area: "language", code: "subtitle_requirements_unverified", title: "Testfilmen", media_id: ids.movie, media_kind: "movie", workflow_id: ids.attention, variant: "default", subject: "Testfilmen.2024.1080p.WEB", detail: "Testfilmen.mkv has no verified da subtitle", count: 1, open: false, occurred_at: now }],
    total: 1, areas: { language: 1 }, since: now,
  })],
  [/^\/api\/v1\/system\/health$/, () => ({ database_state: "ready", database_latency_ms: 3, schema_version: 50, expected_schema: 50, outbox: { pending: 2, due: 0, retrying: 0 }, components: [{ id: ids.indexer, kind: "newznab", name: "TestIndexer", enabled: true, is_default: false, config: {}, created_at: now, updated_at: now, configuration_state: "configured", consecutive_failure: 0, last_outcome: "success" }, { id: "77777777-7777-4777-8777-777777777777", kind: "seerr", name: "Seerr - Media Manager Test med et langt navn", enabled: true, is_default: false, config: {}, created_at: now, updated_at: now, configuration_state: "configured", consecutive_failure: 0 }], mounts: [{ integration_id: "a", name: "AltMount", path: "/mnt/altmount", state: "available" }], plex_altmount: [{ plex_id: "plex-1", plex_name: "Plex i stuen", path: "/mnt/altmount", state: "missing", missing: ["media-manager"] }], plex_altmount_checked_at: now, checked_at: now })],
  [/^\/api\/v1\/system\/info$/, () => ({ version: "test", owner_only: true, browser_auth: "cosmos", api_auth: "bearer", seerr_webhook_enabled: true, metadata_providers: {}, recycle_retention_days: 30, checked_at: now })],
  [/^\/api\/v1\/system\/queue$/, () => ({ checked_at: now, due: 0, scheduled: 1, leased: 0, retrying: 0, completed_last_10m: 4, completed_last_hour: 20, by_type: [{ event_type: "workflow.search_requested", due: 0, scheduled: 1, leased: 0, retrying: 0 }] })],
  [/^\/api\/v1\/calendar$/, () => [{ media_id: ids.series, media_kind: "series", media_title: "Testserien", workflow_id: ids.season, variant: "season:2:episode:1", season: 2, episode: 1, episode_title: "Pilot", air_date: recentDay(-2), monitored: true, workflow_state: "waiting" }]],
  [/^\/api\/v1\/media$/, (url) => (url.searchParams.get("offset") === "0" || !url.searchParams.get("offset") ? media : [])],
  [/^\/api\/v1\/media\/gaps$/, () => ({
    series: [
      { media_id: ids.series, title: "Testserien", year: 2020, tmdb_id: "500", root_id: ids.root, season_count: 5, status: "Returning Series", known_seasons: [2, 3], new_seasons: [4, 5], older_seasons: [1], stopped_seasons: [1], metadata_at: now },
    ],
    movies: [{ media_id: ids.movie, title: "Testfilmen", year: 2024, root_id: ids.root, last_state: "cancelled", last_at: now }],
  })],
  [/^\/api\/v1\/media\/status$/, () => [{ media_id: ids.movie, in_plex: false, missing: 1, attention: true, root_ids: [] }, { media_id: ids.series, in_plex: true, missing: 0, attention: false, root_ids: [ids.root], added_at: now }]],
  [/^\/api\/v1\/media\/recent$/, () => [{ media_id: ids.series, media_kind: "series", title: "Testserien", year: 2020, variant: "season:1:episode:2", edition: "", added_at: now }]],
  [/^\/api\/v1\/media\/[0-9a-f-]+\/detail$/, (url) => {
    const item = url.pathname.includes(ids.series) ? media[1] : media[0]
    return item.kind === "series"
      ? { media: item, workflows: [seasonWorkflow], seasons: [{ number: 1, monitored: true, prefer_season_pack: true, search_immediately: true, state: "plex_confirmed", expected_episodes: 2, updated_at: now, episodes: [{ number: 1, title: "Et" }, { number: 2, title: "To" }] }], files: [] }
      : { media: item, workflows: [attentionWorkflow], seasons: [], files: [] }
  }],
  [/^\/api\/v1\/requests$/, (url) => (url.searchParams.get("offset") === "0" ? [
    { id: "req-1", request_id: "41", seerr_instance_id: ids.seerr, media_kind: "movie", title: "Ønskefilmen", year: 2025, requester: "Mor", requested_at: now, updated_at: now, seerr_state: "1", lifecycle: "not_approved", workflow_count: 0, confirmed_count: 0, attention_count: 0, season_count: 0, available_seasons: 0 },
    { id: "req-2", request_id: "42", seerr_instance_id: ids.seerr, media_kind: "movie", title: "Testfilmen", year: 2024, media_id: ids.movie, updated_at: now, seerr_state: "2", lifecycle: "waiting", waiting_reason: "no eligible release yet", workflow_count: 1, confirmed_count: 0, attention_count: 0, season_count: 0, available_seasons: 0 },
  ] : [])],
  [/^\/api\/v1\/indexers$/, () => [{ id: ids.indexer, name: "TestIndexer {DK}", kind: "newznab", enabled: true, commissioned: true, budget: { query_limit: 1000, grab_limit: 100, background_query_limit: 200, minimum_interval_ms: 2000, page_limit: 100, window_seconds: 86400, source: "owner_stated" }, downloads_30d: { finished: 8, verified: 6 }, new_uploads: { last_read_at: now, next_read_at: now, last_outcome: "ok", matches_24h: 3 }, usage: { queries_used: 400, queries_remaining: 600, background_queries_remaining: 50, grabs_used: 3, grabs_remaining: 97 }, pacing: { consecutive_failures: 0, last_outcome: "success", last_outcome_at: now }, search_outcomes_24h: [{ state: "succeeded", count: 380 }, { state: "deferred", count: 20 }] }]],
  [/^\/api\/v1\/settings\/oldboys\/credentials$/, () => ({ available: true, configured: true, enabled: true, updated_at: now })],
  [/^\/api\/v1\/settings\/quality-profiles$/, () => [{ id: ids.profile, media_kind: "movie", name: "Movie Danish Subs", policy_version: 1, policy: { rules: [], required_subtitle_languages: ["da"] }, upgrades_enabled: true, is_default: true, enabled: true }]],
  [/^\/api\/v1\/settings\/library-roots$/, () => [
    { id: ids.root, media_kind: "movie", name: "Film", destination_root: "/media/Movies", plex_visible_root: "/media/Movies", source_visible_root: "/mnt/altmount", is_default: true, enabled: true },
    { id: "root-4k", media_kind: "movie", name: "Film 4K", destination_root: "/media/Film 4K", plex_visible_root: "/media/Film 4K", source_visible_root: "/mnt/altmount", edition: "4k", is_default: false, enabled: true },
  ]],
  [/^\/api\/v1\/settings\/integrations$/, () => [
    { id: ids.seerr, kind: "seerr", name: "Seerr", enabled: true, is_default: true, config: {}, created_at: now, updated_at: now },
    { id: ids.plex, kind: "plex", name: "Plex", enabled: true, is_default: true, config: { base_url: "http://plex:32400", allow_http: true }, secret_ref: `sealed:integration:${ids.plex}`, created_at: now, updated_at: now },
  ]],
  [/^\/api\/v1\/settings\/integrations\/[0-9a-f-]+\/plex-libraries$/, () => [
    { key: "7", title: "Film 4K", type: "movie", locations: ["/media/Film 4K"] },
    { key: "8", title: "Serier", type: "show", locations: ["/media/Serier"] },
  ]],
  [/^\/api\/v1\/setup\/status$/, () => ({
    complete: false, secret_storage_enabled: true, servarr_enabled: false, seerr_intake_enabled: true,
    steps: [
      { id: "plex", done: true, optional: false, detail: "Plex" }, { id: "altmount", done: false, optional: false, detail: "Add AltMount" },
      { id: "indexer", done: true, optional: false, detail: "1 commissioned" }, { id: "movie_root", done: true, optional: false, detail: "Film" },
      { id: "series_root", done: false, optional: false, detail: "Add a library folder and make it the default" }, { id: "movie_profile", done: true, optional: false, detail: "Movie Danish Subs" },
      { id: "series_profile", done: false, optional: false, detail: "Choose a default quality profile" }, { id: "metadata", done: false, optional: true, detail: "Set TMDB_ACCESS_TOKEN" },
      { id: "seerr", done: true, optional: true, detail: "Seerr" },
    ],
  })],
  [/^\/api\/v1\/settings\/integrations\/[0-9a-f-]+\/seerr-routes$/, () => ({ routes: [{ id: "route-1", seerr_instance_id: ids.seerr, media_kind: "movie", root_folder: "/media/Movies", library_root_id: ids.root, enabled: true, created_at: now, updated_at: now }], observed_folders: [] })],
  [/^\/api\/v1\/settings\/notifications$/, () => []],
  [/^\/api\/v1\/system\/altmount$/, () => ({ altmount: { id: "altmount-1", name: "AltMount" }, version: "v0.4.0", build: "standard", can_switch: true, drive: { status: "running", healthy: true, path: "/mnt/altmount" }, download_speed_bytes_per_sec: 12_500_000, bytes_downloaded_24h: 412_000_000_000, unavailable: [], checked_at: now, servers: [
    { id: "provider_1", name: "Hovedserver med et meget langt navn til mobil", host: "news.example-provider-with-a-long-hostname.test", enabled: true, is_backup_provider: false, quota_set: true, live: { id: "provider_1", host: "news.example.test", state: "active", used_connections: 21, max_connections: 50, error_count: 12, failure_reason: "", current_speed_bytes_per_sec: 9_000_000, ping_ms: 13, missing_warning: false, byte_count_24h: 322_000_000_000, quota_bytes: 1_000_000_000_000, quota_used: 820_000_000_000, quota_reset_at: "2026-12-01T00:00:00Z", quota_exceeded: false } },
    { id: "provider_2", host: "backup.example.test", enabled: true, is_backup_provider: true, quota_set: false, live: { id: "provider_2", host: "backup.example.test", state: "active", used_connections: 0, max_connections: 10, error_count: 0, failure_reason: "", current_speed_bytes_per_sec: 0, ping_ms: 20, missing_warning: true, byte_count_24h: 0, quota_bytes: 0, quota_used: 0, quota_exceeded: false } },
    { id: "provider_3", host: "old.example.test", enabled: false, is_backup_provider: false, quota_set: false },
  ], streams: [
    { file: "Far.Skal.Giftes.1941.DANiSH.1080p.WEB.H264-EGEN.mkv", title: { media_id: ids.movie, kind: "movie", title: "Far skal giftes", year: 1941 }, source: "FUSE", status: "Streaming", started_at: now, last_activity: now, total_size: 2_793_771_338, current_offset: 1_389_625_344, bytes_per_second: 720_875 },
    { file: "Other.File.2020.mkv", source: "WebDAV", status: "Buffering", started_at: now, last_activity: now, total_size: 1_000_000_000, current_offset: 0, bytes_per_second: 0 },
  ] })],
  [/^\/api\/v1\/settings\/usenet-servers$/, () => ({ altmount: { id: "altmount-1", name: "AltMount" }, servers: [
    { id: "provider_1", name: "Hovedudbyder", host: "news.example.test", port: 563, username: "me", max_connections: 20, tls: true, insecure_tls: false, password_set: true, enabled: true, is_backup_provider: false, last_rtt_ms: 41, quota_bytes: 0, quota_period_hours: 0 },
    { id: "provider_2", host: "backup.example.test", port: 563, username: "me2", max_connections: 5, tls: true, insecure_tls: false, password_set: true, enabled: false, is_backup_provider: true, last_rtt_ms: 0, quota_bytes: 0, quota_period_hours: 0 },
  ] })],
  [/^\/api\/v1\/setup\/plex\/login\/login1$/, () => ({ state: "approved", servers: [{ id: "machine-1", name: "Hjemme", owned: true, recommended: "http://plex:32400", addresses: [{ uri: "http://plex:32400", local: true, relay: false, reachable: true }, { uri: "https://10-0-0-5.abc.plex.direct:32400", local: true, relay: false, reachable: true }] }] })],
  [/^\/api\/v1\/backups$/, () => ({ configured: true, format: "mmbackup", restore_verification: "isolated", automatic_deletion: false, production_restore: false, offsite: { configured: false, kind: "", prefix: "" }, records: [{ id: "b1", state: "verified", format: "mmbackup", size_bytes: 1_000_000, attempts: 1, created_at: now, completed_at: now }] })],
  [/^\/api\/v1\/repairs$/, () => []],
  [/^\/api\/v1\/activity$/, () => [{ id: "act-1", kind: "audit", actor: "owner", action: "workflow.search_requested", aggregate_type: "workflow", aggregate_id: ids.attention, detail: {}, occurred_at: now }]],
  [/^\/api\/v1\/import-lists$/, () => [{
    id: "44444444-4444-4444-8444-444444444444", name: "Danske favoritter", provider: "mdblist", enabled: true,
    source_owner: "someone", source_slug: "danske-favoritter", source_url: "https://mdblist.com/lists/someone/danske-favoritter",
    series_root_id: "r2", series_root_name: "Serier", last_synced_at: now, next_sync_at: now, last_sync_state: "ok",
    entries: { total: 3, pending: 0, added: 1, in_library: 1, removed_earlier: 0, failed: 1 }, created_at: now, updated_at: now,
  }]],
  [/^\/api\/v1\/import-lists\/[^/]+\/entries$/, () => [
    { media_kind: "movie", tmdb_id: 1, title: "Testfilmen", year: 2024, rank: 1, state: "in_library", media_id: ids.movie, attempts: 0, first_seen_at: now, last_seen_at: now, updated_at: now },
    { media_kind: "series", tmdb_id: 2, title: "Testserien", year: 2023, rank: 2, state: "added", media_id: ids.series, attempts: 0, first_seen_at: now, last_seen_at: now, updated_at: now },
    { media_kind: "series", tmdb_id: 3, title: "Kommer snart", rank: 3, state: "failed", detail: "no_episodes", attempts: 2, first_seen_at: now, last_seen_at: now, updated_at: now },
  ]],
  [/^\/api\/v1\/discovery\/search$/, () => ({ page: 1, totalPages: 1, totalResults: 1, results: [{ id: 99, mediaType: "movie", title: "Nyfilmen", releaseDate: "2025-05-01" }] })],
  [/^\/api\/v1\/discovery\/movie\/99$/, () => ({ kind: "movie", title: "Nyfilmen", year: 2025, tmdb_id: 99, identities: [{ namespace: "tmdb", value: "99", source: "seerr_metadata" }], metadata: { overview: "En ny film." }, enrichment_state: "tmdb_via_seerr", provider_states: {} })],
]

export type Recorded = { method: string; path: string; body: unknown }

/** Serves the mock API and records every write the page sends. */
export async function mockApi(page: Page): Promise<Recorded[]> {
  const writes: Recorded[] = []
  await page.route("**/api/**", async (route) => {
    const request: Request = route.request()
    const url = new URL(request.url())
    if (request.method() !== "GET") {
      writes.push({ method: request.method(), path: url.pathname, body: request.postData() ? JSON.parse(request.postData()!) : null })
      if (url.pathname === "/api/v1/settings/usenet-servers/test") return route.fulfill({ json: { data: { ok: true, rtt_ms: 23 } } })
      if (url.pathname.endsWith("/check")) return route.fulfill({ json: { data: { ok: true, detail: "Plex answered and shows 2 libraries" } } })
      if (url.pathname === "/api/v1/setup/plex/login") return route.fulfill({ status: 201, json: { data: { id: "login1", auth_url: "https://app.plex.tv/auth#?code=abc", expires_at: now } } })
      if (url.pathname === "/api/v1/setup/plex/login/login1/connect") return route.fulfill({ json: { data: { integration: { id: ids.plex, kind: "plex", name: "Hjemme", enabled: true, is_default: true, config: { base_url: "http://plex:32400" }, created_at: now, updated_at: now }, created: true, check: { ok: true, detail: "Plex answered and shows 2 libraries" } } } })
      if (url.pathname === "/api/v1/setup/plex-access") {
        const apply = Boolean((JSON.parse(request.postData() ?? "{}") as { apply?: boolean }).apply)
        const changes = [
          { kind: "add_folder", purpose: "altmount", path: "/mnt/altmount" },
          { kind: "add_library_path", purpose: "movies", path: "/libraries/movies", library: "Film" },
        ]
        return route.fulfill({ json: { data: { plex_container: "plex", changes, plex_paths: { altmount: "/mnt/altmount" }, restart: true, applied: apply } } })
      }
      if (url.pathname === "/api/v1/system/altmount/switch") {
        const apply = Boolean((JSON.parse(request.postData() ?? "{}") as { apply?: boolean }).apply)
        return route.fulfill({ json: { data: { container: "altmount", current_image: "ghcr.io/kipsilabs/altmount:latest", current_version: "v0.4.0", new_image: "ghcr.io/media-manager52/altmount:mm-stable", base_version: "0.4.0", already_media_manager: false, applied: apply, new_version: apply ? "v0.4.0-mm.3" : undefined } } })
      }
      if (url.pathname === "/api/v1/media") return route.fulfill({ status: 201, json: { data: { media: { ...media[0], id: ids.movie }, workflows: [] } } })
      return route.fulfill({ status: 202, json: { data: { status: "queued", id: ids.attention } } })
    }
    const match = routes.find(([pattern]) => pattern.test(url.pathname))
    if (!match) return route.fulfill({ status: 404, json: { error: { code: "not_found", message: `mock has no ${url.pathname}` } } })
    return route.fulfill({ json: { data: match[1](url) } })
  })
  return writes
}
