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
| Icons | `lucide-react` |
| Charts | `recharts` |
| Router | Next App Router file routing; route → screen mapping is a hand-written switch (see §3) |
| Database in UI | **None.** `db/schema.ts` is empty scaffolding; `db/index.ts` is unused D1 boilerplate. |

Key files:

- `app/layout.tsx` — HTML shell, fonts, `globals.css`.
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

The UI is same-origin by construction: `/api/*` is routed by the public edge
(Cosmos) or the loopback acceptance proxy. There is no API base URL config and
no token handling in browser code.

## 3. Routing and screens

Route metadata lives in `lib/media-manager-routes.ts`:

- `routeTitles` maps every static route to `{ area, title, subtitle }`.
- `normalizeRoute` collapses detail URLs to tokens:
  `/media/movies/<uuid>` → `/media/movies/$mediaId`, same for series, files,
  and indexers.
- `isKnownRoute` accepts only the known static routes plus UUID detail routes.

`components/media-manager/app-shell.tsx` is the single app shell:

- Sidebar navigation (`liveNavigation`) grouped into Product, Operations,
  Sources, Settings, System, Advanced. Every route stays reachable; groups are
  collapsible and Advanced is visually de-emphasized.
- Ctrl/Cmd-K command palette.
- Service status footer (health + version) and the **Owner attention** bell
  (loads `loadWanted` + `loadWorkflows` and uses `collectOwnerAttention`).
- `RouteContent` is the authoritative route → screen switch. Unknown known-route
  combinations render `LiveUnavailableScreen`.
- Library routes (`/media`, `/media/series`) and media detail routes use a
  full-height layout; other routes get a padded page with `PageHeader`.

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
| `LiveOperationalDashboardScreen` | `/` |
| `LiveLibraryScreen` (`mediaKind`) | `/media`, `/media/series` |
| `LiveRequestsScreen` | `/requests` |
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
`PageHeader`, `Panel`, `MetricCard`, `describeStatus`, `StateBadge`,
`StatusChip`, `KeyValue`, `WorkProgress`, `InlineNotice`, `EmptyAction`,
`SectionLabel`.

## 4. Pure helper modules (`lib/`)

Keep classification/filtering/formatting logic here so screens stay testable.

| Module | Responsibility |
| --- | --- |
| `media-manager-api.ts` | Types, fetchers, mutations, envelope handling. |
| `workflow-states.ts` | Canonical UI grouping of workflow states (`activelyAcquiring`, `waiting`, `completed`, `ownerAttention`), `requiresOwnerAttention`, `collectOwnerAttention`. The backend owns the durable machine; this only classifies. |
| `media-library.ts` | `buildMediaLibraryRecords` (join media + workflows + files), filters, summaries, initial filter state. Pure; benchmarked by `npm run bench:library`. |
| `series-variants.ts` | Parse/format `season:NN[:episode:NN]`, `rollupSeriesSeasons` for season coverage UI. |
| `media-manager-requests.ts` | Request lifecycle tone/labels, filter sets, `filterRequests`, counts. |
| `media-manager-data.ts` | `StatusTone` type + shared display constants. |
| `media-manager-routes.ts` | Route titles/normalization/validation. |
| `utils.ts` | `cn` (clsx + tailwind-merge). |

## 5. Truthfulness rules in the UI

1. **No fixture data in the live artifact.** Development concept files are not
   imported. `tests/rendered-html.test.mjs` asserts the rendered shell has no
   `codex-preview=development` metadata, and the artifact tests scan for fixture
   records.
2. **Never label waiting as success.** Use `workflow-states.ts` groups;
   `needs_attention` requires an owner action, `waiting` does not.
3. **Empty/error states are explicit.** Show the real API error; do not
   substitute sample rows.
4. **Mutations are owner actions.** All state-changing calls happen only from
   explicit button/confirmation handlers. Dangerous operations require typed
   exact phrases (adoption execute, adopted restore, real-file purge, adopted
   replacement).
5. **No external network from the UI at runtime beyond API + artwork hosts.**
   Plex/AltMount/Seerr are never called from the browser.

## 6. Styling

- Tailwind 4 via `@tailwindcss/postcss`; design tokens and dark theme live in
  `app/globals.css`.
- The product is a dense dark owner console; use the existing primitives and
  Tailwind utilities rather than adding CSS modules.
- Accessibility is expected: keyboard navigation, focus rings, aria labels on
  icon buttons, and axe checks in the browser suite.

## 7. Tests

Local gates (no network required beyond localhost):

| Command | Covers |
| --- | --- |
| `npm run lint` | ESLint (Next config). |
| `npm run typecheck` | `tsc --noEmit`. |
| `npm test` | Builds the UI then runs `tests/*.test.mjs` with `node --test`: rendered HTML/worker, UI components, movie library joins, series variants, requests filters. |
| `npm run test:browser` | Builds then runs Playwright Chromium specs in `tests/browser/` (add-media, media-library, profiles, workflow-recovery) against a local `vinext start` on port 4174. |
| `npm run bench:library` | Deterministic benchmark of the library transformation. |
| `npm run test:live-browser` | Read-only acceptance against the **deployed** UI/API through `scripts/live-acceptance-proxy.mjs`; see [07-operations.md](../operations/runbook.md#6-health-checks-and-smoke-tests). |

Browser tests use Playwright + `@axe-core/playwright` and block unexpected
network access. Preserve that property.

## 8. Adding or changing a screen

1. Add the route to `routeTitles` in `lib/media-manager-routes.ts` (and detail
   matching in `normalizeRoute`/`isKnownRoute` if it is a detail route).
2. Add the API loader/mutation and types in `lib/media-manager-api.ts`.
3. Implement/reuse the screen. Put pure logic in `lib/` and add a unit test.
4. Wire the component in `RouteContent` (`app-shell.tsx`) and add a nav item if
   it is owner-facing.
5. Add/extend a Playwright spec or `tests/*.test.mjs` case.
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
