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

const media = [
  { id: firstID, kind: "movie", title: "First Local Movie", year: 2024, identities: [{ namespace: "tmdb", value: "101", state: "verified" }] },
  { id: secondID, kind: "movie", title: "Second Local Movie", year: 2022, identities: [{ namespace: "imdb", value: "tt0000202", state: "verified" }] },
  { id: "44444444-4444-4444-8444-444444444444", kind: "series", title: "Local Series", year: 2020, identities: [] },
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
}];

const wanted = [
  { workflow_id: "55555555-5555-4555-8555-555555555555", media_id: firstID, media_kind: "movie", title: "First Local Movie", gap: "upgrade", current_state: "plex_confirmed", target_profile: "Movie 1080p Danish", upgrades_enabled: true, variant: "movie", updated_at: "2026-09-01T00:00:00Z" },
  { workflow_id: "66666666-6666-4666-8666-666666666666", media_id: secondID, media_kind: "movie", title: "Second Local Movie", gap: "missing", current_state: "monitored", target_profile: "Movie 1080p Danish", upgrades_enabled: true, variant: "movie", updated_at: "2026-09-01T00:00:00Z" },
];

const workflows = [
  { id: wanted[0].workflow_id, media_id: firstID, media_kind: "movie", title: "First Local Movie", year: 2024, variant: "movie", state: "plex_confirmed", revision: 4, adopted_replacement_confirmation_required: false, created_at: "2026-08-31T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" },
  { id: wanted[1].workflow_id, media_id: secondID, media_kind: "movie", title: "Second Local Movie", year: 2022, variant: "movie", state: "monitored", revision: 1, adopted_replacement_confirmation_required: false, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" },
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
  const { buildMovieLibraryRecords, filterMovieLibraryRecords } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
  const records = buildMovieLibraryRecords(media, files, wanted, workflows);

  assert.equal(records.length, 2);
  assert.equal(records[0].title, "First Local Movie");
  assert.deepEqual(records[0].resolutions, ["1080p"]);
  assert.deepEqual(records[0].languages, ["da", "en"]);
  assert.equal(records[0].available, true);
  assert.equal(records[0].upgradable, true);
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
    const result = filterMovieLibraryRecords(records, { ...baseFilters, ...change });
    assert.deepEqual(result.map((item) => item.id), expected);
  }
});

test("movie card is a native keyboard control with truthful selection and status", async () => {
  const { buildMovieLibraryRecords, MovieLibraryCard } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
  const [record] = buildMovieLibraryRecords(media, files, wanted, workflows);
  const html = renderToStaticMarkup(React.createElement(MovieLibraryCard, { item: record, selected: true, onSelect() {} }));

  assert.match(html, /<button/);
  assert.match(html, /type="button"/);
  assert.match(html, /aria-pressed="true"/);
  assert.match(html, /First Local Movie, 2024\. Available · upgrade wanted/);
  assert.match(html, />1080p</i);
});

test("changing the selected record cannot retain previous inspector data", async () => {
  const { buildMovieLibraryRecords, MovieInspector } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
  const records = buildMovieLibraryRecords(media, files, wanted, workflows);
  const props = { tab: "files", auto: false, onTabChange() {}, onClose() {} };
  const first = renderToStaticMarkup(React.createElement(MovieInspector, { ...props, item: records[0] }));
  const second = renderToStaticMarkup(React.createElement(MovieInspector, { ...props, item: records[1] }));

  assert.match(first, /First Local Movie/);
  assert.match(first, new RegExp(firstFileID));
  assert.match(second, /Second Local Movie/);
  assert.doesNotMatch(second, /First Local Movie/);
  assert.doesNotMatch(second, new RegExp(firstFileID));
  for (const tab of ["overview", "files", "metadata", "history"]) assert.match(second, new RegExp(`>${tab}<`, "i"));
});

test("library controls preserve routes and expose working view and tab state", async () => {
  const [shell, screen, routesModule] = await Promise.all([
    readFile(path.join(root, "components/media-manager/app-shell.tsx"), "utf8"),
    readFile(path.join(root, "components/media-manager/live-operational-screens.tsx"), "utf8"),
    vite.ssrLoadModule("/lib/media-manager-routes.ts"),
  ]);

  for (const route of Object.keys(routesModule.routeTitles)) assert.match(shell, new RegExp(`href: ["']${route.replaceAll("/", "\\/")}["']|path === ["']${route.replaceAll("/", "\\/")}["']`));
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

  assert.match(shell, /--sidebar-width": "184px"/);
  assert.match(shell, /h-\[60px\]/);
  assert.match(screen, /xl:grid-cols-\[minmax\(0,1fr\)_245px\]/);
  assert.match(screen, /mm-library-root/);
  assert.match(screen, /sm:px-4/);
  assert.match(screen, /lg:grid-cols-5/);
  assert.match(screen, /const pageSize = 10/);
  assert.match(screen, /sm:w-\[330px\][\s\S]*xl:static/);
  assert.match(screen, /md:hidden" aria-label="Mobile navigation"/);
});

test("movie library loader makes only the four expected same-origin API reads", async () => {
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
    const { movieLibraryLoader } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
    const records = await movieLibraryLoader(new AbortController().signal);
    assert.equal(records.length, 2);
    assert.deepEqual(requested.sort(), [
      "/api/v1/files?limit=300",
      "/api/v1/media?limit=500",
      "/api/v1/wanted?limit=300",
      "/api/v1/workflows?limit=300&active=false",
    ]);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
