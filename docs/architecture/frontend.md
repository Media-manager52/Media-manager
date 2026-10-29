# 05 — Frontend reference (Media Manager UI)

Purpose: how the web UI is built, routed, data-loaded, and tested. The UI is a
thin owner console: it renders persisted server read models and never invents
success. All durable behavior lives in the Go API.

## 1. Stack and build

| Concern | Choice |
| --- | --- |
| Framework | Next.js 16 App Router rendered by **Vinext** on **Vite 8** (`vinext` package) |
| Runtime | Cloudflare Workers semantics via `@cloudflare/vite-plugin`; `worker/index.ts` is the entry, Node serves it in the container (`vinext start`). The bundle imports `cloudflare:workers` for optional tracing, so the container sets `NODE_OPTIONS=--import=/app/scripts/register-cloudflare-loader.mjs`, which resolves `cloudflare:` imports to an inert module (`scripts/cloudflare-scheme-loader.mjs`); `tests/rendered-html.test.mjs` registers the same loader. |
| UI | React 19, TypeScript 5.9, Tailwind CSS 4, shadcn-style primitives (`components/ui/*`) built on Base UI/Radix packages |
| Fonts | Geist and Geist Mono (`@fontsource-variable/*`), imported by `app/layout.tsx` and bundled as hashed assets; no font CDN is contacted at runtime |
| Theme | Semantic design tokens in `app/globals.css`; dark (default), light, or system, chosen in the header and stored per browser (`lib/theme.ts`) |
| Icons | `lucide-react` |
| Charts | `recharts` |
| Router | Next App Router file routing; route → screen mapping is a hand-written switch (see §3) |
| Database in UI | **None.** `db/schema.ts` is empty scaffolding; `db/index.ts` is unused D1 boilerplate. |

Key files:

- `app/layout.tsx` — HTML shell, bundled fonts, `globals.css`, and the inline
  `themeBootScript` that applies the stored theme before first paint.
- `lib/theme.ts` — theme preference parsing and resolution (pure).
- `app/page.tsx` — home entry rendering the app shell.
- `app/[...route]/page.tsx` — catch-all route; validates the path with
  `isKnownRoute`, otherwise `notFound()`; renders `<MediaManagerApp path=... />`.
- `app/chatgpt-auth.ts` — internal template auth helper (not part of the media
  domain).
- `vite.config.ts` — Vinext + sites plugin + local Cloudflare bindings.
- `build/sites-vite-plugin.ts` — packages metadata/migrations after the Vite
  build.
- `worker/index.ts` — worker `fetch` handler incl. `/_vinext/image`
  optimization against `IMAGES`.
- `next.config.ts` — `images.remotePatterns` allow-lists TMDB, TheTVDB, Simkl
  artwork hosts. Any new artwork host must be added here **and** must render
  through `metadata-artwork.tsx`, which falls back to a local placeholder.
- `scripts/build-verified.sh` — bounded `vinext build` with a 3-minute timeout.
- `scripts/sites-env.sh` — isolates HOME/caches under `.sites-runtime/` for
  lint/typecheck/build commands.

## 2. Data loading model

All data comes from the Go API through `lib/media-manager-api.ts`:

- `requestJSON<T>(path, init)` does `fetch(path, { credentials: "same-origin" })`,
  accepts `Accept: application/json`, sets JSON content type when there is a
  body, unwraps the `{ data }` envelope, and converts API errors into
  `Error(message)`.
- Read helpers (`loadCatalogMedia`, `loadDashboard`, `loadWorkflows`,
  `loadMediaDetail`, `loadRequests`, ...) are called from screens inside
  `React.useEffect` with an `AbortController`.
- Mutation helpers (`createMedia`, `searchWorkflow`, `createAdoptionBatch`,
  `restoreAdoptedFiles`, `createBackup`, ...) return promises the screens call
  behind explicit owner actions.
- Types (`APIMedia`, `APIWorkflow`, `APIManagedFile`, `APIRequest`,
  `APISystemHealth`, ...) mirror API payloads. When you change an API shape,
  update the matching type; screens are typed against it.
- The catalog loader pages at 200 records up to 50 pages (10 000) and throws a
  bounded error beyond that.
- Read-only screens refresh themselves through `useAutoRefresh`
  (`hooks/use-auto-refresh.ts`). These are Overview, Library, Requests (list
  and inspector), Wanted, Calendar, component health, tasks, and title pages
  every 15 s (`LIVE_REFRESH_MS`), and Workflows every 8 s. Refreshing pauses in
  a background tab and catches up once when the tab is visible again. It only
  re-reads persisted state; it never starts work or contacts an integration.
  Screens opt in with `useLiveData(loader, { autoRefresh: true })`. Settings
  and other form screens stay manual, so a refresh can never overwrite an edit.

The UI is same-origin by construction: `/api/*` is routed by the public edge
(Cosmos) or the loopback acceptance proxy. There is no API base URL config and
no token handling in browser code.

## 3. Routing and screens

### Everyday app

The default interface is a poster-first app for everyday use. It has its own
scoped palette and type in `components/media-manager/app/` (`app.css`, with the
bundled Bricolage Grotesque display font) and these routes:

| Route | Screen |
| --- | --- |
| `/` | Home: what needs the owner, work on the way, recently added, movie and TV rows |
| `/library` | Library: Movies/TV, search, and plain status filters (`?type=series`, `?show=…`) |
| `/library/:id` | Title page: status, five-step progress, seasons, what the owner has, folded expert details |
| `/requests` | Seerr requests with approve/decline for pending ones |
| `/needs-you` | Every owner decision (stopped workflows and pending approvals) |
| `/add` | TMDB search through Seerr; adds with the default root and quality profile |
| `/settings` | Plain status cards that link to the existing forms |

`AppShell` (`app/shell.tsx`) loads one shared snapshot (`app/app-data.tsx`):
the catalog, files, wanted, active and attention workflows, and requests,
refreshed every 15 s while visible. Titles get one headline status from
`lib/app-presentation.ts`. Owner actions are only those the server lists as
available in a workflow's `available_actions`, posted exactly as listed
(`runWorkflowAction`); actions that need a confirmation phrase stay in the
expert console, and Stop asks first. Approve/decline uses the existing request
decision routes. Add repeats the expert Discover flow: verified identities, an
exact episode snapshot per chosen season, and the default root/profile/client.

The expert console keeps every other route. Its overview moved to `/console`
and its request table to `/console/requests`; the sidebar's **Back to the new
app** returns to `/`. Title pages and stopped items link to the matching
console page for options the everyday app does not offer.

`tests/browser/app.spec.ts` tours every everyday route at desktop and phone
size (axe, no sideways scroll, no console errors, no unexpected requests) and
covers filters, folded expert details, server-listed actions with the Stop
confirmation, request approval, and the exact add payloads. Set
`MM_APP_SCREENSHOTS=<dir>` to save full-page screenshots.

Route metadata lives in `lib/media-manager-routes.ts`:

- `routeTitles` maps every static route to `{ area, title, subtitle }`.
- `normalizeRoute` collapses detail URLs to tokens:
  `/media/movies/<uuid>` → `/media/movies/$mediaId`, same for series, files,
  and indexers.
- `isKnownRoute` accepts only the known static routes plus UUID detail routes.

`components/media-manager/app-shell.tsx` is the expert console's shell:

- Owner-console sidebar (`liveNavigation`): Overview, then Library, Intake,
  Operations, Sources, Settings, System, and Advanced. Every route stays
  reachable; Settings, System, and Advanced start collapsed (a group opens when
  it holds the current page), and the whole sidebar collapses to icons
  (`collapsible="icon"`, Ctrl/Cmd-B or the header toggle). The sidebar carries
  the search button and a footer status chip that names the first degraded
  enabled source from persisted health.
- Live counts: `OwnerAttentionProvider` loads `loadWanted` + active
  `loadWorkflows` once per page for the whole shell; Wanted shows the
  owner-attention count, Workflows the actively-acquiring count, and the
  **Owner attention** bell shows its count before it is opened
  (`collectOwnerAttention`, `isActivelyAcquiring`).
- Header: sidebar toggle, breadcrumb (area → title, with detail pages linking
  back to their list via `detailParents`), the Ctrl/Cmd-K command palette, the
  theme menu, and the bell.
- Phones (`< md`): a fixed bottom tab bar (`aria-label="Mobile navigation"`) for
  Overview, Movies, TV Shows, Requests, and More (opens the sidebar sheet).
- `RouteContent` is the authoritative route → screen switch. Unknown known-route
  combinations render `LiveUnavailableScreen`.
- Library routes (`/media`, `/media/series`) use a full-height layout; the
  Overview and media detail routes render their own header; other routes get a
  padded page with `PageHeader` (title + subtitle; the area lives in the
  breadcrumb).

Console building blocks live in `components/media-manager/console.tsx`:
`MiniPoster` (poster thumbnail or tinted initials, never a placeholder
request), `StageStepper` (eight steps from the persisted state only),
`ConsoleRow`, `AttentionCard` (kind of problem, exact persisted reason, and
retry plan in plain language), `attentionEntries`, and `CoverageBar` (season
coverage as counts, with unaired episodes deliberately calm).
`overview-screen.tsx` holds the Overview: system tiles from persisted health,
Needs attention, Active now, Recently completed, Waiting, Recent requests,
and Plex publication.

Screen components live in two large files (this is intentional; keep changes
near the relevant screen and extract pure logic into `lib/`):

`components/media-manager/live-screens.tsx`

| Component | Route(s) |
| --- | --- |
| `LiveWorkflowScreen` | `/activity/queue` |
| `LiveDiscoverScreen` | `/discover` |
| `LiveMediaDetailScreen` | `/media/movies/:id`, `/media/series/:id` |
| `LiveRecycleScreen` | `/activity/recycle` |
| `LiveAdoptionScreen` | `/import` |
| `LiveCollectionsScreen` | `/collections` |
| `LiveConnectionsScreen` | `/settings/connections` |
| `LiveRootsScreen` | `/settings/storage` |
| `LiveProfilesScreen` | `/settings/profiles` |
| `LiveProwlarrImportScreen` | `/sources/indexer-lab` |
| `LiveUnavailableScreen` | fallback for unmapped screens |

`components/media-manager/live-operational-screens.tsx`

| Component | Route(s) |
| --- | --- |
| `LiveOverviewScreen` (`overview-screen.tsx`) | `/console` |
| `LiveLibraryScreen` (`mediaKind`) | `/media`, `/media/series` |
| `LiveRequestsScreen` | `/console/requests` |
| `LiveWantedScreen` | `/wanted` |
| `LiveCalendarScreen` | `/calendar` |
| `LiveActivityHistoryScreen` (`logs` prop) | `/activity/history`, `/system/logs` |
| `LiveFilesScreen`, `LiveFileDetailScreen` | `/files`, `/files/:id` |
| `LiveReleaseIntelligenceScreen` (`mode`) | `/intelligence/releases`, `/intelligence/groups`, `/intelligence/evidence` |
| `LiveBlocklistScreen` | `/activity/blocklist` |
| `LiveSourcesScreen` (`kind`) | `/sources/indexers`, `/sources/download-clients` |
| `LiveIndexerDetailScreen` | `/sources/indexers/:id` |
| `LiveComponentHealthScreen` | `/system/health` |
| `LiveTasksScreen` | `/system/tasks` |
| `LiveSystemInfoScreen` (`mode`) | `/settings/media-management`, `/system/security`, `/system/api` |
| `LiveMetadataSettingsScreen` | `/settings/metadata` |
| `LiveNotificationsScreen` | `/settings/notifications` |
| `LiveImportListsScreen` | `/sources/import-lists` |
| `LiveBackupsScreen` | `/system/backups` |

Shared presentation primitives live in `components/media-manager/ui.tsx`:
`PageHeader`, `Panel` (pass `flush` for edge-to-edge tables and lists under a
padded header), `MetricCard`, `describeStatus`, `StateBadge`, `StatusChip`,
`KeyValue`, `WorkProgress`, `InlineNotice`, `EmptyAction`, `SectionLabel`.

Media detail (`LiveMediaDetailScreen`) is a hero card (poster, kind, state,
facts, profile, identities, and actions) over two columns: seasons with
coverage bars (series) or the acquisition pipeline (movies), workflows, and
files on the left; coverage or availability, profile, files, and details on
the right. Wanted splits into Needs attention, Missing, Upgrades, and In
progress; Workflows groups rows into Needs attention, Active, Scheduled &
waiting, and Completed without removing any recovery control. TV title pages
list each expected episode (`episodeStatus` in `lib/series-variants.ts`), and
in-flight workflows show `DownloadProgress`, AltMount's own reported
percentage, instead of the stepper when one exists. Pending Seerr requests
carry `RequestDecisionButtons` (Approve, or Decline after confirmation) in the
Requests table, request panel, and Overview. The Overview Plex card offers
"Re-check Plex" only while titles are in `plex_timeout`. Calendar opens on a
Monday-first week grid with previous/next navigation, and Health shows one
status tile per component plus an Advanced tools list.

## 4. Pure helper modules (`lib/`)

Keep classification/filtering/formatting logic here so screens stay testable.

| Module | Responsibility |
| --- | --- |
| `media-manager-api.ts` | Types, fetchers, mutations, envelope handling. |
| `workflow-presentation.ts` | Plain-language presentation: `workflowSteps`/`workflowStep` (stepper position from the persisted state; unknown states never look further along), `attentionHeadline`, and `retryPlan` (persisted schedule facts only). |
| `workflow-states.ts` | Canonical UI grouping of workflow states (`activelyAcquiring`, `waiting`, `completed`, `ownerAttention`), `requiresOwnerAttention`, `collectOwnerAttention`. The backend owns the durable machine; this only classifies. |
| `media-library.ts` | `buildMediaLibraryRecords` (join media + workflows + files), filters, summaries, initial filter state. Pure; benchmarked by `npm run bench:library`. |
| `series-variants.ts` | Parse/format `season:NN[:episode:NN]`, `rollupSeriesSeasons` for season coverage UI. |
| `media-manager-requests.ts` | Request lifecycle tone/labels, filter sets, `filterRequests`, counts. |
| `app-presentation.ts` | Everyday-app wording: workflow → plain stage and five-step progress, one headline status per title, attention headlines, which server-listed actions to offer, request status, coarse relative time. |
| `media-manager-data.ts` | `StatusTone` type + shared display constants. |
| `media-manager-routes.ts` | Route titles/normalization/validation. |
| `utils.ts` | `cn` (clsx + tailwind-merge). |

## 5. Truthfulness rules in the UI

1. **No fixture data in the live artifact.** Development concept files are not
   imported. `tests/rendered-html.test.mjs` asserts the rendered shell has no
   `codex-preview=development` metadata, and the artifact tests scan for fixture
   records.
2. **Never label waiting as success.** Use `workflow-states.ts` groups;
   `needs_attention` requires an owner action, `waiting` does not. A Seerr
   request whose linked retry discovery found a release requiring owner approval
   is `needs_attention`, even while its acquisition workflow remains `waiting`.
   Requests show whether intake created workflows, the recorded waiting reason,
   and last/next searches. Next search comes only from pending work for the
   exact workflow revision or retry-discovery generation; missing schedule
   evidence never becomes a promised automatic retry.
3. **Empty/error states are explicit.** Show the real API error; do not
   substitute sample rows.
4. **Mutations are owner actions.** All state-changing calls happen only from
   explicit button/confirmation handlers. Dangerous operations require typed
   exact phrases (adoption execute, adopted restore, real-file purge, adopted
   replacement).
5. **No external network from the UI at runtime beyond API + artwork hosts.**
   Plex/AltMount/Seerr are never called from the browser.

## 6. Styling

- Tailwind 4 via `@tailwindcss/postcss`. All colour goes through the semantic
  tokens in `app/globals.css`; **never add a literal hex colour to a screen.**
  Use the Tailwind names the tokens generate:
  - surfaces `bg-canvas` < `bg-surface` (panels) < `bg-surface-2` (insets,
    inputs, table headers) < `bg-surface-3` (hover, selected);
  - borders `border-line`, `border-line-strong`;
  - text `text-fg` > `text-fg-secondary` > `text-fg-muted` > `text-fg-subtle`
    (every level meets WCAG AA on every surface in both themes);
  - accent `bg-brand` / `text-brand-fg`, `bg-brand-soft`, `text-brand-text`,
    `border-brand-line`;
  - status `text-good|info|warn|bad` (readable text shade), `bg-*-soft`,
    `border-*-line`, and `bg-good-solid`/`bg-warn-solid`/`bg-bad-solid` for dots.
- `:root` holds the light theme and `.dark` the dark theme; shadcn tokens
  (`--background`, `--primary`, ...) and the canonical `--status-*` variables
  resolve through the same roles, so primitives and screens always agree.
- Type: Geist at a 14px base; screen text is at least 11px. Use
  `font-medium`/`font-semibold` (the bold weights are intentionally unused)
  and `font-mono` for paths, IDs, and scopes.
- Use the existing primitives and component classes (`mm-panel`, `mm-metric`,
  `mm-input`, `mm-tabs`, `mm-media-hero`, `mm-timeline`) rather than CSS modules.
- Accessibility is expected: keyboard navigation, focus rings, accessible names
  on icon buttons and form controls, and axe checks in both themes.

## 7. Tests

Local gates (no network required beyond localhost):

| Command | Covers |
| --- | --- |
| `npm run lint` | ESLint (Next config). |
| `npm run typecheck` | `tsc --noEmit`. |
| `npm test` | Builds the UI then runs `tests/*.test.mjs` with `node --test`: rendered HTML/worker, UI components, movie library joins, series variants, requests filters. |
| `npm run test:browser` | Builds then runs Playwright Chromium specs in `tests/browser/` against a local `vinext start` on port 4174 (the web server registers the same `cloudflare:` loader as the container). Includes the **route tour** (`route-tour.spec.ts`): every route at desktop dark, desktop light, and mobile, with the complete local owner API in `tests/browser/fixtures/owner-api.ts`, asserting no console errors, no unexpected or mutating requests, no horizontal overflow, and no serious axe violations. Set `MM_TOUR_SCREENSHOTS=<dir>` to save full-page screenshots. |
| `npm run bench:library` | Deterministic benchmark of the library transformation. |
| `npm run test:live-browser` | Read-only acceptance against the **deployed** UI/API through `scripts/live-acceptance-proxy.mjs`; see [07-operations.md](../operations/runbook.md#6-health-checks-and-smoke-tests). |

Browser tests use Playwright + `@axe-core/playwright` and block unexpected
network access. Preserve that property. The shell loads owner attention on every
page, so a spec with its own API mock must answer `GET /api/v1/wanted` and
`GET /api/v1/workflows?active=true`. Both GitLab CI and GitHub Actions run the
browser suite in the pinned `mcr.microsoft.com/playwright` image.

## 8. Adding or changing a screen

1. Add the route to `routeTitles` in `lib/media-manager-routes.ts` (and detail
   matching in `normalizeRoute`/`isKnownRoute` if it is a detail route).
2. Add the API loader/mutation and types in `lib/media-manager-api.ts`.
3. Implement/reuse the screen. Put pure logic in `lib/` and add a unit test.
4. Wire the component in `RouteContent` (`app-shell.tsx`) and add a nav item if
   it is owner-facing.
5. Add/extend a Playwright spec or `tests/*.test.mjs` case. New routes are
   covered by the route tour automatically; extend `owner-api.ts` with any new
   GET endpoint the screen loads.
6. Run `npm run lint && npm run typecheck && npm test && npm run test:browser`.
7. Update this document and `docs/status/implementation.md` if the route's
   status changed.

## 9. Local UI-only development

`npm run dev` starts the Vite/Vinext dev server for the UI only. API calls fail
unless something serves `/api/*` on the same origin. Options:

- Use the deployed stack + `scripts/live-acceptance-proxy.mjs` (read-only).
- Run the full Compose stack and use the live UI.
- For UI-only work, prefer the local Playwright suite, which intercepts API
  calls with fixtures and blocks external network.

Never point a local dev server at production credentials for writing.
