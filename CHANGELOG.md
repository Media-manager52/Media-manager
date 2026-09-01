# Changelog

Notable Media Manager changes are recorded here. The project has not published a tagged release yet, so the completed work remains under **Unreleased** until the owner cuts the first release.

The structure follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions will use semantic versioning once release tags begin.

## Unreleased

### Added

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

- Movie-library assembly now indexes related files, wanted items, and workflows once and reuses precomputed search text, avoiding quadratic work as the catalog grows.
- Catalog loading now retrieves every API page in bounded 200-record batches instead of silently presenting a truncated library.
- Library contrast, typography, responsive sizing, and inspector readability were improved without changing the approved composition.
- Newznab search requests now honor each commissioned provider's supported dialect and independently persisted query/grab budget.
- Equal-score release selection uses deterministic durable identities rather than response timing.
- Indexer cooldown work is deferred without blocking the worker or consuming an acquisition attempt.

### Security

- Every Newznab NZB request, including each of at most two redirects, rechecks the workflow revision and selected candidate immediately before sending.
- Cross-origin NZB redirects use per-hop public-address validation and literal-IP pinning while preserving TLS hostname verification and stripping credentials, Referer, and origin-specific headers.
- Newznab observation URLs are sanitized before persistence, including nested and percent-encoded credential parameters; historical observations were corrected without deleting evidence.
- Secret files, environment files, runtime data, generated output, and deployment credentials remain excluded from version control.

### Verified

- One real movie workflow and one real complete-season-pack workflow completed exact Danish-subtitle, publication-generation, and Plex identity/path verification.
- A subtitle-failed season output remained unpublished, undeleted, and visible for owner review as required.
- The supported Docker stack, schema 30, production UI build, Go race suite, browser suite, and GitLab pipeline have passing evidence.
