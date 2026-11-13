# 05 — Frontend reference (Media Manager UI)

Purpose: how the owner web UI in `web/` is built, how it loads data, and how
to change it safely. The UI is in Danish, because the owner is.

## 1. Stack and build

| Part | Choice |
|---|---|
| Language | TypeScript 5.9, strict |
| UI | React 19, React Router 7 (browser routes) |
| Data | TanStack Query 5 over `web/src/api/client.ts` |
| Styling | Tailwind CSS 4 with design tokens in `web/src/styles.css` |
| Icons, font | Lucide, Inter (self-hosted, no font CDN) |
| Build | Vite into `web/dist` |
| Runtime | `web/server.mjs`: a dependency-free Node server |
| Tests | Vitest (unit), Playwright + axe (browser, mocked API) |

The production image (`Dockerfile` target `ui`) holds only `server.mjs` and
`dist/`, with no `node_modules`, and runs as the `node` user on port 3000. The
server:

- serves hashed `/assets/*` with a one-year immutable cache, and `index.html`
  with `no-cache`;
- answers every other path without a file extension with `index.html`, so
  client routes work on reload;
- returns 404 for unknown files and for `/api/*` and `/health/*` (those
  belong to the API service; answering them would hide a routing mistake);
- refuses anything but GET and HEAD;
- sets a strict Content-Security-Policy (`script-src 'self'`; images only from
  TMDB, TheTVDB and Simkl), `nosniff`, `no-referrer`, and
  `frame-ancestors 'none'`;
- never reads, stores or proxies credentials.

The public edge (Cosmos) routes `/api` and `/health` to the API and adds the
owner credential. The UI therefore calls only relative `/api/v1/...` paths.

## 2. Data loading model

- `web/src/api/client.ts` holds every API type and loader. Errors become
  `ApiError` with the HTTP status and the server's stable `code` and `hint`.
- Pages call loaders through `useQuery`. Keys are stable arrays such as
  `["workflow", id]`, and lists that change on their own refetch every 15–60 s.
- A 4xx is never retried; network errors and 5xx are retried twice.
- Large lists page through the API with bounded page sizes (catalogue 200 per
  page up to 10,000; attention 500 per page up to 10,000).
- After any write, the page invalidates the affected queries, so what it shows
  is re-read from the server, not assumed.

### Workflow actions come from the server

`GET /api/v1/workflows[/{id}]` returns `available_actions`. Each action names
its route, method, effect, whether it applies now (or why not), a prefilled
body, and an optional confirmation phrase. `components/workflow-actions.tsx`
renders exactly those actions:

1. The owner clicks an action, and a dialog explains it in Danish
   (`actionLabel` in `i18n.ts`). It also says when the action contacts an
   external service.
2. If the server asks for a confirmation phrase, the owner must type it.
3. `runWorkflowAction` sends the method, path and body the server described.
   It accepts only `/api/v1/workflows/{uuid}/...` paths.

The UI never decides on its own that an action is allowed. The server
re-checks everything and may still answer 409.

## 3. Routing and screens

Routes live in `web/src/routes.tsx`; each screen is one file in
`web/src/pages/`.

| Path | Screen | Main API |
|---|---|---|
| `/` | Oversigt: counts, attention preview, health, the next 7 days | `dashboard`, `workflows?attention`, `system/health`, `calendar` |
| `/handling` | Kræver handling: grouped by reason, with actions | `workflows?attention`, `problems` |
| `/bibliotek` | Bibliotek: posters, Film/Serier, search, sort | `media` (paged) |
| `/bibliotek/mangler` | Find manglende: seasons of the owner's series that are neither followed nor in Plex (new and older) and films without a file or open work; **Tilføj sæson** / **Hent** per title, at most 25 per bulk click | `media/gaps`, then `media/{id}/workflows` as the title page |
| `/bibliotek/:id` | A title: metadata, seasons and episodes, workflows, files | `media/{id}/detail` |
| `/tilfoej` | Tilføj: TMDB search via Seerr, then verified add | `discovery/*`, `POST media` |
| `/anmodninger` | Seerr requests, approve or decline | `requests`, `requests/{id}/approve|decline` |
| `/arbejde` | Workflows by tab (henter, kræver handling, venter, afventer, I Plex, annulleret) | `workflows?state=` |
| `/arbejde/:id` | One workflow: steps, actions, searches, candidates, facts | `workflows/{id}`, `/searches`, `/candidates` |
| `/kalender` | Episodes from the last week and upcoming | `calendar` |
| `/kilder` | Indexers: limits, use, pacing, edit; OldBoys keys | `indexers`, `PATCH settings/integrations/{id}`, `settings/oldboys/credentials` |
| `/lister` | Followed MDBList lists: add by address, film and series folder and profile, read now, each title's outcome, stop following | `import-lists`, `PATCH`/`DELETE import-lists/{id}`, `import-lists/{id}/sync`, `import-lists/{id}/entries` |
| `/indstillinger/:section` | Profiles, library roots, Seerr rules, notifications | `settings/*` |
| `/system/:section` | Status, background queue, backups, repairs, activity | `system/*`, `backups`, `repairs`, `activity` |

Old English paths (`/media`, `/requests`, `/activity/queue`, `/v2/...`, and
so on) redirect to the new ones.

Every API code becomes Danish text in `web/src/i18n.ts`, and only there:
states, attention codes, actions, languages, and the most common server
explanations (`translateDetail`). A code that has no translation is shown in a
readable form of the code itself, never as a guess.

## 4. Pure helper modules (`web/src/lib/`)

| Module | Role |
|---|---|
| `workflow-states.ts` | Groups persisted states (acquiring, waiting, completed, attention). |
| `workflow-presentation.ts` | Step position for the progress bar. Unknown states never look further along. |
| `series-variants.ts` | Parses `season:N[:episode:M]`, rolls workflows and files up per season, and derives each episode's status. |
| `media-manager-requests.ts` | Request lifecycle tone and filters. |
| `quality-profile-rules.ts` | Reads required and preferred rules from a profile policy. |
| `links.ts` | Hrefs and the scope label ("Sæson 2", "S02E05"). |

These have no React and are covered by Vitest. The preset fixture
`lib/fixtures/quality-preset-applications.json` is generated by the Go engine
(`TestQualityPresetEditorFixtureIsCurrent` in `internal/releases`).

## 5. Truthfulness rules in the UI

1. **Never label waiting as success.** Only `plex_confirmed` and `available`
   are good. `needs_attention` needs an owner decision; `waiting` does not.
2. **Empty and error states are explicit.** The UI shows the real API error and
   never substitutes sample rows. A 401 says to log in again through Cosmos.
3. **Mutations are owner actions.** Every write happens only after a button and
   a confirmation dialog. Typed phrases are required where the server asks for
   one. Deleting a Seerr rule also asks first.
4. **No external network beyond the API and artwork hosts.** The browser never
   calls Plex, AltMount, Seerr or indexers directly (CSP `connect-src 'self'`).
5. **Server text is never lost.** Unknown details are shown exactly as the
   server wrote them.

## 6. Styling

- Tokens (`--bg`, `--surface`, `--text-2`, `--good`, and so on) are defined on
  `:root` for light mode. Dark mode follows the system or the owner's choice
  (`data-theme`, saved in `localStorage` as `mm-theme`; `public/theme-init.js`
  applies it before first paint).
- Colours are chosen for WCAG AA contrast in both themes. The axe browser test
  fails on any serious or critical violation.
- Layout works from 360 px wide: a grouped sidebar and a top search bar
  (Ctrl+K or `/` focuses it) on desktop, a top bar plus a bottom tab bar and
  drawer on mobile, and no horizontal page scroll (also tested). The palette,
  the overview's picture tiles, the library's poster grid and the title page's
  wide picture with facts and cast row (`components/title-hero.tsx`,
  `lib/title-facts.ts`) and the picture cards on Kræver handling and
  Anmodninger (`components/media-card.tsx`), the calendar's picture tiles
  (`lib/calendar-groups.ts`), Arbejde's dense list, the work page's picture
  header, the pill tabs (`PillNav`) and the coloured chips (`components/icon-chip.tsx`)
  are adapted from HolyArrs
  (AGPL-3.0-only, see
  `THIRD_PARTY_NOTICES.md`).

## 7. Tests

| Command (in `web/`) | What it covers |
|---|---|
| `npm test` | Vitest: labels and translations, formatting, library search and sort, the static server (headers, caching, path traversal, API 404, methods), and the ported pure-logic suites. |
| `npm run test:browser` | Playwright against the built bundle and `server.mjs`, with the mocked API in `tests/browser/mock-api.ts`. Every page renders without errors, horizontal scroll or serious axe violations (desktop, mobile, dark). Action flows check that nothing is sent before confirmation and then exactly the server's request. Covered flows: reconcile, cancel and abandon, request approval, adding a movie with verified identities, deleting a Seerr rule, the theme choice. |
| `scripts/test-live-website.sh` (repo root) | Read-only tour of the deployed site with real data through a loopback proxy. The browser refuses every non-GET request. |

## 8. Adding or changing a screen

1. Add or extend the loader and type in `web/src/api/client.ts`.
2. Add the page in `web/src/pages/` and its route in `web/src/routes.tsx`, plus
   a nav entry in `components/shell.tsx` if it is a top-level screen.
3. Put every new API code's Danish text in `web/src/i18n.ts`.
4. Use the shared parts in `components/ui.tsx` (`Card`, `Badge`, `Button`,
   `Dialog`, `Segmented`, `ErrorBox`, `Empty`, and so on) instead of new styles.
5. Add the page to `tests/browser/pages.spec.ts` and its mock data to
   `mock-api.ts`. Add an action test for any write.

## 9. Local development

```sh
cd web
npm ci
MM_API_URL=http://127.0.0.1:28081 npm run dev
```

`MM_API_TOKEN` may be set for local development only. The dev proxy then adds
it as the bearer token. Never commit it or put it in a build.
