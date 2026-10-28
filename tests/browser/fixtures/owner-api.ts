import type { Page } from "@playwright/test"

/**
 * A complete, self-consistent local owner API for browser tests. Every GET the
 * live screens issue is answered from these records; any mutation or unknown
 * request is recorded as unexpected and refused, and every non-local request is
 * blocked before transmission. These records exist only in the test suite and
 * are never imported by the application.
 */

const ids = {
  movieA: "11111111-1111-4111-8111-111111111111",
  movieB: "22222222-2222-4222-8222-222222222222",
  movieC: "44444444-4444-4444-8444-444444444444",
  movieD: "45454545-4545-4545-8545-454545454545",
  series: "99999999-9999-4999-8999-999999999999",
  seriesB: "98989898-9898-4898-8898-989898989898",
  fileA: "33333333-3333-4333-8333-333333333333",
  fileSeries: "77777777-7777-4777-8777-777777777777",
  wfA: "55555555-5555-4555-8555-555555555555",
  wfB: "66666666-6666-4666-8666-666666666666",
  wfSeries: "88888888-8888-4888-8888-888888888888",
  wfAttention: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  wfSearching: "abababab-abab-4bab-8bab-abababababab",
  mediaAttention: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
  plex: "c1c1c1c1-c1c1-4c1c-8c1c-c1c1c1c1c1c1",
  seerr: "c2c2c2c2-c2c2-4c2c-8c2c-c2c2c2c2c2c2",
  altmount: "c3c3c3c3-c3c3-4c3c-8c3c-c3c3c3c3c3c3",
  indexer: "c4c4c4c4-c4c4-4c4c-8c4c-c4c4c4c4c4c4",
  indexerB: "c5c5c5c5-c5c5-4c5c-8c5c-c5c5c5c5c5c5",
  rootMovies: "d1d1d1d1-d1d1-4d1d-8d1d-d1d1d1d1d1d1",
  rootTV: "d2d2d2d2-d2d2-4d2d-8d2d-d2d2d2d2d2d2",
  profileMovie: "e1e1e1e1-e1e1-4e1e-8e1e-e1e1e1e1e1e1",
  profileTV: "e2e2e2e2-e2e2-4e2e-8e2e-e2e2e2e2e2e2",
  request: "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1",
  requestB: "f2f2f2f2-f2f2-4f2f-8f2f-f2f2f2f2f2f2",
  candidate: "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1",
  candidateB: "a2a2a2a2-a2a2-4a2a-8a2a-a2a2a2a2a2a2",
  recycle: "a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a3a3",
  collection: "a4a4a4a4-a4a4-4a4a-8a4a-a4a4a4a4a4a4",
  importList: "a5a5a5a5-a5a5-4a5a-8a5a-a5a5a5a5a5a5",
  backup: "a6a6a6a6-a6a6-4a6a-8a6a-a6a6a6a6a6a6",
  notification: "a7a7a7a7-a7a7-4a7a-8a7a-a7a7a7a7a7a7",
  block: "a8a8a8a8-a8a8-4a8a-8a8a-a8a8a8a8a8a8",
  adoption: "a9a9a9a9-a9a9-4a9a-8a9a-a9a9a9a9a9a9",
} as const

export const ownerFixtureIDs = ids

const observed = "2026-09-01T00:00:00Z"
const metadata = (overrides: Record<string, unknown>) => ({ genres: [], countries: [], networks: [], aliases: [], ratings: [], sources: [], events: [], observed_at: observed, ...overrides })

const media = [
  { id: ids.movieA, kind: "movie", title: "First Browser Movie", year: 2024, identities: [{ namespace: "tmdb", value: "101", state: "verified" }], metadata: metadata({ overview: "The first browser-test overview.", tagline: "A quiet film about a loud city.", original_language: "en", runtime_minutes: 98, certification: "15", release_date: "2024-03-01", genres: ["Drama", "Thriller"], countries: ["DK"], ratings: [{ source: "tmdb", value: 8.1, votes: 200 }], credits: [{ name: "Browser Director", role: "Director", source: "tmdb" }, { name: "Browser Actor", role: "Cast", character: "Lead", source: "tmdb" }], sources: [{ provider: "tmdb", provider_id: "101", role: "primary" }] }) },
  { id: ids.movieB, kind: "movie", title: "Second Browser Movie", year: 2022, identities: [{ namespace: "imdb", value: "tt0000202", state: "verified" }], metadata: metadata({ overview: "The second browser-test overview.", original_language: "da", genres: ["Comedy"], countries: ["DK"] }) },
  { id: ids.movieC, kind: "movie", title: "Third Browser Movie", year: 2020, identities: [] },
  { id: ids.movieD, kind: "movie", title: "Nordic Winter Film", year: 2019, identities: [{ namespace: "tmdb", value: "404", state: "verified" }], metadata: metadata({ overview: "A long winter in a small northern town.", original_language: "sv", runtime_minutes: 121, genres: ["Drama"], countries: ["SE"], ratings: [{ source: "tmdb", value: 7.2, votes: 88 }] }) },
  { id: ids.mediaAttention, kind: "movie", title: "Attention Browser Movie", year: 2023, identities: [{ namespace: "tmdb", value: "505", state: "verified" }], metadata: metadata({ overview: "Waiting on verified Danish subtitles.", original_language: "fr", genres: ["Mystery"] }) },
  { id: ids.series, kind: "series", title: "First Browser Series", year: 2021, identities: [{ namespace: "tvdb", value: "303", state: "verified" }], metadata: metadata({ overview: "The browser-test series overview.", original_language: "da", format: "Series", status: "Returning", release_date: "2021-03-04", season_count: 2, episode_count: 14, genres: ["Drama"], countries: ["DK"], networks: [{ name: "Browser Network", source: "tvdb" }], ratings: [{ source: "tvdb", value: 7.8, votes: 100 }] }) },
  { id: ids.seriesB, kind: "series", title: "Harbour Detectives", year: 2018, identities: [{ namespace: "tvdb", value: "606", state: "verified" }], metadata: metadata({ overview: "Two detectives and one very busy harbour.", original_language: "da", status: "Ended", season_count: 3, episode_count: 24, genres: ["Crime"], networks: [{ name: "DR1", source: "tvdb" }] }) },
]

const files = [
  { id: ids.fileA, media_id: ids.movieA, media_kind: "movie", title: "First Browser Movie", year: 2024, root_name: "Movies Primary", destination_relative: "First Browser Movie (2024)/First Browser Movie (2024).mkv", source_relative: "browser-fixture/output.mkv", plex_visible_target: "/movies/First Browser Movie (2024)/First Browser Movie (2024).mkv", state: "current", generation: 2, plex_confirmed: true, verified_at: observed, variant: "movie", origin: "altmount", quality_profile: "Movie 1080p Danish", quality_evidence_state: "media_info", quality_evidence: { facts: { resolution: "1080p", video_codec: "hevc", audio_codecs: ["eac3"], audio_languages: ["en"], subtitle_languages: ["da"] } }, source_size_bytes: 8_400_000_000, plex_provider_guid: "tmdb://101" },
  { id: ids.fileSeries, media_id: ids.series, media_kind: "series", title: "First Browser Series", year: 2021, root_name: "TV Primary", destination_relative: "First Browser Series (2021) {tvdb-303}/Season 01/First Browser Series - S01E01.mkv", source_relative: "browser-series/episode.mkv", plex_visible_target: "/tv/First Browser Series (2021) {tvdb-303}/Season 01/First Browser Series - S01E01.mkv", state: "current", generation: 1, plex_confirmed: true, variant: "season:01", origin: "altmount", quality_profile: "TV 1080p Danish", quality_evidence_state: "media_info", quality_evidence: { facts: { resolution: "1080p", audio_codecs: ["aac"], subtitle_languages: ["da"] } }, source_size_bytes: 1_900_000_000 },
  { id: "34343434-3434-4434-8434-343434343434", media_id: ids.seriesB, media_kind: "series", title: "Harbour Detectives", year: 2018, root_name: "TV Primary", destination_relative: "Harbour Detectives (2018)/Season 01/Harbour Detectives - S01E01.mkv", source_relative: "Harbour.Detectives.S01E01.mkv", plex_visible_target: "/tv/Harbour Detectives (2018)/Season 01/Harbour Detectives - S01E01.mkv", state: "current", generation: 1, plex_confirmed: true, variant: "season:01", origin: "adoption", quality_profile: "TV 1080p Danish", quality_evidence_state: "release_evidence", quality_evidence: { facts: { resolution: "720p" } }, source_size_bytes: 1_100_000_000 },
]

const workflows = [
  { id: ids.wfA, media_id: ids.movieA, media_kind: "movie", title: "First Browser Movie", year: 2024, variant: "movie", state: "plex_confirmed", revision: 4, adopted_replacement_confirmation_required: false, terminal_at: observed, created_at: "2026-08-31T00:00:00Z", updated_at: observed },
  { id: ids.wfB, media_id: ids.movieB, media_kind: "movie", title: "Second Browser Movie", year: 2022, variant: "movie", state: "monitored", revision: 1, search_allowed: true, adopted_replacement_confirmation_required: false, created_at: observed, updated_at: observed },
  { id: ids.wfSeries, media_id: ids.series, media_kind: "series", title: "First Browser Series", year: 2021, variant: "season:01", state: "plex_confirmed", revision: 5, adopted_replacement_confirmation_required: false, created_at: observed, updated_at: observed },
  { id: ids.wfSearching, media_id: ids.movieD, media_kind: "movie", title: "Nordic Winter Film", year: 2019, variant: "movie", state: "searching", revision: 3, round_ordinal: 1, round_state: "open", round_slots_used: 1, round_attempt_limit: 2, acquisition_attempts: 1, adopted_replacement_confirmation_required: false, created_at: observed, updated_at: "2026-09-02T08:00:00Z" },
  { id: ids.wfAttention, media_id: ids.mediaAttention, media_kind: "movie", title: "Attention Browser Movie", year: 2023, variant: "movie", state: "needs_attention", revision: 7, attention_code: "subtitle_unverified", attention_detail: "Required Danish subtitles were not verified", owner_action_required: true, round_ordinal: 1, round_state: "exhausted", round_slots_used: 2, round_attempt_limit: 2, acquisition_attempts: 2, retained_output_count: 2, retained_output_bytes: 9_800_000_000, excluded_release_count: 2, adopted_replacement_confirmation_required: false, created_at: observed, updated_at: "2026-09-02T00:00:00Z" },
]

const wanted = [
  { workflow_id: ids.wfA, media_id: ids.movieA, media_kind: "movie", title: "First Browser Movie", year: 2024, gap: "upgrade", current_state: "plex_confirmed", target_profile: "Movie 1080p Danish", upgrades_enabled: true, variant: "movie", updated_at: observed },
  { workflow_id: ids.wfB, media_id: ids.movieB, media_kind: "movie", title: "Second Browser Movie", year: 2022, gap: "missing", current_state: "monitored", target_profile: "Movie 1080p Danish", upgrades_enabled: true, variant: "movie", updated_at: observed },
  { workflow_id: ids.wfSearching, media_id: ids.movieD, media_kind: "movie", title: "Nordic Winter Film", year: 2019, gap: "missing", current_state: "searching", target_profile: "Movie 1080p Danish", upgrades_enabled: true, variant: "movie", updated_at: observed },
  { workflow_id: ids.wfAttention, media_id: ids.mediaAttention, media_kind: "movie", title: "Attention Browser Movie", year: 2023, gap: "missing", current_state: "needs_attention", target_profile: "Movie 1080p Danish", upgrades_enabled: true, owner_action_required: true, attention_detail: "Required Danish subtitles were not verified", variant: "movie", updated_at: "2026-09-02T00:00:00Z" },
]

const integrations = [
  { id: ids.plex, kind: "plex", name: "Plex", enabled: true, is_default: true, config: { base_url: "http://plex:32400" }, secret_ref: "PLEX_TOKEN", created_at: observed, updated_at: observed },
  { id: ids.seerr, kind: "seerr", name: "Seerr", enabled: true, is_default: true, config: { base_url: "http://seerr:5055" }, secret_ref: "SEERR_API_KEY", created_at: observed, updated_at: observed },
  { id: ids.altmount, kind: "altmount", name: "AltMount", enabled: true, is_default: true, config: { base_url: "http://altmount:8080", reported_root: "/", local_read_only_root: "/mnt/altmount" }, created_at: observed, updated_at: observed },
  { id: ids.indexer, kind: "newznab", name: "NZB Example", enabled: true, is_default: false, config: { base_url: "https://indexer.invalid/api", commissioned_dialects: { movie: { mode: "movie", params: ["q", "imdbid"] }, series: { mode: "tvsearch", params: ["q", "tvdbid", "season"] } }, query_limit: 1000, grab_limit: 100 }, secret_ref: "NZB_EXAMPLE_KEY", created_at: observed, updated_at: observed },
  { id: ids.indexerB, kind: "newznab", name: "Staged Indexer", enabled: false, is_default: false, config: { base_url: "https://staged.invalid/api" }, secret_ref: "STAGED_KEY", created_at: observed, updated_at: observed },
]

const sources = integrations.map((item) => ({
  ...item,
  configuration_state: item.kind === "newznab" && !item.enabled ? "awaiting_commission" : "configured",
  minimum_interval_ms: item.kind === "newznab" ? 3000 : undefined,
  consecutive_failure: 0,
  last_outcome: item.kind === "newznab" && item.enabled ? "succeeded" : undefined,
}))

const roots = [
  { id: ids.rootMovies, media_kind: "movie", name: "Movies Primary", destination_root: "/libraries/movies", plex_visible_root: "/libraries/movies", source_visible_root: "/mnt/altmount", plex_instance_id: ids.plex, plex_library_id: "1", is_default: true, enabled: true },
  { id: ids.rootTV, media_kind: "series", name: "TV Primary", destination_root: "/libraries/tv", plex_visible_root: "/libraries/tv", source_visible_root: "/mnt/altmount", plex_instance_id: ids.plex, plex_library_id: "2", is_default: true, enabled: true },
]

const profiles = [
  { id: ids.profileMovie, media_kind: "movie", name: "Movie 1080p Danish", policy_version: 3, upgrades_enabled: true, is_default: true, enabled: true, policy: { rules: [{ name: "Required resolution", fact: "resolution", allowed_values: ["1080p", "2160p"], kind: "required", unknown: "hold" }], required_subtitle_languages: ["da"], preferred_subtitle_languages: [] } },
  { id: ids.profileTV, media_kind: "series", name: "TV 1080p Danish", policy_version: 1, upgrades_enabled: true, is_default: true, enabled: true, policy: { rules: [], required_subtitle_languages: ["da"], original_language_routing: [] } },
]

const requests = [
  { id: ids.request, request_id: "412", seerr_instance_id: ids.seerr, seerr_instance_name: "Seerr", media_kind: "movie", title: "First Browser Movie", year: 2024, requester: "Owner", requested_at: "2026-08-30T10:00:00Z", updated_at: observed, seerr_state: "2", intake_outcome: "media_manager_added", media_id: ids.movieA, workflow_count: 1, confirmed_count: 1, attention_count: 0, current_state: "plex_confirmed", quality_profile: "Movie 1080p Danish", variant: "movie", lifecycle: "plex_confirmed", season_count: 0, available_seasons: 0 },
  { id: ids.requestB, request_id: "423", seerr_instance_id: ids.seerr, seerr_instance_name: "Seerr", media_kind: "movie", title: "Attention Browser Movie", year: 2023, requester: "Family", requested_at: "2026-09-01T09:00:00Z", updated_at: "2026-09-02T00:00:00Z", seerr_state: "2", intake_outcome: "media_manager_added", media_id: ids.mediaAttention, workflow_count: 1, confirmed_count: 0, attention_count: 1, current_state: "needs_attention", attention_code: "subtitle_unverified", attention_detail: "Required Danish subtitles were not verified", quality_profile: "Movie 1080p Danish", variant: "movie", lifecycle: "needs_attention", season_count: 0, available_seasons: 0 },
]

const candidates = [
  { id: ids.candidate, workflow_id: ids.wfA, media_id: ids.movieA, media_kind: "movie", media_title: "First Browser Movie", media_year: 2024, variant: "movie", title: "First.Browser.Movie.2024.1080p.WEB-DL.DDP5.1.H.265-GROUP", indexer_id: ids.indexer, indexer_name: "NZB Example", size_bytes: 8_400_000_000, published_at: "2026-08-20T00:00:00Z", verdict: "eligible", score: 42, reasons: ["resolution 1080p satisfied"], normalized_facts: { resolution: "1080p", group: "GROUP", source: "web-dl", subtitle_languages: ["da"] }, problems: [], selected: true, observed_at: observed, last_observed_at: "2026-09-02T00:00:00Z" },
  { id: ids.candidateB, workflow_id: ids.wfAttention, media_id: ids.mediaAttention, media_kind: "movie", media_title: "Attention Browser Movie", media_year: 2023, variant: "movie", title: "Attention.Browser.Movie.2023.720p.BluRay.x264-OTHER", indexer_id: ids.indexer, indexer_name: "NZB Example", size_bytes: 4_100_000_000, published_at: "2026-08-21T00:00:00Z", verdict: "rejected", score: 0, reasons: [], normalized_facts: { resolution: "720p", group: "OTHER" }, problems: ["resolution 720p is not allowed"], selected: false, observed_at: observed, last_observed_at: observed },
]

const activity = [
  { id: "ac000001-0000-4000-8000-000000000001", kind: "workflow_transition", actor: "worker", action: "plex_scanning → plex_confirmed", aggregate_type: "workflow", aggregate_id: ids.wfA, detail: { reason: "Plex confirmed every expected identity and path" }, occurred_at: observed },
  { id: "ac000002-0000-4000-8000-000000000002", kind: "audit", actor: "owner", action: "media.created", aggregate_type: "media", aggregate_id: ids.movieB, detail: { title: "Second Browser Movie" }, occurred_at: "2026-08-31T12:00:00Z" },
  { id: "ac000003-0000-4000-8000-000000000003", kind: "workflow_transition", actor: "worker", action: "verifying → needs_attention", aggregate_type: "workflow", aggregate_id: ids.wfAttention, detail: { reason: "Required Danish subtitles were not verified" }, occurred_at: "2026-09-02T00:00:00Z" },
]

const recycle = [
  { id: ids.recycle, cycle_id: "cy000001-0000-4000-8000-000000000001", media_id: ids.movieA, media_title: "First Browser Movie", library_link_version_id: "lv000001-0000-4000-8000-000000000001", root_id: ids.rootMovies, entry_kind: "link", purpose: "upgrade_displacement", original_relative: "First Browser Movie (2024)/First Browser Movie (2024) {release-1}.mkv", recycle_relative: ".recycle/cy000001/First Browser Movie (2024) {release-1}.mkv", altmount_source_retained: true, purge_after: "2026-10-01T00:00:00Z", state: "retained", confirmation_required: false, restore_attempts: 0, created_at: observed },
]

const detailFor = (id: string) => {
  const item = media.find((entry) => entry.id === id) ?? media[0]
  return {
    media: item,
    workflows: workflows.filter((workflow) => workflow.media_id === item.id),
    seasons: item.kind === "series" ? [
      { number: 1, monitored: true, prefer_season_pack: true, search_immediately: true, state: "available", expected_episodes: 8, released_episodes: 8, workflow_id: ids.wfSeries, workflow_state: "plex_confirmed", updated_at: observed, episodes: [{ number: 1, title: "The First One", air_date: "2021-03-04" }, { number: 2, title: "The Second One", air_date: "2021-03-11" }] },
      { number: 2, monitored: true, prefer_season_pack: true, search_immediately: true, state: "wanted", expected_episodes: 6, released_episodes: 3, future_episodes: 3, workflow_state: "waiting", waiting_reason: "Airing season: released episodes are acquired individually.", updated_at: observed, episodes: [{ number: 3, title: "The Third One", air_date: "2026-09-28" }, { number: 4, title: "The Fourth One", air_date: "2026-10-05" }] },
    ] : [],
    files: files.filter((file) => file.media_id === item.id),
  }
}

const getResponses: Record<string, unknown> = {
  "/api/v1/media": media,
  "/api/v1/files": files,
  "/api/v1/wanted": wanted,
  "/api/v1/workflows": workflows,
  "/api/v1/system/health": { database_state: "ready", database_latency_ms: 2, schema_version: 41, expected_schema: 41, outbox: { pending: 3, due: 1, retrying: 0, oldest_due_at: observed }, components: sources, checked_at: observed },
  "/api/v1/system/info": { version: "browser-test", owner_only: true, browser_auth: "Cosmos OpenID with server-side bearer injection", api_auth: "Owner bearer token", seerr_webhook_enabled: true, metadata_providers: { tmdb: "direct_configured", tvdb: "direct_configured", simkl: "direct_configured", imdb: "via_simkl_when_available" }, altmount_source_policy: "read-only", publication_type: "symlink", upgrade_replacement_gate: "exact Plex confirmation", recycle_retention_days: 30, public_edge_owner: "Cosmos Cloud", machine_api_keys: false, api_prefix: "/api/v1", checked_at: observed },
  "/api/v1/system/tasks": { event_types: [{ event_type: "workflow.search_requested", pending: 2, due: 1, retrying: 0, completed: 140, highest_attempts: 1, oldest_due_at: observed, latest_at: observed }, { event_type: "plex.confirm_requested", pending: 0, due: 0, retrying: 0, completed: 38, highest_attempts: 2, latest_at: observed }], recurring: [{ name: "Seerr polling", schedule: "every 5 minutes", automatic: true }, { name: "Recycle purge planning", schedule: "hourly", automatic: true }] },
  "/api/v1/dashboard": { counts: { movies: 5, series: 2, active_workflows: 3, needs_attention: 1, current_files: 3, recycle_entries: 1, pending_events: 3 }, recent_workflows: workflows, recent_activity: activity, generated_at: observed },
  "/api/v1/activity": activity,
  "/api/v1/settings/integrations": integrations,
  "/api/v1/settings/oldboys/credentials": { available: true, configured: false },
  "/api/v1/settings/library-roots": roots,
  "/api/v1/settings/quality-profiles": profiles,
  "/api/v1/acquisition-verifications": [{ id: "ve000001-0000-4000-8000-000000000001", workflow_id: ids.wfAttention, altmount_job_id: "jb000001-0000-4000-8000-000000000001", candidate_id: ids.candidateB, media_title: "Attention Browser Movie", release_title: "Attention.Browser.Movie.2023.1080p.WEB-DL-GROUP", attempt_generation: 2, status: "unverified", required_languages: ["da"], preferred_languages: [], observed_languages: ["en", "fr"], evidence: {}, failure_reasons: ["required subtitle language da was not observed"], retry_state: "exhausted", can_reverify: true, created_at: "2026-09-02T00:00:00Z" }],
  "/api/v1/recycle": recycle,
  "/api/v1/intelligence/releases": candidates,
  "/api/v1/sources": sources,
  "/api/v1/requests": requests,
  "/api/v1/blocklist": [{ id: ids.block, scope: "native_release", indexer_instance_id: ids.indexer, indexer_name: "NZB Example", release_title: "Attention.Browser.Movie.2023.720p.BluRay.x264-OTHER", reason: "Mislabelled audio", expires_at: "2026-10-30T00:00:00Z", created_at: observed, created_by: "owner" }],
  "/api/v1/adoption": [{ id: ids.adoption, source_root: "/srv/media/Old Movies", media_kind: "movie", default_root_id: ids.rootMovies, default_quality_profile_id: ids.profileMovie, monitoring_enabled: false, search_immediately: false, replacement_search_authorized: false, state: "reviewing", created_at: observed, pending_items: 2, approved_items: 1, rejected_items: 0, attention_items: 0 }],
  "/api/v1/collections": [{ id: ids.collection, name: "Winter evenings", description: "Slow Nordic dramas.", created_at: observed, updated_at: observed, members: [media[1], media[3]] }],
  "/api/v1/calendar": [{ media_id: ids.series, media_kind: "series", media_title: "First Browser Series", workflow_id: ids.wfSeries, variant: "season:02", season: 2, episode: 4, episode_title: "The Fourth One", air_date: "2026-10-05", monitored: true, workflow_state: "waiting" }, { media_id: ids.series, media_kind: "series", media_title: "First Browser Series", workflow_id: ids.wfSeries, variant: "season:02", season: 2, episode: 5, episode_title: "The Fifth One", air_date: "2026-10-12", monitored: true, workflow_state: "waiting" }],
  "/api/v1/settings/notifications": [{ id: ids.notification, name: "Phone push", endpoint: "https://push.invalid/…", allow_http: false, enabled: true, events: ["workflow.needs_attention"], secret_ref: "PUSH_TOKEN", created_at: observed, updated_at: observed }],
  "/api/v1/notifications/deliveries": [{ id: "de000001-0000-4000-8000-000000000001", destination_id: ids.notification, destination: "Phone push", audit_event_id: "ac000003-0000-4000-8000-000000000003", payload: { event_id: "ac000003-0000-4000-8000-000000000003", action: "workflow.needs_attention", aggregate_type: "workflow", aggregate_id: ids.wfAttention, occurred_at: observed }, state: "delivered", attempts: 1, response_status: 204, created_at: observed, delivered_at: observed }],
  "/api/v1/import-lists": [{ id: ids.importList, name: "IMDb watchlist", provider: "imdb_export", enabled: true, movie_root_id: ids.rootMovies, movie_root_name: "Movies Primary", movie_quality_profile_id: ids.profileMovie, movie_quality_profile: "Movie 1080p Danish", created_at: observed, updated_at: observed, latest_snapshot: { id: "sn000001-0000-4000-8000-000000000001", import_list_id: ids.importList, source_digest: "abc123", item_count: 12, pending_count: 8, existing_count: 3, unsupported_count: 1, created_at: observed } }],
  "/api/v1/backups": { configured: true, format: "pg_dump custom format, AES-256-GCM (MMBAK001)", restore_verification: "isolated temporary database", automatic_deletion: false, production_restore: false, offsite: { configured: true, kind: "s3", prefix: "media-manager/backups" }, records: [{ id: ids.backup, state: "verified", format: "MMBAK001", filename: "media-manager-2026-09-01.mmbak", size_bytes: 48_000_000, sha256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", schema_version: 41, attempts: 1, offsite_state: "uploaded", offsite_key: "media-manager/backups/media-manager-2026-09-01.mmbak", offsite_uploaded_at: observed, created_at: observed, completed_at: observed }] },
  "/api/v1/problems": { problems: [{ code: "workflow.needs_attention", area: "workflows", level: "error", title: "Workflow: season pack incomplete", detail: "season pack is incomplete; 2 expected episodes are missing", count: 1, oldest_at: observed, latest_at: observed, href: "/activity/queue" }, { code: "backup.offsite_failed", area: "backups", level: "warning", title: "Off-site backup copy failed", detail: "S3 upload returned HTTP 503", count: 1, oldest_at: observed, latest_at: observed, href: "/system/backups" }], checked_at: observed },
  "/api/v1/discovery/search": { page: 1, totalPages: 1, totalResults: 0, results: [] },
}

function detailResponse(path: string): unknown {
  let match = path.match(/^\/api\/v1\/workflows\/([0-9a-f-]{36})\/searches$/)
  if (match) return [{ id: "se000001-0000-4000-8000-000000000001", mode: "targeted", state: "complete", started_at: observed, completed_at: observed, new_candidates: 2, indexers: [{ indexer_id: ids.indexer, indexer_name: "NZB Example", indexer_kind: "newznab", state: "succeeded", completed_at: observed }, { indexer_id: ids.indexer, indexer_name: "Second Example", indexer_kind: "newznab", state: "deferred", detail: "indexer_pacing_deferred", retry_at: "2026-09-02T00:10:00Z", completed_at: observed }] }]
  match = path.match(/^\/api\/v1\/media\/([0-9a-f-]{36})\/detail$/)
  if (match) return detailFor(match[1])
  match = path.match(/^\/api\/v1\/media\/([0-9a-f-]{36})$/)
  if (match) return media.find((item) => item.id === match![1]) ?? media[0]
  match = path.match(/^\/api\/v1\/files\/([0-9a-f-]{36})$/)
  if (match) return files.find((file) => file.id === match![1]) ?? files[0]
  match = path.match(/^\/api\/v1\/requests\/([0-9a-f-]{36})$/)
  if (match) {
    const request = requests.find((item) => item.id === match![1]) ?? requests[0]
    return { request, workflows: workflows.filter((workflow) => workflow.media_id === request.media_id), seasons: [], files: files.filter((file) => file.media_id === request.media_id) }
  }
  match = path.match(/^\/api\/v1\/adoption\/([0-9a-f-]{36})$/)
  if (match) return { ...(getResponses["/api/v1/adoption"] as Array<Record<string, unknown>>)[0], items: [{ id: "ai000001-0000-4000-8000-000000000001", batch_id: ids.adoption, original_relative: "Old Film (1999)/old.film.1999.mkv", proposed_relative: "Old Film (1999)/Old Film (1999).mkv", review_state: "pending", execution_state: "planned", recognized_companions: ["old.film.1999.da.srt"], unknown_companions: [] }] }
  match = path.match(/^\/api\/v1\/import-lists\/([0-9a-f-]{36})$/)
  if (match) return (getResponses["/api/v1/import-lists"] as unknown[])[0]
  match = path.match(/^\/api\/v1\/settings\/integrations\/([0-9a-f-]{36})\/seerr-routes$/)
  if (match) return { routes: [], observed_folders: [{ media_kind: "movie", root_folder: "/srv/media/Auto", request_count: 12, latest_at: observed, routed: true }] }
  return undefined
}

export type OwnerAPIRecorder = { unexpected: string[] }

/** Installs the local owner API and blocks every other origin. */
export async function installOwnerAPI(page: Page, baseURL: string): Promise<OwnerAPIRecorder> {
  const recorder: OwnerAPIRecorder = { unexpected: [] }
  await page.route("**/*", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.origin !== baseURL) {
      recorder.unexpected.push(`${request.method()} ${url.origin}${url.pathname}`)
      await route.abort("blockedbyclient")
      return
    }
    if (!url.pathname.startsWith("/api/v1/")) {
      await route.continue()
      return
    }
    if (request.method() !== "GET") {
      recorder.unexpected.push(`${request.method()} ${url.pathname}`)
      await route.fulfill({ status: 501, json: { error: { message: "Mutations are not allowed in this test" } } })
      return
    }
    let data: unknown = getResponses[url.pathname]
    if (url.pathname === "/api/v1/media" && Number(url.searchParams.get("offset") ?? "0") > 0) data = []
    if (data === undefined) data = detailResponse(url.pathname)
    if (data === undefined) {
      recorder.unexpected.push(`GET ${url.pathname}${url.search}`)
      await route.fulfill({ status: 501, json: { error: { message: "Unexpected local test request" } } })
      return
    }
    await route.fulfill({ status: 200, json: { data } })
  })
  return recorder
}
