# Changelog

Notable Media Manager changes are recorded here. The project has not published a tagged release yet, so the completed work remains under **Unreleased** until the owner cuts the first release.

The structure follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions will use semantic versioning once release tags begin.

## Unreleased

### Added

- A Radarr/Sonarr-compatible API (`/radarr/api/v3`, `/sonarr/api/v3`, guarded by `SERVARR_API_KEY`) so Seerr can use Media Manager as its only server: it exposes Media Manager's library roots and quality profiles, acknowledges adds without creating a second catalog, and reports Plex-confirmed availability for scans. Existing requests keep working through the Seerr root-folder routes.
- Seerr request routing: the service root folder and profile chosen in Seerr are persisted as request provenance and resolved through owner-created mappings in Settings → Connections; an unmapped folder pauses the request with an exact reason instead of publishing into the wrong library, and saving a mapping requeues paused requests immediately.
- A complete documentation set under `docs/` (onboarding, architecture, operations, reference, status, archive) with an index, package-level Go doc comments for every package, and a `docs:check` link/anchor validator wired into CI.
- A loopback-only authenticated gateway smoke helper that prompts securely without storing the owner password.
- A credential-safe live Playwright acceptance suite for every deployed route across desktop, tablet, and mobile viewports.
- Movie and exact TV season-pack creation acceptance coverage across the live read-only form, browser request payload, owner HTTP API, and isolated PostgreSQL lifecycle.
- A responsive TV Shows library with real series metadata, season-level workflow/file state, filters, selection, and exact series-detail navigation.
- A concise repository code map and local movie-library benchmark for developer and AI-assisted maintenance.
- Owner-only live screens for library, workflows, wanted media, files, sources, settings, health, audit history, adoption review, and release evidence.
- Seerr discovery and intake with exact provider identities and immutable TV-season expectations.
- Provider-aware Newznab commissioning, conservative persisted budgets, request pacing, exact release selection, and Prowlarr backup import.
- AltMount submission, ambiguous-result reconciliation, bounded completed-output inspection, and retained owner-visible failures.
- Complete-season pack acquisition as one job with exact episode coverage.
- Required and preferred subtitle-language policy, canonical Danish `da` normalization, embedded and strict sidecar inspection, and two bounded acquisition attempts.
- Plex-safe managed symlink publication, generation-linked subtitle companions, exact identity/path verification, safe upgrades, and rollback support.
- Direct TMDB, TheTVDB, and Simkl metadata enrichment with persisted provenance.
- Reviewed existing-library adoption, cross-root execution, reversible retention, collections, import lists, notifications, and verified database backups.
- Dense responsive movie library with persisted metadata, real filters, pagination, selected-item inspection, and keyboard operation.
- Local Playwright and axe browser coverage for desktop, mobile, accessibility, interactions, and unexpected-network blocking.
- GitLab CI verification for the frontend, browser suite, and PostgreSQL-backed Go race tests.

### Changed

- Danish subtitles remain **required** for foreign content on the two `Best Available Danish Subs` quality profiles (`required_subtitle_languages: ["da"]`); a same-day experiment that relaxed them to preferred was reverted at owner direction before any relaxed-policy output was verified or published. Output verification enforces the union of the attempt's round snapshot and the profile's current required languages, so a later tightening also applies to a job already in flight. Danish-original routing is unchanged.
- AltMount packages are screened before submission: an NZB whose manifest contains only archive sets, ISO/DVD structures, or other non-playable containers is rejected without an upload. AltMount queue failures caused by archives, unplayable manifests, or missing Usenet segments now exclude the exact release and package and retry another candidate inside the open acquisition round instead of parking the workflow as infrastructure attention. If AltMount has already pruned the failed queue entry, the persisted failure detail drives the same decision instead of retrying the reconcile forever.
- Movie-library assembly now indexes related files, wanted items, and workflows once and reuses precomputed search text, avoiding quadratic work as the catalog grows.
- Catalog loading now retrieves every API page in bounded 200-record batches instead of silently presenting a truncated library.
- Library contrast, typography, responsive sizing, and inspector readability were improved without changing the approved composition.
- Newznab search requests now honor each commissioned provider's supported dialect and independently persisted query/grab budget.
- Equal-score release selection uses deterministic durable identities rather than response timing.
- Indexer cooldown work is deferred without blocking the worker or consuming an acquisition attempt.

### Fixed

- The Radarr/Sonarr add and command handlers now accept the real client payloads Seerr sends (`profileId`, `minimumAvailability`, `addOptions`, `searchNow`, `seasons`, `seasonFolder`, `seriesType`, `tags`, `movieIds`, ...). Strict JSON decoding rejected every add with HTTP 400, so Seerr marked new requests FAILED before Media Manager's intake ever saw them.
- Msgnews download locators now request the canonical `www.msgnews.live` host directly. The provider 308-redirects the bare host while carrying the API key in the path, which the cross-origin credential policy correctly rejected; requesting the canonical host keeps the key on the same registrable site without weakening redirect pinning or credential stripping.

### Security

- Every Newznab NZB request, including each of at most two redirects, rechecks the workflow revision and selected candidate immediately before sending.
- Cross-origin NZB redirects use per-hop public-address validation and literal-IP pinning while preserving TLS hostname verification and stripping credentials, Referer, and origin-specific headers.
- Newznab observation URLs are sanitized before persistence, including nested and percent-encoded credential parameters; historical observations were corrected without deleting evidence.
- Secret files, environment files, runtime data, generated output, and deployment credentials remain excluded from version control.

### Verified

- One real movie workflow and one real complete-season-pack workflow completed exact Danish-subtitle, publication-generation, and Plex identity/path verification.
- A subtitle-failed season output remained unpublished, undeleted, and visible for owner review as required.
- The supported Docker stack, schema 30, production UI build, Go race suite, browser suite, and GitLab pipeline have passing evidence.
