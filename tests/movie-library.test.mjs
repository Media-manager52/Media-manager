import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test, { after } from "node:test";
import { fileURLToPath } from "node:url";

import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

const root = fileURLToPath(new URL("..", import.meta.url));
const vite = await createServer({
  appType: "custom",
  configFile: false,
  root,
  resolve: { alias: [
    { find: "next/image", replacement: path.join(root, "tests/next-image-stub.mjs") },
    { find: "@", replacement: root },
  ] },
  server: { middlewareMode: true },
});

after(async () => {
  await vite.close();
});

const firstID = "11111111-1111-4111-8111-111111111111";
const secondID = "22222222-2222-4222-8222-222222222222";
const firstFileID = "33333333-3333-4333-8333-333333333333";
const seriesID = "44444444-4444-4444-8444-444444444444";
const seriesFileID = "77777777-7777-4777-8777-777777777777";

const media = [
  { id: firstID, kind: "movie", title: "First Local Movie", year: 2024, identities: [{ namespace: "tmdb", value: "101", state: "verified" }], metadata: { original_title: "First Original", overview: "First persisted overview", original_language: "en", format: "Movie", status: "Released", runtime_minutes: 98, poster_url: "https://image.tmdb.org/t/p/w500/first.jpg", genres: ["Drama"], countries: ["DK"], networks: [], aliases: [], sources: [{ provider: "tmdb", provider_id: "101", url: "https://www.themoviedb.org/movie/101", role: "primary descriptive metadata" }], ratings: [{ source: "tmdb", value: 8.1, votes: 200 }], events: [], observed_at: "2026-09-01T00:00:00Z" } },
  { id: secondID, kind: "movie", title: "Second Local Movie", year: 2022, identities: [{ namespace: "imdb", value: "tt0000202", state: "verified" }] },
  { id: seriesID, kind: "series", title: "Local Series", year: 2020, identities: [{ namespace: "tvdb", value: "303", state: "verified" }], metadata: { original_title: "Local Series", overview: "Persisted series overview", original_language: "da", format: "Series", status: "Returning", release_date: "2020-02-03", season_count: 2, episode_count: 14, genres: ["Drama"], countries: ["DK"], networks: [{ name: "Local Network", source: "tvdb" }], aliases: [], sources: [], ratings: [], events: [], observed_at: "2026-09-01T00:00:00Z" } },
];

const files = [{
  id: firstFileID,
  media_id: firstID,
  media_kind: "movie",
  title: "First Local Movie",
  year: 2024,
  root_name: "Movies Primary",
  destination_relative: "First Local Movie (2024)/First Local Movie (2024).mkv",
  source_relative: "job/output.mkv",
  plex_visible_target: "/movies/First Local Movie (2024)/First Local Movie (2024).mkv",
  state: "current",
  generation: 2,
  plex_confirmed: true,
  variant: "movie",
  origin: "altmount",
  quality_profile: "Movie 1080p Danish",
  quality_evidence_state: "media_info",
  quality_evidence: { facts: { resolution: "1080p", audio_codecs: ["eac3"], audio_languages: ["en"], subtitle_languages: ["da"] } },
  source_size_bytes: 1024,
}, {
  id: seriesFileID,
  media_id: seriesID,
  media_kind: "series",
  title: "Local Series",
  year: 2020,
  root_name: "TV Primary",
  destination_relative: "Local Series (2020) {tvdb-303}/Season 01/Local Series - S01E01.mkv",
  source_relative: "series-job/episode.mkv",
  plex_visible_target: "/tv/Local Series (2020) {tvdb-303}/Season 01/Local Series - S01E01.mkv",
  state: "current",
  generation: 1,
  plex_confirmed: true,
  variant: "season:01",
  origin: "altmount",
  quality_profile: "TV 1080p Danish",
  quality_evidence_state: "media_info",
  quality_evidence: { facts: { resolution: "1080p", audio_codecs: ["aac"], subtitle_languages: ["da"] } },
  source_size_bytes: 2048,
}];

const wanted = [
  { workflow_id: "55555555-5555-4555-8555-555555555555", media_id: firstID, media_kind: "movie", title: "First Local Movie", gap: "upgrade", current_state: "plex_confirmed", target_profile: "Movie 1080p Danish", upgrades_enabled: true, variant: "movie", updated_at: "2026-09-01T00:00:00Z" },
  { workflow_id: "66666666-6666-4666-8666-666666666666", media_id: secondID, media_kind: "movie", title: "Second Local Movie", gap: "missing", current_state: "monitored", target_profile: "Movie 1080p Danish", upgrades_enabled: true, variant: "movie", updated_at: "2026-09-01T00:00:00Z" },
  { workflow_id: "88888888-8888-4888-8888-888888888888", media_id: seriesID, media_kind: "series", title: "Local Series", gap: "upgrade", current_state: "plex_confirmed", target_profile: "TV 1080p Danish", upgrades_enabled: true, variant: "season:01", updated_at: "2026-09-01T00:00:00Z" },
];

const workflows = [
  { id: wanted[0].workflow_id, media_id: firstID, media_kind: "movie", title: "First Local Movie", year: 2024, variant: "movie", state: "plex_confirmed", revision: 4, adopted_replacement_confirmation_required: false, created_at: "2026-08-31T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" },
  { id: wanted[1].workflow_id, media_id: secondID, media_kind: "movie", title: "Second Local Movie", year: 2022, variant: "movie", state: "monitored", revision: 1, adopted_replacement_confirmation_required: false, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" },
  { id: wanted[2].workflow_id, media_id: seriesID, media_kind: "series", title: "Local Series", year: 2020, variant: "season:01", state: "plex_confirmed", revision: 5, adopted_replacement_confirmation_required: false, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" },
];

const baseFilters = {
  query: "",
  summary: "all",
  quality: "",
  resolution: "",
  audio: "",
  language: "",
  availability: "all",
  root: "",
  plexConfirmedOnly: false,
  managedOnly: false,
};

test("builds and filters movies only from persisted API fields", async () => {
  const { buildMediaLibraryRecords, filterMediaLibraryRecords } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
  const records = buildMediaLibraryRecords("movie", media, files, wanted, workflows);

  assert.equal(records.length, 2);
  assert.equal(records[0].title, "First Local Movie");
  assert.deepEqual(records[0].resolutions, ["1080p"]);
  assert.deepEqual(records[0].languages, ["da", "en"]);
  assert.equal(records[0].available, true);
  assert.equal(records[0].upgradable, true);
  assert.equal(records[0].metadata.overview, "First persisted overview");
  assert.equal(records[1].missing, true);
  assert.equal(records[1].monitoring, true);

  for (const [change, expected] of [
    [{ query: "tmdb 101" }, [firstID]],
    [{ quality: "Movie 1080p Danish" }, [firstID, secondID]],
    [{ resolution: "1080p" }, [firstID]],
    [{ audio: "eac3" }, [firstID]],
    [{ language: "da" }, [firstID]],
    [{ availability: "available" }, [firstID]],
    [{ root: "Movies Primary" }, [firstID]],
    [{ summary: "upgradable" }, [firstID]],
    [{ summary: "missing" }, [secondID]],
    [{ summary: "monitoring" }, [secondID]],
    [{ plexConfirmedOnly: true }, [firstID]],
    [{ managedOnly: true }, [firstID]],
  ]) {
    const result = filterMediaLibraryRecords(records, { ...baseFilters, ...change });
    assert.deepEqual(result.map((item) => item.id), expected);
  }
});

test("joins every related record at the supported catalog bound", async () => {
  const { buildMediaLibraryRecords, filterMediaLibraryRecords } = await vite.ssrLoadModule("/lib/media-library.ts");
  const count = 10_000;
  const largeMedia = Array.from({ length: count }, (_, index) => ({
    id: `media-${index}`,
    kind: "movie",
    title: `Movie ${String(index).padStart(5, "0")}`,
    year: 2000 + (index % 25),
    identities: [{ namespace: "tmdb", value: String(index), state: "verified" }],
  }));
  const largeFiles = largeMedia.map((item, index) => ({
    ...files[0],
    id: `file-${index}`,
    media_id: item.id,
  }));
  const largeWanted = largeMedia.map((item, index) => ({
    ...wanted[0],
    workflow_id: `wanted-${index}`,
    media_id: item.id,
  }));
  const largeWorkflows = largeMedia.map((item, index) => ({
    ...workflows[0],
    id: `workflow-${index}`,
    media_id: item.id,
  }));

  const records = buildMediaLibraryRecords("movie", largeMedia, largeFiles, largeWanted, largeWorkflows);

  assert.equal(records.length, count);
  assert.equal(new Set(records.map((item) => item.id)).size, count);
  for (const index of [0, Math.floor(count / 2), count - 1]) {
    const record = records.find((item) => item.id === `media-${index}`);
    assert.equal(record.files[0].id, `file-${index}`);
    assert.equal(record.wanted[0].workflow_id, `wanted-${index}`);
    assert.equal(record.workflows[0].id, `workflow-${index}`);
  }
  assert.deepEqual(
    filterMediaLibraryRecords(records, { ...baseFilters, query: "tmdb 9374" }).map((item) => item.id),
    ["media-9374"],
  );
});

test("movie card is a native keyboard control with truthful selection and status", async () => {
  const { buildMediaLibraryRecords, MediaLibraryCard } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
  const [record] = buildMediaLibraryRecords("movie", media, files, wanted, workflows);
  const html = renderToStaticMarkup(React.createElement(MediaLibraryCard, { item: record, selected: true, onSelect() {} }));

  assert.match(html, /<button/);
  assert.match(html, /type="button"/);
  assert.match(html, /aria-pressed="true"/);
  assert.match(html, /First Local Movie, 2024\. Available · upgrade wanted/);
  assert.match(html, />1080p</i);
  assert.match(html, /https:\/\/image\.tmdb\.org\/t\/p\/w500\/first\.jpg/);
});

test("changing the selected record cannot retain previous inspector data", async () => {
  const { buildMediaLibraryRecords, MediaInspector } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
  const records = buildMediaLibraryRecords("movie", media, files, wanted, workflows);
  const props = { tab: "files", auto: false, onTabChange() {}, onClose() {} };
  const first = renderToStaticMarkup(React.createElement(MediaInspector, { ...props, item: records[0] }));
  const second = renderToStaticMarkup(React.createElement(MediaInspector, { ...props, item: records[1] }));
  const firstOverview = renderToStaticMarkup(React.createElement(MediaInspector, { ...props, tab: "overview", item: records[0] }));
  const firstMetadata = renderToStaticMarkup(React.createElement(MediaInspector, { ...props, tab: "metadata", item: records[0] }));

  assert.match(first, /First Local Movie/);
  assert.match(firstOverview, /First persisted overview/);
  assert.match(firstMetadata, /https:\/\/www\.themoviedb\.org\/movie\/101/);
  assert.match(first, new RegExp(firstFileID));
  assert.match(second, /Second Local Movie/);
  assert.doesNotMatch(second, /First Local Movie/);
  assert.doesNotMatch(second, /First persisted overview/);
  assert.doesNotMatch(second, new RegExp(firstFileID));
  for (const tab of ["overview", "files", "metadata", "history"]) assert.match(second, new RegExp(`>${tab}<`, "i"));
});

test("series records use real season state and exact series navigation", async () => {
  const { buildMediaLibraryRecords, filterMediaLibraryRecords, MediaLibraryCard, MediaInspector } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
  const [record] = buildMediaLibraryRecords("series", media, files, wanted, workflows);

  assert.equal(record.kind, "series");
  assert.equal(record.available, true);
  assert.equal(record.upgradable, true);
  assert.equal(record.files[0].variant, "season:01");
  assert.equal(record.metadata.season_count, 2);
  assert.deepEqual(filterMediaLibraryRecords([record], { ...baseFilters, query: "tvdb 303" }).map((item) => item.id), [seriesID]);

  const card = renderToStaticMarkup(React.createElement(MediaLibraryCard, { item: record, selected: true, onSelect() {} }));
  const inspector = renderToStaticMarkup(React.createElement(MediaInspector, { item: record, tab: "overview", auto: false, onTabChange() {}, onClose() {} }));
  const partial = renderToStaticMarkup(React.createElement(MediaLibraryCard, { item: { ...record, missing: true }, selected: false, onSelect() {} }));
  assert.match(card, /Local Series, 2020\. Available · upgrade wanted/);
  assert.match(card, /2 seasons/);
  // Without provider artwork the card is a tinted title card, never a placeholder request.
  assert.doesNotMatch(card, /<img/);
  assert.match(card, /text-shadow[^>]*>Local Series</);
  assert.match(inspector, new RegExp(`/media/series/${seriesID}`));
  assert.match(inspector, />TV show details</);
  assert.match(inspector, />Seasons</);
  assert.match(inspector, />2</);
  assert.match(inspector, /Local Network/);
  const refreshButton = inspector.match(/<button[^>]*>[\s\S]*?Refresh metadata<\/button>/);
  assert.ok(refreshButton);
  assert.doesNotMatch(refreshButton[0], /\sdisabled(?:=|>)/);
  assert.match(partial, /Partially available/);
});

test("library controls preserve routes and expose working view and tab state", async () => {
  const [shell, screen, routesModule] = await Promise.all([
    readFile(path.join(root, "components/media-manager/app-shell.tsx"), "utf8"),
    readFile(path.join(root, "components/media-manager/live-operational-screens.tsx"), "utf8"),
    vite.ssrLoadModule("/lib/media-manager-routes.ts"),
  ]);

  const v2Shell = await readFile(path.join(root, "components/media-manager/v2-app.tsx"), "utf8");
  for (const route of Object.keys(routesModule.routeTitles)) assert.match(route.startsWith("/v2") ? v2Shell : shell, new RegExp(`href: ["']${route.replaceAll("/", "\\/")}["']|path === ["']${route.replaceAll("/", "\\/")}["']`));
  assert.match(shell, /\/media\/movies\/\$mediaId/);
  assert.match(shell, /\/media\/series\/\$mediaId/);
  assert.match(screen, /aria-label="Grid view"[\s\S]*setView\("grid"\)/);
  assert.match(screen, /aria-label="List view"[\s\S]*setView\("list"\)/);
  assert.match(screen, /value="overview"/);
  assert.match(screen, /value="files"/);
  assert.match(screen, /value="metadata"/);
  assert.match(screen, /value="history"/);
});

test("desktop and mobile layout contracts remain explicit in the focused screen", async () => {
  const [shell, screen] = await Promise.all([
    readFile(path.join(root, "components/media-manager/app-shell.tsx"), "utf8"),
    readFile(path.join(root, "components/media-manager/live-operational-screens.tsx"), "utf8"),
  ]);

  assert.match(shell, /--sidebar-width": "15rem"/);
  assert.match(shell, /collapsible="icon"/);
  assert.match(shell, /h-14/);
  assert.match(shell, /aria-label="Mobile navigation" className="fixed inset-x-0 bottom-0[^"]*md:hidden"/);
  assert.match(screen, /xl:grid-cols-\[minmax\(0,1fr\)_320px\]/);
  assert.match(screen, /mm-library-root/);
  assert.match(screen, /sm:px-5/);
  assert.match(screen, /grid-template-columns:repeat\(auto-fill,minmax\(150px,1fr\)\)/);
  assert.match(screen, /const basePageSize = 24/);
  assert.match(screen, /sm:w-\[380px\][\s\S]*xl:static/);
  assert.match(screen, /sm:top-14 sm:w-\[380px\]/);
});

test("catalog loader returns only complete bounded pagination results", async (t) => {
  const { loadCatalogMedia } = await vite.ssrLoadModule("/lib/media-manager-api.ts");
  const response = (data) => new Response(JSON.stringify({ data }), { status: 200, headers: { "Content-Type": "application/json" } });
  const catalogItems = (offset, count) => Array.from({ length: count }, (_, index) => ({
    id: `catalog-${offset + index}`,
    kind: "movie",
    title: `Catalog Movie ${offset + index}`,
    identities: [],
  }));
  const withFetch = async (fetchImplementation, action) => {
    const originalFetch = globalThis.fetch;
    globalThis.fetch = fetchImplementation;
    try {
      return await action();
    } finally {
      globalThis.fetch = originalFetch;
    }
  };

  await t.test("stops after a catalog smaller than one page", async () => {
    const controller = new AbortController();
    const requested = [];
    const loaded = await withFetch(async (input, init) => {
      requested.push(String(input));
      assert.equal(init.signal, controller.signal);
      return response(catalogItems(0, 3));
    }, () => loadCatalogMedia(controller.signal));

    assert.equal(loaded.length, 3);
    assert.deepEqual(requested, ["/api/v1/media?limit=200&offset=0"]);
  });

  await t.test("loads multiple pages with exact offsets and no duplicates", async () => {
    const controller = new AbortController();
    const records = catalogItems(0, 450);
    const offsets = [];
    const loaded = await withFetch(async (input, init) => {
      const requestURL = new URL(String(input), "http://media-manager.test");
      assert.equal(requestURL.pathname, "/api/v1/media");
      assert.equal(requestURL.searchParams.get("limit"), "200");
      assert.equal(init.signal, controller.signal);
      const offset = Number(requestURL.searchParams.get("offset"));
      offsets.push(offset);
      return response(records.slice(offset, offset + 200));
    }, () => loadCatalogMedia(controller.signal));

    assert.deepEqual(offsets, [0, 200, 400]);
    assert.equal(loaded.length, 450);
    assert.equal(new Set(loaded.map((item) => item.id)).size, 450);
    assert.deepEqual(loaded.map((item) => item.id), records.map((item) => item.id));
  });

  await t.test("abort rejects instead of returning the first partial page", async () => {
    const controller = new AbortController();
    let requests = 0;
    await withFetch(async (_input, init) => {
      requests += 1;
      assert.equal(init.signal, controller.signal);
      if (requests === 1) {
        controller.abort();
        return response(catalogItems(0, 200));
      }
      throw new DOMException("The operation was aborted", "AbortError");
    }, async () => {
      await assert.rejects(loadCatalogMedia(controller.signal), { name: "AbortError" });
    });
    assert.equal(requests, 2);
  });

  await t.test("safety bound rejects instead of returning a truncated catalog", async () => {
    const offsets = [];
    await withFetch(async (input) => {
      const requestURL = new URL(String(input), "http://media-manager.test");
      const offset = Number(requestURL.searchParams.get("offset"));
      offsets.push(offset);
      return response(catalogItems(offset, 200));
    }, async () => {
      await assert.rejects(loadCatalogMedia(), /safe 10000-record load limit/);
    });
    assert.equal(offsets.length, 50);
    assert.equal(offsets.at(-1), 9800);
  });
});

test("movie and series library loaders make only the expected same-origin API reads", async () => {
  const requested = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (input) => {
    const requestPath = String(input);
    if (!requestPath.startsWith("/api/v1/")) throw new Error(`Unexpected network request: ${requestPath}`);
    requested.push(requestPath);
    const data = requestPath.startsWith("/api/v1/media?") ? media
      : requestPath.startsWith("/api/v1/files?") ? files
        : requestPath.startsWith("/api/v1/wanted?") ? wanted
          : requestPath.startsWith("/api/v1/workflows?") ? workflows
            : null;
    if (data === null) throw new Error(`Unexpected API request: ${requestPath}`);
    return new Response(JSON.stringify({ data }), { status: 200, headers: { "Content-Type": "application/json" } });
  };

  try {
    const { movieLibraryLoader, seriesLibraryLoader } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
    const movieRecords = await movieLibraryLoader(new AbortController().signal);
    assert.equal(movieRecords.length, 2);
    assert.deepEqual(requested.sort(), [
      "/api/v1/files?limit=500&offset=0",
      "/api/v1/media?limit=200&offset=0",
      "/api/v1/wanted?limit=500&offset=0",
      "/api/v1/workflows?limit=500&offset=0&active=false",
    ]);
    requested.length = 0;
    const seriesRecords = await seriesLibraryLoader(new AbortController().signal);
    assert.equal(seriesRecords.length, 1);
    assert.equal(seriesRecords[0].id, seriesID);
    assert.deepEqual(requested.sort(), [
      "/api/v1/files?limit=500&offset=0",
      "/api/v1/media?limit=200&offset=0",
      "/api/v1/wanted?limit=500&offset=0",
      "/api/v1/workflows?limit=500&offset=0&active=false",
    ]);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("bounded owner attention summarizes the remainder instead of flooding the panel", async () => {
  const { OwnerAttention } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
  const items = Array.from({ length: 9 }, (_, index) => ({
    workflow_id: `w${index}`,
    media_id: `m${index}`,
    media_kind: "movie",
    title: `Attention Movie ${index}`,
    variant: "default",
    gap: "missing",
    current_state: "needs_attention",
    target_profile: "UHD Reference",
    upgrades_enabled: true,
    attention_detail: `Persisted reason ${index}`,
    owner_action_required: true,
    updated_at: "2026-09-01T00:00:00Z",
  }));

  const bounded = renderToStaticMarkup(React.createElement(OwnerAttention, { items, workflows: [], limit: 3 }));
  assert.match(bounded, /Attention Movie 0/);
  assert.match(bounded, /Attention Movie 2/);
  assert.doesNotMatch(bounded, /Attention Movie 3/);
  assert.match(bounded, /Showing 3 of 9 items that need attention/);
  assert.match(bounded, /View all in Wanted/);

  const full = renderToStaticMarkup(React.createElement(OwnerAttention, { items, workflows: [] }));
  assert.match(full, /Attention Movie 8/);
  assert.doesNotMatch(full, /Showing 3 of 9/);
});

test("paged owner list loaders carry exact server offsets and stable page metadata", async () => {
  const { loadManagedFilesPage, loadRequestsPage, loadWantedPage, loadWorkflowsPage } = await vite.ssrLoadModule("/lib/media-manager-api.ts");
  const response = (data, meta) => new Response(JSON.stringify({ data, meta }), { status: 200, headers: { "Content-Type": "application/json" } });
  const originalFetch = globalThis.fetch;
  const requested = [];
  globalThis.fetch = async (input) => {
    const url = new URL(String(input), "http://media-manager.test");
    requested.push(url.pathname + url.search);
    if (url.pathname === "/api/v1/workflows") return response([{ id: "wf-3" }], { count: 1, limit: 2, offset: 2 });
    if (url.pathname === "/api/v1/wanted") return response([], { count: 0, limit: 2, offset: 2 });
    if (url.pathname === "/api/v1/files") return response([{ id: "file-3" }], { count: 1, limit: 2, offset: 2 });
    if (url.pathname === "/api/v1/requests") return response([{ id: "request-3" }], { count: 1, limit: 2, offset: 2 });
    throw new Error(`unexpected ${url.pathname}`);
  };
  try {
    const workflows = await loadWorkflowsPage({ active: false, limit: 2, offset: 2 });
    const wanted = await loadWantedPage({ limit: 2, offset: 2 });
    const files = await loadManagedFilesPage({ limit: 2, offset: 2 });
    const requests = await loadRequestsPage({ limit: 2, offset: 2 });
    assert.deepEqual(requested, [
      "/api/v1/workflows?limit=2&offset=2&active=false",
      "/api/v1/wanted?limit=2&offset=2",
      "/api/v1/files?limit=2&offset=2",
      "/api/v1/requests?limit=2&offset=2",
    ]);
    assert.equal(workflows.items[0].id, "wf-3");
    assert.equal(wanted.hasMore, false);
    assert.equal(files.items[0].id, "file-3");
    assert.equal(requests.items[0].id, "request-3");
  } finally {
    globalThis.fetch = originalFetch;
  }
});
